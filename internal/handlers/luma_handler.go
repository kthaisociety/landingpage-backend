package handlers

import (
	"log"
	"net/http"

	"backend/internal/config"
	"backend/internal/luma"
	"backend/internal/middleware"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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
	db   *gorm.DB
	cfg  *config.Config
	luma *luma.LumaAPI
}

func NewLumaHandler(db *gorm.DB, cfg *config.Config, lumaApi *luma.LumaAPI) *LumaHandler {
	return &LumaHandler{db: db, cfg: cfg, luma: lumaApi}
}

func (h *LumaHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/luma")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("/add-member", h.AddMember)
	admin.POST("/sync-all", h.SyncAll)
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

type lumaSyncFailure struct {
	Email string `json:"email"`
	Error string `json:"error"`
}

// SyncAll adds every registered @kthais.com member to the Luma Members
// tier in one pass — for backfilling members who joined before this
// integration existed. Runs synchronously and sequentially (no job queue
// in this codebase); fine at this org's member counts, and each call is
// already bounded by luma.LumaAPI's own request timeout. A per-member
// failure doesn't stop the rest — the response summarizes both counts so
// an admin can see partial progress rather than an opaque one-shot error.
func (h *LumaHandler) SyncAll(c *gin.Context) {
	var emails []string
	if err := h.db.Model(&models.User{}).Pluck("email", &emails).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members"})
		return
	}

	added := 0
	failures := []lumaSyncFailure{}
	for _, email := range emails {
		if !isKthaisEmail(email) {
			continue
		}
		if err := h.luma.AddMemberToTier(c.Request.Context(), email, h.cfg.Luma.MembersTierID); err != nil {
			log.Printf("luma sync-all: failed to add %s: %v", email, err)
			failures = append(failures, lumaSyncFailure{Email: email, Error: err.Error()})
			continue
		}
		added++
	}

	c.JSON(http.StatusOK, gin.H{"added": added, "failed": len(failures), "errors": failures})
}
