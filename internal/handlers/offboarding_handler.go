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
	"gorm.io/gorm/clause"
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
	headOfIT.POST("/grant", h.GrantHeadOfIT)
	headOfIT.POST("/revoke", h.RevokeHeadOfIT)
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

// ListHeadsOfIT returns the emails of every current Head of IT. Open to any
// admin (not head-of-IT-gated) — who holds this power isn't sensitive, and
// the admin panel needs it to decide whether to offer "Make Head of IT" or
// "Remove Head of IT" for each admin row.
func (h *OffboardingHandler) ListHeadsOfIT(c *gin.Context) {
	var emails []string
	if err := h.db.Model(&models.Profile{}).Where("is_head_of_it = ?", true).Pluck("email", &emails).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list heads of IT"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"emails": emails})
}

type headOfITTargetRequest struct {
	Email string `json:"email" binding:"required"`
}

// resolveAdminTarget validates that email belongs to an existing admin,
// shared by Grant and Revoke since both act on "some other admin, by
// email."
func (h *OffboardingHandler) resolveAdminTarget(c *gin.Context, rawEmail string) (target models.Profile, ok bool) {
	targetEmail := strings.ToLower(strings.TrimSpace(rawEmail))
	if targetEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return models.Profile{}, false
	}
	if err := h.db.Where("email = ?", targetEmail).First(&target).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no profile found for that email"})
		return models.Profile{}, false
	}
	var targetUser models.User
	if err := h.db.Where("id = ?", target.UserId).First(&targetUser).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no user found for that email"})
		return models.Profile{}, false
	}
	if !slices.Contains([]string(targetUser.Roles), models.RoleAdmin) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must already be an admin"})
		return models.Profile{}, false
	}
	return target, true
}

// GrantHeadOfIT adds another admin as a Head of IT alongside every existing
// one — there can be any number of heads at once, so granting never
// threatens the "at least one" invariant and needs no transaction: it's a
// pure addition. The very first Head of IT still has to be set with a
// one-off manual database update, since granting itself requires already
// being one.
func (h *OffboardingHandler) GrantHeadOfIT(c *gin.Context) {
	_, requesterEmail, ok := h.requireHeadOfIT(c)
	if !ok {
		return
	}

	var req headOfITTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	target, ok := h.resolveAdminTarget(c, req.Email)
	if !ok {
		return
	}

	if err := h.db.Model(&models.Profile{}).Where("user_uuid = ?", target.UserUUID).
		Update("is_head_of_it", true).Error; err != nil {
		log.Printf("offboarding: granting head of IT to %s: %v", target.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to grant head of IT"})
		return
	}

	log.Printf("offboarding: %s made %s a head of IT", requesterEmail, target.Email)
	c.JSON(http.StatusOK, gin.H{"email": target.Email})
}

// errCannotRevokeLastHeadOfIT signals RevokeHeadOfIT's lock-and-count check
// found the target is the only remaining Head of IT — a 409, not a 500,
// since nothing went wrong; the action is just refused to preserve the
// invariant that there's always at least one.
var errCannotRevokeLastHeadOfIT = errors.New("cannot revoke the only remaining head of IT")

// RevokeHeadOfIT removes email as a Head of IT — themselves or another
// current head, doesn't matter which — as long as at least one other admin
// still holds it afterward. Locks every current head-of-IT row for the
// duration of the check-and-update so two concurrent revokes (e.g. the last
// two heads each trying to step down at once) can't both succeed and leave
// zero: Postgres serializes them on that lock, and the loser re-evaluates
// the count after the winner's commit is visible.
func (h *OffboardingHandler) RevokeHeadOfIT(c *gin.Context) {
	_, requesterEmail, ok := h.requireHeadOfIT(c)
	if !ok {
		return
	}

	var req headOfITTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))
	if targetEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		var heads []models.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_head_of_it = ?", true).Find(&heads).Error; err != nil {
			return err
		}
		targetIsHead := false
		for _, p := range heads {
			if strings.EqualFold(p.Email, targetEmail) {
				targetIsHead = true
				break
			}
		}
		if !targetIsHead {
			return errHeadOfITTargetNotAHead
		}
		if len(heads) <= 1 {
			return errCannotRevokeLastHeadOfIT
		}
		return tx.Model(&models.Profile{}).Where("email = ?", targetEmail).
			Update("is_head_of_it", false).Error
	})
	if errors.Is(err, errCannotRevokeLastHeadOfIT) {
		c.JSON(http.StatusConflict, gin.H{"error": "can't remove the only remaining head of IT — make someone else one first"})
		return
	}
	if errors.Is(err, errHeadOfITTargetNotAHead) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "that admin isn't a head of IT"})
		return
	}
	if err != nil {
		log.Printf("offboarding: revoking head of IT from %s: %v", targetEmail, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke head of IT"})
		return
	}

	log.Printf("offboarding: %s removed %s as a head of IT", requesterEmail, targetEmail)
	c.JSON(http.StatusOK, gin.H{"email": targetEmail})
}

// errHeadOfITTargetNotAHead signals Revoke was asked to remove someone who
// doesn't currently hold the flag at all — a 400, not a no-op, so the
// caller's mental model of who's a head stays accurate.
var errHeadOfITTargetNotAHead = errors.New("target is not a head of IT")

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
