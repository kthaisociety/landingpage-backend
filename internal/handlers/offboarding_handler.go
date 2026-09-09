package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

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
	headOfIT.POST("/transfer", h.TransferHeadOfIT)
}

// requesterIsHeadOfIT checks Profile.IsHeadOfIT — deliberately not
// requesterIsHeadOfTeam/AdminTeam, which any admin can set on themselves via
// UpdateInterviewSettings. See the IsHeadOfIT field doc comment for why this
// needs its own, non-self-editable flag.
func requesterIsHeadOfIT(db *gorm.DB, userID uuid.UUID) (bool, error) {
	var profile models.Profile
	if err := db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		return false, err
	}
	return profile.IsHeadOfIT, nil
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
	h.proxy(c, "/internal/offboarding/deactivate", req.Email)
}

type offboardingDeleteRequest struct {
	Email   string `json:"email" binding:"required"`
	Confirm string `json:"confirm"`
}

// Delete permanently deletes the Google Workspace account and attempts to
// permanently delete the Mattermost account for email. Cannot be undone
// from this system — requires typing deleteAccountConfirmPhrase exactly.
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

	log.Printf("offboarding: %s is PERMANENTLY DELETING the account for %s", adminEmail, req.Email)
	h.proxy(c, "/internal/offboarding/delete", req.Email)
}

type transferHeadOfITRequest struct {
	Email string `json:"email" binding:"required"`
}

// errHeadOfITAlreadyTransferred signals the compare-and-swap guard in
// TransferHeadOfIT's transaction found the requester was no longer the
// current head by the time the update ran (lost a race with another
// transfer) — a 409, not a 500, since nothing actually went wrong.
var errHeadOfITAlreadyTransferred = errors.New("head of IT was already transferred by a concurrent request")

// errHeadOfITSuccessorVanished signals the successor's profile disappeared
// between TransferHeadOfIT's pre-transaction validation and the grant
// update inside the transaction — rolling back leaves the original
// requester still holding the flag instead of committing a handover to
// nobody.
var errHeadOfITSuccessorVanished = errors.New("the intended successor's profile no longer exists")

// TransferHeadOfIT hands the Head-of-IT flag to a different admin. There is
// deliberately no separate "revoke" action — the only way to stop being
// Head of IT is to name a successor here, in the same transaction that
// grants it to them, so the system can never end up with zero heads. The
// very first Head of IT has to be set with a one-off manual database
// update; every handover after that goes through this endpoint.
func (h *OffboardingHandler) TransferHeadOfIT(c *gin.Context) {
	requesterID, requesterEmail, ok := h.requireHeadOfIT(c)
	if !ok {
		return
	}

	var req transferHeadOfITRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))
	if targetEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	if targetEmail == strings.ToLower(requesterEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "you're already the head of IT"})
		return
	}

	var target models.Profile
	if err := h.db.Where("email = ?", targetEmail).First(&target).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no profile found for that email"})
		return
	}

	var targetUser models.User
	if err := h.db.Where("id = ?", target.UserId).First(&targetUser).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no user found for that email"})
		return
	}
	if !slices.Contains([]string(targetUser.Roles), models.RoleAdmin) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the new head of IT must already be an admin"})
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		// The WHERE also re-checks is_head_of_it=true, not just user_uuid —
		// requireHeadOfIT's read above happened outside this transaction, so
		// two overlapping transfer requests from the same current head could
		// otherwise both pass that check and both go on to grant a
		// successor, leaving two heads. Postgres row-locks this UPDATE, so
		// the loser of two concurrent transfers blocks until the winner
		// commits, then affects zero rows here and rolls back instead of
		// silently creating a second head.
		result := tx.Model(&models.Profile{}).
			Where("user_uuid = ? AND is_head_of_it = ?", requesterID, true).
			Update("is_head_of_it", false)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errHeadOfITAlreadyTransferred
		}
		// Same reasoning as the check above, mirrored for the successor: the
		// target was validated to exist before this transaction started, so
		// if their profile is deleted in the gap between that check and
		// here, GORM reports no error for a zero-row UPDATE — without this
		// check the transaction would still commit, leaving the requester
		// revoked and no one holding the flag at all.
		grant := tx.Model(&models.Profile{}).Where("user_uuid = ?", target.UserUUID).
			Update("is_head_of_it", true)
		if grant.Error != nil {
			return grant.Error
		}
		if grant.RowsAffected == 0 {
			return errHeadOfITSuccessorVanished
		}
		return nil
	})
	if errors.Is(err, errHeadOfITAlreadyTransferred) {
		c.JSON(http.StatusConflict, gin.H{"error": "you're no longer the head of IT — someone else already transferred it"})
		return
	}
	if errors.Is(err, errHeadOfITSuccessorVanished) {
		c.JSON(http.StatusConflict, gin.H{"error": "that admin's profile no longer exists — you're still the head of IT"})
		return
	}
	if err != nil {
		log.Printf("offboarding: transferring head of IT from %s to %s: %v", requesterEmail, targetEmail, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer head of IT"})
		return
	}

	log.Printf("offboarding: %s transferred head of IT to %s", requesterEmail, targetEmail)
	c.JSON(http.StatusOK, gin.H{"new_head_of_it_email": targetEmail})
}

func (h *OffboardingHandler) proxy(c *gin.Context, path, email string) {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodPost, path, payload)
	if err != nil {
		log.Printf("offboarding %s: %v", path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}
