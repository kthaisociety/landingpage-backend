package handlers

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BoardRoleHandler owns the nine board-role labels shown on the Members
// dashboard, plus Team. Team is freely admin-settable, like the old
// AdminTeam field. BoardRole is split in two: eight of the nine values are
// held by exactly one person at a time and only ever move via a transfer
// initiated by the current holder (TransferBoardRole) — no admin grant/
// revoke/set-for-anyone, deliberately, since BoardRoleHeadOfIT is one of
// the eight and gates real permissions (requesterIsHeadOfIT,
// requesterIsHeadOfTeam, deleteUserAndProfile). BoardRoleBoardAdvisor is
// the one multi-holder exception, managed by plain admin add/remove. See
// Profile.BoardRole's doc comment for the full reasoning.
type BoardRoleHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewBoardRoleHandler(db *gorm.DB, cfg *config.Config) *BoardRoleHandler {
	return &BoardRoleHandler{db: db, cfg: cfg}
}

func (h *BoardRoleHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))

	admin.GET("/board-role", h.ListBoardRoleHolders)
	admin.POST("/board-role/transfer", h.TransferBoardRole)
	admin.POST("/board-role/board-advisor/add", h.AddBoardAdvisor)
	admin.POST("/board-role/board-advisor/remove", h.RemoveBoardAdvisor)
	admin.POST("/team/set", h.SetTeam)
	admin.POST("/team/backfill", h.BackfillMemberTeams)
}

// exactlyOneBoardRoles is models.AllBoardRoles minus BoardRoleBoardAdvisor
// — the eight roles TransferBoardRole accepts.
var exactlyOneBoardRoles = func() []string {
	roles := make([]string, 0, len(models.AllBoardRoles)-1)
	for _, role := range models.AllBoardRoles {
		if role != models.BoardRoleBoardAdvisor {
			roles = append(roles, role)
		}
	}
	return roles
}()

// ListBoardRoleHolders returns every board role's current holder(s). Open
// to any admin — who holds these isn't sensitive (mirrors ListHeadsOfIT,
// which this supersedes for new frontend code). The eight exactly-one
// roles are each an email or null; board_advisor is always an array, even
// when empty, since it's the one role with no single holder.
func (h *BoardRoleHandler) ListBoardRoleHolders(c *gin.Context) {
	var profiles []models.Profile
	if err := h.db.Where("board_role <> ''").Find(&profiles).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list board roles"})
		return
	}

	result := gin.H{}
	for _, role := range exactlyOneBoardRoles {
		result[role] = nil
	}
	advisors := []string{}
	for _, p := range profiles {
		if p.BoardRole == models.BoardRoleBoardAdvisor {
			advisors = append(advisors, p.Email)
			continue
		}
		result[p.BoardRole] = p.Email
	}
	result[models.BoardRoleBoardAdvisor] = advisors

	c.JSON(http.StatusOK, result)
}

type transferBoardRoleRequest struct {
	Role    string `json:"role" binding:"required"`
	ToEmail string `json:"to_email" binding:"required"`
}

var (
	// errRequesterDoesNotHoldRole signals the authenticated admin tried to
	// transfer a role they don't currently hold — the whole point of
	// self-service transfer is that only the current holder can initiate
	// one.
	errRequesterDoesNotHoldRole = errors.New("requester does not currently hold this role")
	// errBoardRoleRecipientAlreadyHolds signals the transfer/add target
	// already holds a different board role — accepting it would silently
	// vacate that other role with no successor.
	errBoardRoleRecipientAlreadyHolds = errors.New("recipient already holds a board role")
	// errRecipientNotAdmin mirrors the old GrantHeadOfIT's
	// resolveAdminTarget check, generalized to every exactly-one role: every
	// /admin/board-role/* route requires the *caller* to already be an
	// admin, so transferring one of these roles to a non-admin would create
	// an administrative dead end — that recipient could never call the
	// transfer endpoint themselves to hand the role onward.
	errRecipientNotAdmin = errors.New("recipient must already be an admin")
)

// TransferBoardRole is the only write path for the eight exactly-one board
// roles. The requester must currently hold role themselves — checked
// against their own authenticated identity, never a caller-supplied
// "from" email, so there's no impersonation surface. Locks both the
// requester's and recipient's Profile rows for the duration of the
// check-and-update so a concurrent transfer of the same role, or a
// concurrent transfer *to* the same recipient, can't race past the
// validation.
func (h *BoardRoleHandler) TransferBoardRole(c *gin.Context) {
	requesterID, requesterEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req transferBoardRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role and to_email are required"})
		return
	}
	if req.Role == models.BoardRoleBoardAdvisor {
		c.JSON(http.StatusBadRequest, gin.H{"error": "board advisor has no single holder to transfer — use /admin/board-role/board-advisor/add or /remove instead"})
		return
	}
	if !slices.Contains(exactlyOneBoardRoles, req.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown board role"})
		return
	}
	toEmail := strings.ToLower(strings.TrimSpace(req.ToEmail))
	if toEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_email is required"})
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		var requester models.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_uuid = ?", requesterID).First(&requester).Error; err != nil {
			return err
		}
		if requester.BoardRole != req.Role {
			return errRequesterDoesNotHoldRole
		}

		var recipient models.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("email = ?", toEmail).First(&recipient).Error; err != nil {
			return err
		}
		if recipient.BoardRole != "" {
			return errBoardRoleRecipientAlreadyHolds
		}
		var recipientUser models.User
		if err := tx.Where("id = ?", recipient.UserId).First(&recipientUser).Error; err != nil {
			return err
		}
		if !slices.Contains([]string(recipientUser.Roles), models.RoleAdmin) {
			return errRecipientNotAdmin
		}

		if err := tx.Model(&requester).Update("board_role", "").Error; err != nil {
			return err
		}
		return tx.Model(&recipient).Update("board_role", req.Role).Error
	})

	switch {
	case errors.Is(err, errRequesterDoesNotHoldRole):
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not currently hold this role"})
		return
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "recipient must have a profile first"})
		return
	case errors.Is(err, errBoardRoleRecipientAlreadyHolds):
		c.JSON(http.StatusBadRequest, gin.H{"error": "recipient already holds a board role — they need to transfer or be removed from it first"})
		return
	case errors.Is(err, errRecipientNotAdmin):
		c.JSON(http.StatusBadRequest, gin.H{"error": "recipient must already be an admin"})
		return
	case err != nil:
		log.Printf("board-role transfer: %s -> %s (%s): %v", requesterEmail, toEmail, req.Role, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer role"})
		return
	}

	log.Printf("board-role: %s transferred %s to %s", requesterEmail, req.Role, toEmail)
	c.JSON(http.StatusOK, gin.H{"role": req.Role, "email": toEmail})
}

type boardAdvisorRequest struct {
	Email string `json:"email" binding:"required"`
}

// AddBoardAdvisor adds email as a Board Advisor — the one board role with
// no single holder to protect, so unlike the other eight, any admin can
// add/remove freely (same reasoning as Team being freely admin-settable).
func (h *BoardRoleHandler) AddBoardAdvisor(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req boardAdvisorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))

	err := h.db.Transaction(func(tx *gorm.DB) error {
		var target models.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("email = ?", targetEmail).First(&target).Error; err != nil {
			return err
		}
		if target.BoardRole != "" && target.BoardRole != models.BoardRoleBoardAdvisor {
			return errBoardRoleRecipientAlreadyHolds
		}
		return tx.Model(&target).Update("board_role", models.BoardRoleBoardAdvisor).Error
	})

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no profile found for that email"})
		return
	case errors.Is(err, errBoardRoleRecipientAlreadyHolds):
		c.JSON(http.StatusBadRequest, gin.H{"error": "that member already holds a different board role"})
		return
	case err != nil:
		log.Printf("board-role: failed to add board advisor %s: %v", targetEmail, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add board advisor"})
		return
	}

	log.Printf("board-role: %s added %s as a board advisor", adminEmail, targetEmail)
	c.JSON(http.StatusOK, gin.H{"email": targetEmail})
}

// RemoveBoardAdvisor removes email as a Board Advisor. A no-op (still 200)
// if they weren't one — unlike the exactly-one roles, there's no
// "must currently hold it" invariant to protect on the way out; any
// number of advisors, including zero, is a valid state.
func (h *BoardRoleHandler) RemoveBoardAdvisor(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req boardAdvisorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))

	if err := h.db.Model(&models.Profile{}).
		Where("email = ? AND board_role = ?", targetEmail, models.BoardRoleBoardAdvisor).
		Update("board_role", "").Error; err != nil {
		log.Printf("board-role: failed to remove board advisor %s: %v", targetEmail, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove board advisor"})
		return
	}

	log.Printf("board-role: %s removed %s as a board advisor", adminEmail, targetEmail)
	c.JSON(http.StatusOK, gin.H{"email": targetEmail})
}

type setTeamRequest struct {
	Email string `json:"email" binding:"required"`
	Team  string `json:"team"`
}

// SetTeam sets a member's recruitment-team label. Freely admin-settable —
// unlike board roles, team membership gates nothing and many people share
// a team, so there's no transfer/invariant machinery here.
func (h *BoardRoleHandler) SetTeam(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req setTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	if req.Team != "" {
		if _, ok := allowedApplicationTeams[req.Team]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown team"})
			return
		}
	}
	targetEmail := strings.ToLower(strings.TrimSpace(req.Email))

	result := h.db.Model(&models.Profile{}).Where("email = ?", targetEmail).Update("team", req.Team)
	if result.Error != nil {
		log.Printf("board-role: failed to set team for %s: %v", targetEmail, result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set team"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no profile found for that email"})
		return
	}

	log.Printf("board-role: %s set %s's team to %q", adminEmail, targetEmail, req.Team)
	c.JSON(http.StatusOK, gin.H{"email": targetEmail, "team": req.Team})
}

// BackfillMemberTeams is a one-time-ish bulk fix for members whose Profile
// predates Team existing (or predates auto-populating it at profile-
// creation time — see resolveTeamFromAcceptedApplication): for every
// Profile with an empty Team and no BoardRole, look up their most recent
// accepted GeneralApplication by KthaisEmail and set Team from its
// AssignedTeam. Board-role holders are deliberately excluded, not just
// displayed differently: board roles aren't scoped to any of the five
// recruitment teams (this includes the four Head-of-team roles too — a
// Head of Business's authority comes from BoardRole, not Team, and their
// original recruitment team, if any, is no longer their current one), so
// assigning one a Team here would just be wrong, not merely redundant with
// what the dashboard already shows for them. Same "reuse the one already-
// tested lookup" approach as the auto-populate call sites in
// profile_handler.go and AuthHandler.GoogleCallback — this is deliberately
// not a raw SQL migration, so this is the single place that logic lives.
// Idempotent and safe to call more than once: it only ever touches
// profiles with team = '' and board_role = '', and a member with no
// matching application (never applied, or admin-onboarded outside
// recruitment) is silently left as "" (Unassigned), same as at profile-
// creation time.
func (h *BoardRoleHandler) BackfillMemberTeams(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var profiles []models.Profile
	if err := h.db.Where("team = '' AND board_role = ''").Find(&profiles).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members"})
		return
	}

	updated := 0
	for _, profile := range profiles {
		team := resolveTeamFromAcceptedApplication(h.db, profile.Email)
		if team == "" {
			continue
		}
		// Update via the already-loaded struct (same pattern as
		// AddBoardAdvisor/RemoveBoardAdvisor below) rather than
		// re-specifying a WHERE clause by hand — Profile embeds gorm.Model
		// (a uint ID) but also declares its own uuid.UUID Id as the actual
		// primary key column, so a hand-built "id = ?" using profile.ID
		// would bind the wrong (always-zero) field.
		if err := h.db.Model(&profile).Update("team", team).Error; err != nil {
			log.Printf("board-role: failed to backfill team for %s: %v", profile.Email, err)
			continue
		}
		updated++
	}

	log.Printf("board-role: %s backfilled team for %d/%d member(s) with no team", adminEmail, updated, len(profiles))
	c.JSON(http.StatusOK, gin.H{"checked": len(profiles), "updated": updated})
}
