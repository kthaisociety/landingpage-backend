package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"backend/internal/config"
	"backend/internal/middleware"

	"github.com/gin-gonic/gin"
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
}

// requireHeadOfIT is deliberately stricter than the rest of this admin
// group: RoleRequired("admin") above only proves the caller is *an* admin.
// Every handler in this file re-checks head-of-IT specifically, the same
// way AdminCloseFinalizePhase and AdminSendBulk do.
func (h *OffboardingHandler) requireHeadOfIT(c *gin.Context) (adminEmail string, ok bool) {
	userID, adminEmail, authOK := getAdminIdentity(c)
	if !authOK {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return "", false
	}
	isHeadOfIT, err := requesterIsHeadOfTeam(h.db, userID, "IT")
	if err != nil || !isHeadOfIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the head of IT can offboard a member's account"})
		return "", false
	}
	return adminEmail, true
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
	adminEmail, ok := h.requireHeadOfIT(c)
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
	adminEmail, ok := h.requireHeadOfIT(c)
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
