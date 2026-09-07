package handlers

import (
	"log"
	"net/http"
	"strings"

	"backend/internal/config"
	"backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// ManualOnboardingHandler lets an admin onboard a new @kthais.com member
// outside the recruitment pipeline entirely (board appointments, special
// cases) — no GeneralApplication is created or required. There is
// deliberately no persistence here: see notifyOnboardingService's doc
// comment and onboarding-service-plan.md for why an empty applicationID
// (no backend record to reference) is the honest representation of "this
// person didn't come through the application pipeline," rather than
// fabricating a placeholder id or a new table just to have something to
// look up later.
type ManualOnboardingHandler struct {
	cfg *config.Config
}

func NewManualOnboardingHandler(cfg *config.Config) *ManualOnboardingHandler {
	return &ManualOnboardingHandler{cfg: cfg}
}

func (h *ManualOnboardingHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/onboarding/manual")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("", h.Create)
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
