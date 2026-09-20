package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// deleteAccountConfirmPhrase must be typed exactly, byte-for-byte, to
// permanently delete a member's account — same "no accidental confirming"
// guard as finalizePhaseCloseConfirmPhrase: there is no UI affordance that
// can trigger this by a stray click, only a deliberate, typed action.
const deleteAccountConfirmPhrase = "DELETE THIS ACCOUNT"

// OffboardingHandler proxies member-offboarding actions (deactivate/
// permanently delete the @kthais.com Google Workspace + Mattermost
// accounts) to onboarding-service, which holds those credentials — same
// isolation reasoning as ManualOnboardingHandler applied to onboarding
// itself. Restricted to the head of IT specifically, not just any admin
// and not just any IT admin, given the blast radius: this is the single
// most destructive action the whole system can take on a real member's
// accounts. Applies to any user who has ever signed in with a @kthais.com
// address — deliberately independent of onboarding-service's own
// OnboardingRecord table, which only covers onboardings this system itself
// ran; members onboarded before this system existed still need to be
// offboardable.
type OffboardingHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewOffboardingHandler(db *gorm.DB, cfg *config.Config) *OffboardingHandler {
	return &OffboardingHandler{db: db, cfg: cfg}
}

func (h *OffboardingHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/offboarding")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("/deactivate", h.Deactivate)
	admin.POST("/delete", h.Delete)

	headOfIT := r.Group("/admin/head-of-it")
	headOfIT.Use(middleware.AuthRequiredJWT(h.cfg))
	headOfIT.Use(middleware.RoleRequired(h.cfg, "admin"))
	headOfIT.GET("", h.ListHeadsOfIT)
}

// requesterIsHeadOfIT checks Profile.BoardRole == BoardRoleHeadOfIT —
// deliberately not requesterIsHeadOfTeam/AdminTeam, which any admin can set
// on themselves via UpdateInterviewSettings. See Profile.BoardRole's doc
// comment for why Head of IT specifically only ever moves via
// BoardRoleHandler's transfer endpoint, never a self-editable field.
func requesterIsHeadOfIT(db *gorm.DB, userID uuid.UUID) (bool, error) {
	var profile models.Profile
	if err := db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		return false, err
	}
	return profile.BoardRole == models.BoardRoleHeadOfIT, nil
}

// requireHeadOfIT is deliberately stricter than the rest of this admin
// group: RoleRequired("admin") above only proves the caller is *an* admin.
// Every handler in this file re-checks head-of-IT specifically.
func (h *OffboardingHandler) requireHeadOfIT(c *gin.Context) (requesterID uuid.UUID, adminEmail string, ok bool) {
	userID, adminEmail, authOK := getAdminIdentity(c)
	if !authOK {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.UUID{}, "", false
	}
	isHeadOfIT, err := requesterIsHeadOfIT(h.db, userID)
	if err != nil || !isHeadOfIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the head of IT can offboard a member's account"})
		return uuid.UUID{}, "", false
	}
	return userID, adminEmail, true
}

// isKthaisEmail guards against offboarding actions ever being pointed at
// something other than a real KTHAIS-issued address (a personal Gmail
// login, a typo, etc.) — onboarding-service re-checks this too, but
// failing fast here means a bad request never even reaches the service
// holding the credentials.
func isKthaisEmail(email string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(email)), "@kthais.com")
}

type offboardingDeactivateRequest struct {
	Email string `json:"email" binding:"required"`
}

// Deactivate suspends the Google Workspace account and deactivates the
// Mattermost account for email. Reversible on both sides from each
// system's own admin console — see onboarding-service's
// offboarding.Service.Deactivate.
func (h *OffboardingHandler) Deactivate(c *gin.Context) {
	_, adminEmail, ok := h.requireHeadOfIT(c)
	if !ok {
		return
	}

	var req offboardingDeactivateRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isKthaisEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid @kthais.com email is required"})
		return
	}

	log.Printf("offboarding: %s is deactivating the account for %s", adminEmail, req.Email)
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))
	status := h.proxy(c, "/internal/offboarding/deactivate", targetEmail)
	if status != http.StatusOK {
		return
	}

	// Best-effort, doesn't affect the response already written above: the
	// real, external deactivation already succeeded by the time this runs.
	// This is the only local record that the account is deactivated —
	// LumaHandler.SyncAll relies on it to not silently undo this by
	// re-adding the member to Luma.
	now := time.Now()
	if err := h.db.Model(&models.User{}).Where("email = ?", targetEmail).Update("deactivated_at", &now).Error; err != nil {
		log.Printf("offboarding: deactivated %s but failed to record it locally: %v", targetEmail, err)
	}
}

type offboardingDeleteRequest struct {
	Email   string `json:"email" binding:"required"`
	Confirm string `json:"confirm"`
}

// Delete deletes this app's own local User/Profile record for email (if one
// exists) FIRST, then permanently deletes the Google Workspace account and
// attempts to permanently delete the Mattermost account. Local-first is
// deliberate, not incidental: deleteUserAndProfile's row-locked transaction
// is what actually refuses to delete an account currently holding one of
// the eight exactly-one board roles — running it before the irreversible
// external call means a request targeting, say, the sole Head of IT is
// refused here, before any real account is touched, rather than after.
// Deactivate leaves the local record alone since it's meant to be
// reversible; Delete cannot be undone from this system and requires typing
// deleteAccountConfirmPhrase exactly.
func (h *OffboardingHandler) Delete(c *gin.Context) {
	_, adminEmail, ok := h.requireHeadOfIT(c)
	if !ok {
		return
	}

	var req offboardingDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isKthaisEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid @kthais.com email is required"})
		return
	}
	if req.Confirm != deleteAccountConfirmPhrase {
		c.JSON(http.StatusBadRequest, gin.H{"error": `type "` + deleteAccountConfirmPhrase + `" exactly to confirm`})
		return
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))

	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	// Looked up by User, not Profile: RegisteredUserRequired shows a User
	// can exist with no matching Profile at all (signed in but never
	// finished profile setup) — looking up Profile here would treat that
	// as "no local record," skip deleteUserAndProfile entirely, and leave
	// the orphaned User row (still visible in the admin Users list) behind.
	// deleteUserAndProfile's own head-of-IT check is Profile-based and
	// already correct either way: a User with no Profile can't be a head.
	var targetUser models.User
	lookupErr := h.db.Where("email = ?", targetEmail).First(&targetUser).Error
	switch {
	case lookupErr == nil:
		if err := deleteUserAndProfile(h.db, targetUser.ID); err != nil {
			if errors.Is(err, errCannotDeleteAccountHoldingBoardRole) {
				c.JSON(http.StatusConflict, gin.H{"error": "can't delete an account that currently holds a board role — transfer it away first"})
				return
			}
			log.Printf("offboarding: failed to delete local record for %s: %v", targetEmail, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete local user record"})
			return
		}
	case errors.Is(lookupErr, gorm.ErrRecordNotFound):
		// No local record for this email at all (e.g. provisioned but
		// never actually logged into the site) — nothing to protect or
		// clean up locally, proceed straight to the external deletion.
	default:
		// A real database error, not "no such row" — treated the same as
		// any other failure to reach a safe state: refuse rather than risk
		// deleting real external accounts without having actually checked
		// the Head-of-IT invariant.
		log.Printf("offboarding: failed to look up local user for %s: %v", targetEmail, lookupErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up local user record"})
		return
	}

	log.Printf("offboarding: %s is PERMANENTLY DELETING the account for %s", adminEmail, targetEmail)
	payload, err := json.Marshal(map[string]string{"email": targetEmail})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	status, body, err := callOnboardingService(h.cfg, http.MethodPost, "/internal/offboarding/delete", payload)
	if err != nil {
		log.Printf("offboarding /internal/offboarding/delete: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "the local account record was removed, but the onboarding service is unreachable — the Google Workspace/Mattermost accounts may still exist and need manual cleanup",
		})
		return
	}

	c.Data(status, "application/json; charset=utf-8", body)
}

// ListHeadsOfIT returns the emails of every current Head of IT. Open to any
// admin (not head-of-IT-gated) — who holds this power isn't sensitive.
// Kept for backward compatibility; GET /admin/board-role (BoardRoleHandler)
// supersedes it for new frontend code, covering all nine board roles in one
// call instead of just this one.
func (h *OffboardingHandler) ListHeadsOfIT(c *gin.Context) {
	var emails []string
	if err := h.db.Model(&models.Profile{}).Where("board_role = ?", models.BoardRoleHeadOfIT).Pluck("email", &emails).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list heads of IT"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"emails": emails})
}

// proxy writes the response itself either way, but also returns the
// upstream status code (0 if it never got that far) so a caller like
// Deactivate can react to success without duplicating this request/error
// handling.
func (h *OffboardingHandler) proxy(c *gin.Context, path, email string) int {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return 0
	}

	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return 0
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodPost, path, payload)
	if err != nil {
		log.Printf("offboarding %s: %v", path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return 0
	}
	c.Data(status, "application/json; charset=utf-8", body)
	return status
}
