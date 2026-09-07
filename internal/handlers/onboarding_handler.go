package handlers

import (
	"crypto/subtle"
	"log"
	"net/http"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OnboardingHandler exposes the small set of endpoints onboarding-service
// calls into this backend for — sending emails (this backend already owns
// SES + templates) and, once that flow's data model is settled, activating
// a new member's Profile. Every route here is server-to-server only, gated
// by a shared secret, never behind AuthRequiredJWT (the caller has no user
// session). See onboarding-service-plan.md at the repo root for the full
// design; this backend never calls out to Google Workspace or Mattermost
// itself — that's exclusively onboarding-service's job.
type OnboardingHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewOnboardingHandler(db *gorm.DB, cfg *config.Config) *OnboardingHandler {
	return &OnboardingHandler{db: db, cfg: cfg}
}

func (h *OnboardingHandler) Register(r *gin.RouterGroup) {
	onboarding := r.Group("/internal/onboarding")
	onboarding.Use(h.requireOnboardingServiceSecret)
	{
		onboarding.POST("/send-email", h.SendEmail)
		onboarding.POST("/record-account", h.RecordAccount)
	}
}

// requireOnboardingServiceSecret rejects before touching any handler logic
// if the caller doesn't present the exact shared secret — mirrors
// ExchangeMCPToken's check in auth_handler.go. An unset
// OnboardingServiceSecret must never be treated as "no secret required".
func (h *OnboardingHandler) requireOnboardingServiceSecret(c *gin.Context) {
	provided := c.GetHeader("X-Service-Secret")
	if h.cfg.OnboardingServiceSecret == "" || provided == "" ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(h.cfg.OnboardingServiceSecret)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.Next()
}

type sendEmailRequest struct {
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	ButtonURL  string `json:"button_url"`
	ButtonText string `json:"button_text"`
}

// SendEmail lets onboarding-service send a member-facing email through this
// backend's existing SES setup instead of standing up its own — see
// email.SendOnboardingEmail for why the body is treated as plain text, never
// as a template, and why an empty button falls back to a generic contact
// link.
func (h *OnboardingHandler) SendEmail(c *gin.Context) {
	var req sendEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.To == "" || req.Subject == "" || req.Body == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to, subject, and body are required"})
		return
	}

	if err := email.SendOnboardingEmail(req.To, req.Subject, req.Body, req.ButtonURL, req.ButtonText); err != nil {
		log.Printf("onboarding send-email: failed to send to %q: %v", req.To, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send email"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "sent"})
}

type recordAccountRequest struct {
	ApplicationID uuid.UUID `json:"application_id" binding:"required"`
	KthaisEmail   string    `json:"kthais_email" binding:"required"`
}

// RecordAccount is pure bookkeeping, called once onboarding-service finishes
// provisioning: it writes the new @kthais.com address back onto the
// GeneralApplication row so admins can see, from the application itself,
// that onboarding completed and what the resulting account is. It does not
// create a User or Profile — the member gets those the normal way, on their
// first Google login with this address (see AuthHandler.GoogleCallback).
// Only accepted applications can be recorded; anything else is a caller bug
// on onboarding-service's side (it should only ever have been notified about
// accepted applications in the first place).
func (h *OnboardingHandler) RecordAccount(c *gin.Context) {
	var req recordAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "application_id and kthais_email are required"})
		return
	}

	var application models.GeneralApplication
	result := h.db.Where("id = ?", req.ApplicationID).First(&application)
	if result.Error == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	} else if result.Error != nil {
		log.Printf("onboarding record-account: database error looking up %s: %v", req.ApplicationID, result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if application.Status != models.GeneralApplicationStatusAccepted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "application is not accepted"})
		return
	}

	now := time.Now()
	application.KthaisEmail = req.KthaisEmail
	application.OnboardedAt = &now
	if err := h.db.Save(&application).Error; err != nil {
		log.Printf("onboarding record-account: failed to save %s: %v", req.ApplicationID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save"})
		return
	}

	c.JSON(http.StatusOK, application)
}
