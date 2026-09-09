package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// ManualOnboardingHandler owns the admin-facing /admin/onboarding/* routes:
// starting a manual onboarding (outside the recruitment pipeline entirely —
// board appointments, special cases; no GeneralApplication is created or
// required — see notifyOnboardingService's doc comment and
// onboarding-service-plan.md for why an empty applicationID is the honest
// representation of "this person didn't come through the application
// pipeline," rather than fabricating a placeholder id or a new table), and
// proxying admin visibility/recovery actions straight through to
// onboarding-service (ListRecords, CancelOnboarding, RestartOnboarding) —
// this backend never persists a copy of onboarding state, onboarding-service
// stays the source of truth for all of it.
type ManualOnboardingHandler struct {
	cfg *config.Config
}

func NewManualOnboardingHandler(cfg *config.Config) *ManualOnboardingHandler {
	return &ManualOnboardingHandler{cfg: cfg}
}

func (h *ManualOnboardingHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/onboarding")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("/manual", h.Create)
	admin.GET("/records", h.ListRecords)
	admin.POST("/retry", h.RetryOnboarding)
	admin.POST("/cancel", h.CancelOnboarding)
	admin.POST("/restart", h.RestartOnboarding)
}

type manualOnboardingRequest struct {
	FirstName    string `json:"first_name" binding:"required"`
	LastName     string `json:"last_name" binding:"required"`
	Email        string `json:"email" binding:"required"`
	AssignedTeam string `json:"assigned_team" binding:"required"`
}

// Create fires the same onboarding-service notify call the recruitment
// pipeline uses, with an empty applicationID — see
// notifyOnboardingService. Fire-and-forget, matching that same pattern:
// this endpoint always responds success once the request is valid,
// regardless of whether onboarding-service itself is reachable.
func (h *ManualOnboardingHandler) Create(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req manualOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "first_name, last_name, email, and assigned_team are required"})
		return
	}

	// Same normalization/bounds as validateGeneralApplicationInput
	// (general_application_handler.go) — this is the same "a name and an
	// email address" shape, just arriving from an admin form instead of the
	// public application form.
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Email = strings.TrimSpace(req.Email)
	if len(req.FirstName) == 0 || len(req.FirstName) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "first name is required and must be at most 80 characters"})
		return
	}
	if len(req.LastName) == 0 || len(req.LastName) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "last name is required and must be at most 80 characters"})
		return
	}
	if !isValidEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email is required"})
		return
	}
	if _, ok := allowedApplicationTeams[req.AssignedTeam]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid assigned_team"})
		return
	}

	log.Printf("manual onboarding: %s %s <%s> (%s) initiated by %s", req.FirstName, req.LastName, req.Email, req.AssignedTeam, adminEmail)
	go notifyOnboardingService(h.cfg, "", req.FirstName, req.LastName, req.Email, req.AssignedTeam)

	c.JSON(http.StatusOK, gin.H{"status": "notified"})
}

// callOnboardingService builds and executes a secret-authenticated request
// to onboarding-service, returning the raw response status/body for
// pass-through — shared by ListRecords, CancelOnboarding, and
// RestartOnboarding. Callers are responsible for their own
// OnboardingServiceURL-unset handling: a read (ListRecords) can reasonably
// fall back to an empty result, but a mutating action failing loudly is
// correct, not a silent no-op.
func (h *ManualOnboardingHandler) callOnboardingService(method, path string, body []byte) (status int, respBody []byte, err error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.cfg.OnboardingServiceURL+path, reqBody)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Service-Secret", h.cfg.OnboardingServiceSecret)

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// ListRecords proxies onboarding-service's own /internal/onboarding/records
// straight through. Returns an empty list rather than an error when
// OnboardingServiceURL isn't configured, matching how notifyOnboardingService
// treats that same case as a silent no-op rather than a failure.
func (h *ManualOnboardingHandler) ListRecords(c *gin.Context) {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusOK, []any{})
		return
	}

	status, body, err := h.callOnboardingService(http.MethodGet, "/internal/onboarding/records", nil)
	if err != nil {
		log.Printf("onboarding records: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

type onboardingRecordActionRequest struct {
	ID uint `json:"id" binding:"required"`
}

// RetryOnboarding proxies to onboarding-service's own
// /retry-provisioning — see that service's RecordActionsHandler.Retry for
// what it actually does (re-runs provisioning from wherever it left off;
// only valid for a record in kth_email_confirmed or failed).
func (h *ManualOnboardingHandler) RetryOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/retry-provisioning")
}

// CancelOnboarding proxies to onboarding-service's own /cancel — see that
// service's RecordActionsHandler.Cancel for what it actually does. Unlike
// ListRecords, an unconfigured OnboardingServiceURL is a real error here:
// this is a mutating action, so failing loudly is correct, not a silent
// no-op.
func (h *ManualOnboardingHandler) CancelOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/cancel")
}

// RestartOnboarding proxies to onboarding-service's own /restart — see
// that service's RecordActionsHandler.Restart.
func (h *ManualOnboardingHandler) RestartOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/restart")
}

func (h *ManualOnboardingHandler) proxyRecordAction(c *gin.Context, path string) {
	var req onboardingRecordActionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	payload, err := json.Marshal(map[string]uint{"id": req.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	status, body, err := h.callOnboardingService(http.MethodPost, path, payload)
	if err != nil {
		log.Printf("onboarding %s: %v", path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}
