package handlers

import (
	"log"
	"net/http"

	"backend/internal/config"
	"backend/internal/luma"
	"backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// LumaHandler lets an admin retroactively add a member to Luma's "Members"
// tier — for someone who onboarded before this integration existed, or
// whose automatic add during onboarding-service provisioning failed. Calls
// this backend's own Luma client directly (see internal/luma), rather than
// going through onboarding-service, which has no Luma credential of its
// own. Open to any admin, not head-of-IT-gated like OffboardingHandler's
// deactivate/delete: unlike those, this is additive and reversible from
// Luma's own dashboard, not a destructive action on a real account.
// Applies to any @kthais.com address, independent of onboarding-service's
// own OnboardingRecord table (same as OffboardingHandler).
type LumaHandler struct {
	cfg  *config.Config
	luma *luma.LumaAPI
}

func NewLumaHandler(cfg *config.Config, lumaApi *luma.LumaAPI) *LumaHandler {
	return &LumaHandler{cfg: cfg, luma: lumaApi}
}

func (h *LumaHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/luma")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("/add-member", h.AddMember)
}

type lumaAddMemberRequest struct {
	Email string `json:"email" binding:"required"`
}

// AddMember adds email to the Luma Members tier (cfg.Luma.MembersTierID).
func (h *LumaHandler) AddMember(c *gin.Context) {
	var req lumaAddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isKthaisEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid @kthais.com email is required"})
		return
	}

	if err := h.luma.AddMemberToTier(c.Request.Context(), req.Email, h.cfg.Luma.MembersTierID); err != nil {
		log.Printf("luma add-member: failed to add %s: %v", req.Email, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to add member to luma"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "added"})
}
