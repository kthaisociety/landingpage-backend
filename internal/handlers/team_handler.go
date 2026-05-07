package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TeamHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewTeamHandler(db *gorm.DB, cfg *config.Config) *TeamHandler {
	return &TeamHandler{db: db, cfg: cfg}
}

// PublicTeamMember is the shape returned to the frontend
type PublicTeamMember struct {
	ProfileID      string `json:"profileId"`
	FirstName      string `json:"firstName"`
	LastName       string `json:"lastName"`
	ProfilePicture string `json:"profilePicture"`
	GraduationYear int    `json:"graduationYear"`
	Role           string `json:"role"`
	Department     string `json:"department"`
	AcademicYear   string `json:"academicYear"`
	AboutMe        string `json:"aboutMe"`
	GitHubLink     string `json:"githubLink"`
	LinkedInLink   string `json:"linkedinLink"`
}

func (h *TeamHandler) Register(r *gin.RouterGroup) {
	team := r.Group("/team")

	// Public endpoints
	team.GET("/members", h.GetTeamMembers)
	team.GET("/years", h.GetAvailableYears)

	// Auth-required: members manage their own entries
	protected := team.Group("")
	protected.Use(middleware.AuthRequiredJWT(h.cfg))
	protected.GET("/my-entries", h.GetMyTeamEntries)
	protected.POST("/member", h.AddMyTeamEntry)
	protected.DELETE("/member/:id", h.RemoveMyTeamEntry)

	// Admin-only
	admin := protected.Group("/admin")
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.GET("/user-entries", h.AdminGetUserTeamEntries)
	admin.POST("/member", h.AdminAddTeamEntry)
	admin.DELETE("/member/:id", h.AdminRemoveTeamEntry)
	admin.GET("/members/all", h.AdminListAllEntries)
}

// GetTeamMembers returns team members optionally filtered by year and/or department
func (h *TeamHandler) GetTeamMembers(c *gin.Context) {
	year := c.Query("year")
	department := c.Query("department")
	alumniParam := strings.ToLower(strings.TrimSpace(c.Query("alumni")))
	isAlumni := alumniParam == "1" || alumniParam == "true" || alumniParam == "yes"

	query := h.db.Table("team_members").
		Select(`
			profiles.id         AS profile_id,
			profiles.first_name AS first_name,
			profiles.last_name  AS last_name,
			profiles.profile_picture AS profile_picture,
			profiles.graduation_year AS graduation_year,
			profiles.about_me   AS about_me,
			profiles.git_hub_link  AS git_hub_link,
			profiles.linked_in_link AS linked_in_link,
			team_members.team_member_role       AS role,
			team_members.team_member_department AS department,
			team_members.academic_year          AS academic_year
		`).
		Joins("JOIN profiles ON profiles.user_id = team_members.user_id").
		Where("team_members.deleted_at IS NULL")

	if isAlumni {
		calendarYear := time.Now().Year()
		query = query.Where("profiles.graduation_year > 0 AND profiles.graduation_year < ?", calendarYear)
	} else if year != "" {
		query = query.Where("team_members.academic_year = ?", year)
	}
	if department != "" && department != "All" && department != "Alumni" {
		query = query.Where("team_members.team_member_department = ?", department)
	}

	type row struct {
		ProfileID      string `gorm:"column:profile_id"`
		FirstName      string `gorm:"column:first_name"`
		LastName       string `gorm:"column:last_name"`
		ProfilePicture string `gorm:"column:profile_picture"`
		GraduationYear int    `gorm:"column:graduation_year"`
		AboutMe        string `gorm:"column:about_me"`
		GitHubLink     string `gorm:"column:git_hub_link"`
		LinkedInLink   string `gorm:"column:linked_in_link"`
		Role           string `gorm:"column:role"`
		Department     string `gorm:"column:department"`
		AcademicYear   string `gorm:"column:academic_year"`
	}

	var rows []row
	if err := query.Scan(&rows).Error; err != nil {
		log.Printf("GetTeamMembers error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make([]PublicTeamMember, 0, len(rows))
	for _, r := range rows {
		result = append(result, PublicTeamMember{
			ProfileID:      r.ProfileID,
			FirstName:      r.FirstName,
			LastName:       r.LastName,
			ProfilePicture: r.ProfilePicture,
			GraduationYear: r.GraduationYear,
			Role:           r.Role,
			Department:     r.Department,
			AcademicYear:   r.AcademicYear,
			AboutMe:        r.AboutMe,
			GitHubLink:     r.GitHubLink,
			LinkedInLink:   r.LinkedInLink,
		})
	}

	c.JSON(http.StatusOK, result)
}

// GetAvailableYears returns all distinct academic years that have team members
func (h *TeamHandler) GetAvailableYears(c *gin.Context) {
	var years []string
	if err := h.db.Model(&models.TeamMember{}).
		Where("deleted_at IS NULL AND academic_year != ''").
		Distinct("academic_year").
		Order("academic_year DESC").
		Pluck("academic_year", &years).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, years)
}

// GetMyTeamEntries returns the authenticated user's own team entries
func (h *TeamHandler) GetMyTeamEntries(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userUUIDStr, ok := claims["user_id"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUIDStr).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	var entries []models.TeamMember
	h.db.Where("user_id = ? AND deleted_at IS NULL", profile.UserId).
		Order("academic_year DESC").
		Find(&entries)

	c.JSON(http.StatusOK, entries)
}

// AddMyTeamEntry lets a member add their own team membership entry
func (h *TeamHandler) AddMyTeamEntry(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)

	userUUIDStr, ok := claims["user_id"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}

	// Get the uint user ID via the profile
	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUIDStr).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found. Create your profile first."})
		return
	}

	var input struct {
		Role         string `json:"role"`
		Department   string `json:"department" binding:"required"`
		AcademicYear string `json:"academicYear" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entry := models.TeamMember{
		UserID:               profile.UserId,
		TeamMemberRole:       input.Role,
		TeamMemberDepartment: input.Department,
		AcademicYear:         input.AcademicYear,
	}
	if err := h.db.Create(&entry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, entry)
}

// RemoveMyTeamEntry lets a member delete their own entry (by TeamMember ID)
func (h *TeamHandler) RemoveMyTeamEntry(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)

	userUUIDStr, ok := claims["user_id"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUIDStr).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	entryID := c.Param("id")
	var entry models.TeamMember
	if err := h.db.First(&entry, "id = ?", entryID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found"})
		return
	}

	if entry.UserID != profile.UserId {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete another member's entry"})
		return
	}

	h.db.Delete(&entry)
	c.JSON(http.StatusOK, gin.H{"message": "Removed"})
}

// AdminGetUserTeamEntries returns team_members rows for a user (by profiles.user_uuid).
func (h *TeamHandler) AdminGetUserTeamEntries(c *gin.Context) {
	userIDStr := strings.TrimSpace(c.Query("userId"))
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId query parameter is required"})
		return
	}
	userUUID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid userId"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	var entries []models.TeamMember
	if err := h.db.Where("user_id = ? AND deleted_at IS NULL", profile.UserId).
		Order("academic_year DESC").
		Find(&entries).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, entries)
}

// AdminAddTeamEntry lets an admin add any member to a team
func (h *TeamHandler) AdminAddTeamEntry(c *gin.Context) {
	var input struct {
		ProfileID    string `json:"profileId" binding:"required"`
		Role         string `json:"role"`
		Department   string `json:"department" binding:"required"`
		AcademicYear string `json:"academicYear" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var profile models.Profile
	if err := h.db.Where("id = ?", input.ProfileID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	entry := models.TeamMember{
		UserID:               profile.UserId,
		TeamMemberRole:       input.Role,
		TeamMemberDepartment: input.Department,
		AcademicYear:         input.AcademicYear,
	}
	if err := h.db.Create(&entry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, entry)
}

// AdminRemoveTeamEntry lets an admin delete any entry
func (h *TeamHandler) AdminRemoveTeamEntry(c *gin.Context) {
	entryID := c.Param("id")
	var entry models.TeamMember
	if err := h.db.First(&entry, "id = ?", entryID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found"})
		return
	}
	h.db.Delete(&entry)
	c.JSON(http.StatusOK, gin.H{"message": "Removed"})
}

// AdminListAllEntries returns all team member entries with profile info
func (h *TeamHandler) AdminListAllEntries(c *gin.Context) {
	type row struct {
		ID           uint   `gorm:"column:id"`
		ProfileID    string `gorm:"column:profile_id"`
		FirstName    string `gorm:"column:first_name"`
		LastName     string `gorm:"column:last_name"`
		Email        string `gorm:"column:email"`
		Role         string `gorm:"column:role"`
		Department   string `gorm:"column:department"`
		AcademicYear string `gorm:"column:academic_year"`
	}

	var rows []row
	if err := h.db.Table("team_members").
		Select(`
			team_members.id,
			profiles.id         AS profile_id,
			profiles.first_name AS first_name,
			profiles.last_name  AS last_name,
			profiles.email      AS email,
			team_members.team_member_role       AS role,
			team_members.team_member_department AS department,
			team_members.academic_year          AS academic_year
		`).
		Joins("JOIN profiles ON profiles.user_id = team_members.user_id").
		Where("team_members.deleted_at IS NULL").
		Order("team_members.academic_year DESC, profiles.last_name ASC").
		Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}
