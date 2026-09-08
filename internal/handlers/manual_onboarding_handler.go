package handlers

import (
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
// listing onboarding records for admin visibility (proxied straight through
// to onboarding-service, never persisted here — see ListRecords).
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

// ListRecords proxies onboarding-service's own /internal/onboarding/records
// straight through — this backend never persists a copy of onboarding
// state (see the package doc comment above), onboarding-service stays the
// source of truth. Returns an empty list rather than an error when
// OnboardingServiceURL isn't configured, matching how notifyOnboardingService
// treats that same case as a silent no-op rather than a failure.
func (h *ManualOnboardingHandler) ListRecords(c *gin.Context) {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusOK, []any{})
		return
	}

	req, err := http.NewRequest(http.MethodGet, h.cfg.OnboardingServiceURL+"/internal/onboarding/records", nil)
	if err != nil {
		log.Printf("onboarding records: failed to build request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reach onboarding service"})
		return
	}
	req.Header.Set("X-Service-Secret", h.cfg.OnboardingServiceSecret)

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("onboarding records: request failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("onboarding records: failed to read response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read onboarding service response"})
		return
	}

	c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
}
