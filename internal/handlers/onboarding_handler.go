package handlers

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/luma"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OnboardingHandler exposes the small set of endpoints onboarding-service
// calls into this backend for — sending emails (this backend already owns
// SES + templates), adding a newly provisioned member to Luma's "Members"
// tier (this backend already owns the Luma API key — see internal/luma),
// and, once that flow's data model is settled, activating a new member's
// Profile. Every route here is server-to-server only, gated by a shared
// secret, never behind AuthRequiredJWT (the caller has no user session).
// See onboarding-service-plan.md at the repo root for the full design;
// this backend never calls out to Google Workspace or
// Mattermost itself — that's exclusively onboarding-service's job.
type OnboardingHandler struct {
	db   *gorm.DB
	cfg  *config.Config
	luma *luma.LumaAPI
}

func NewOnboardingHandler(db *gorm.DB, cfg *config.Config, lumaApi *luma.LumaAPI) *OnboardingHandler {
	return &OnboardingHandler{db: db, cfg: cfg, luma: lumaApi}
}

func (h *OnboardingHandler) Register(r *gin.RouterGroup) {
	onboarding := r.Group("/internal/onboarding")
	onboarding.Use(h.requireOnboardingServiceSecret)
	{
		onboarding.POST("/send-email", h.SendEmail)
		onboarding.POST("/record-account", h.RecordAccount)
		onboarding.POST("/add-to-luma", h.AddToLuma)
		onboarding.POST("/remove-from-luma", h.RemoveFromLuma)
		onboarding.GET("/contract-template", h.GetContractTemplate)
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

type addToLumaRequest struct {
	Email string `json:"email" binding:"required"`
}

// AddToLuma adds email to Luma's "Members" tier (cfg.Luma.MembersTierID).
// Called both by onboarding-service's provisioning step (blocking: a
// failure here fails the whole onboarding record, retryable the same way a
// Google Workspace failure is) and, indirectly, by this backend's own
// /admin/luma/add-member (LumaHandler) for retroactively adding a member
// who onboarded before this integration existed.
func (h *OnboardingHandler) AddToLuma(c *gin.Context) {
	var req addToLumaRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isKthaisEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid @kthais.com email is required"})
		return
	}

	if err := h.luma.AddMemberToTier(c.Request.Context(), req.Email, h.cfg.Luma.MembersTierID); err != nil {
		log.Printf("onboarding add-to-luma: failed to add %s: %v", req.Email, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to add member to luma"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "added"})
}

type removeFromLumaRequest struct {
	Email string `json:"email" binding:"required"`
}

// RemoveFromLuma removes email from Luma's "Members" tier (declines their
// membership — see luma.LumaAPI.RemoveMember). Called by onboarding-
// service's offboarding.Service.Deactivate/Delete, attempted alongside
// the Google/Mattermost steps there regardless of whether those succeed.
func (h *OnboardingHandler) RemoveFromLuma(c *gin.Context) {
	var req removeFromLumaRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isKthaisEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid @kthais.com email is required"})
		return
	}

	if err := h.luma.RemoveMember(c.Request.Context(), req.Email); err != nil {
		log.Printf("onboarding remove-from-luma: failed to remove %s: %v", req.Email, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to remove member from luma"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

// GetContractTemplate serves the currently uploaded membership-contract
// file to onboarding-service, which relays it to a member clicking their
// emailed contract link (see that service's PortalHandler.DownloadContract
// and backendclient.GetContractTemplate) — this backend is the one place
// the file itself lives, same reasoning as SendEmail above reusing this
// backend's SES setup rather than onboarding-service holding its own
// credentials. 404s if no admin has uploaded one yet.
func (h *OnboardingHandler) GetContractTemplate(c *gin.Context) {
	template, err := models.LoadOnboardingContractTemplate(h.db)
	if err != nil {
		log.Printf("onboarding contract-template: database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if template.ID == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no contract template has been uploaded yet"})
		return
	}

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		log.Printf("onboarding contract-template: failed to initialize storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize storage"})
		return
	}
	data, err := template.GetData(r2)
	if err != nil {
		log.Printf("onboarding contract-template: failed to fetch data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch contract template"})
		return
	}

	contentType := template.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, template.FileName))
	c.Data(http.StatusOK, contentType, data)
}

// onboardingNotifyRequest is the body sent to onboarding-service's own
// POST /notify endpoint (not a route this backend hosts) — see
// onboarding-service-plan.md. applicationID is empty (omitted from the
// JSON entirely via omitempty, decoding to nil on onboarding-service's
// side) for an admin's manual onboarding action — there's no backend
// GeneralApplication to reference in that case.
type onboardingNotifyRequest struct {
	ApplicationID string `json:"application_id,omitempty"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	PersonalEmail string `json:"personal_email"`
	AssignedTeam  string `json:"assigned_team"`
}

// onboardingNotifyMaxAttempts/onboardingNotifyRetryDelay smooth over a
// transient failure (a redeploy, a network blip) in notifyOnboardingService
// — now that AdminFinalizeDecision no longer sends its own acceptance email,
// this call is a freshly-accepted applicant's *only* welcome notice, so one
// flaky attempt must not be the difference between "welcomed" and "silently
// never heard from again."
const onboardingNotifyMaxAttempts = 3

var onboardingNotifyRetryDelay = 2 * time.Second

// notifyOnboardingService hands off a person to onboarding-service so it
// can run account provisioning — either a newly accepted applicant
// (applicationID set, called from AdminFinalizeDecision) or an admin's
// manual onboarding action (applicationID "", called from
// ManualOnboardingHandler). Best-effort and non-fatal by design: for the
// applicant path this runs after the finalize decision is already durably
// saved, so a delivery failure here must never roll back or block the
// admin's accept action; for the manual path there's nothing to roll back
// in the first place. Skipped entirely (not an error) when
// OnboardingServiceURL isn't configured — expected in any environment where
// onboarding-service isn't deployed yet. Retries up to
// onboardingNotifyMaxAttempts times before giving up; returns whether it
// ultimately succeeded so callers with a GeneralApplication to update (see
// AdminFinalizeDecision) can record that durably instead of only logging it
// — a failure here otherwise vanishes into server logs nobody's watching,
// leaving an accepted applicant permanently un-notified with no visible sign
// anything went wrong.
func notifyOnboardingService(cfg *config.Config, applicationID, firstName, lastName, personalEmail, assignedTeam string) (succeeded bool) {
	if cfg.OnboardingServiceURL == "" {
		return false
	}

	for attempt := 1; attempt <= onboardingNotifyMaxAttempts; attempt++ {
		if notifyOnboardingServiceOnce(cfg, applicationID, firstName, lastName, personalEmail, assignedTeam) {
			return true
		}
		if attempt < onboardingNotifyMaxAttempts {
			time.Sleep(onboardingNotifyRetryDelay)
		}
	}
	log.Printf("ATTENTION: onboarding notify permanently failed after %d attempts for %s %s (application %s) — they have not received any welcome notice; retry manually", onboardingNotifyMaxAttempts, firstName, lastName, logID(applicationID))
	return false
}

// logID renders an empty applicationID (the manual-onboarding case) as
// something more legible than "" in a log line.
func logID(applicationID string) string {
	if applicationID == "" {
		return "manual, no application"
	}
	return applicationID
}

func notifyOnboardingServiceOnce(cfg *config.Config, applicationID, firstName, lastName, personalEmail, assignedTeam string) bool {
	body, err := json.Marshal(onboardingNotifyRequest{
		ApplicationID: applicationID,
		FirstName:     firstName,
		LastName:      lastName,
		PersonalEmail: personalEmail,
		AssignedTeam:  assignedTeam,
	})
	if err != nil {
		log.Printf("onboarding notify: failed to marshal request for %s %s: %v", firstName, lastName, err)
		return false
	}

	req, err := http.NewRequest(http.MethodPost, cfg.OnboardingServiceURL+"/notify", bytes.NewReader(body))
	if err != nil {
		log.Printf("onboarding notify: failed to build request for %s %s: %v", firstName, lastName, err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Secret", cfg.OnboardingServiceSecret)

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("onboarding notify: request failed for %s %s: %v", firstName, lastName, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("onboarding notify: onboarding-service returned %d for %s %s", resp.StatusCode, firstName, lastName)
		return false
	}
	return true
}
