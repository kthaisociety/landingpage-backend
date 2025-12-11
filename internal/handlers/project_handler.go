package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ProjectHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewProjectHandler(db *gorm.DB, cfg *config.Config) *ProjectHandler {
	return &ProjectHandler{db: db, cfg: cfg}
}

func (h *ProjectHandler) Register(r *gin.RouterGroup) {
	projects := r.Group("/projects")
	{
		// Public endpoints (anyone can view)
		projects.GET("", h.List)
		projects.GET("/:id", h.Get)

		// Authenticated endpoints
		authenticated := projects.Group("")
		authenticated.Use(middleware.AuthRequiredJWT(h.cfg))
		authenticated.Use(middleware.RegisteredUserRequired(h.db))
		authenticated.POST("", h.Create)
		authenticated.PUT("/:id", h.Update)
		authenticated.DELETE("/:id", h.Delete)
		authenticated.POST("/:id/members", h.AddMember)
		authenticated.DELETE("/:id/members/:userId", h.RemoveMember)
	}
}

// ProjectResponse includes all project details with members
type ProjectResponse struct {
	ID          uuid.UUID               `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Skills      []string                `json:"skills"`
	Status      models.ProjectStatus    `json:"status"`
	Members     []ProjectMemberResponse `json:"members"`
	CreatedAt   string                  `json:"created_at"`
	UpdatedAt   string                  `json:"updated_at"`
}

type ProjectMemberResponse struct {
	UserID         uuid.UUID `json:"user_id"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Email          string    `json:"email"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
}

// List returns all projects with their members
func (h *ProjectHandler) List(c *gin.Context) {
	var projects []models.Project
	if err := h.db.Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Build response with members for each project
	var response []ProjectResponse
	for _, project := range projects {
		projectResp := h.buildProjectResponse(project)
		response = append(response, projectResp)
	}

	c.JSON(http.StatusOK, response)
}

// Get returns a single project with all details
func (h *ProjectHandler) Get(c *gin.Context) {
	projectID := c.Param("id")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusOK, response)
}

// Create creates a new project
func (h *ProjectHandler) Create(c *gin.Context) {
	userID, _, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user data"})
		return
	}

	var input struct {
		Name        string               `json:"name" binding:"required"`
		Description string               `json:"description"`
		Skills      []string             `json:"skills"`
		Status      models.ProjectStatus `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default to planning if no status provided
	if input.Status == "" {
		input.Status = models.ProjectStatusPlanning
	}

	var project models.Project

	// Use Transaction method - handles nested transactions via savepoints
	err = h.db.Transaction(func(tx *gorm.DB) error {
		project = models.Project{
			ProjectID:   uuid.New(),
			ProjectName: input.Name,
			Description: input.Description,
			Skills:      input.Skills,
			Status:      input.Status,
		}

		if err := tx.Create(&project).Error; err != nil {
			return err
		}

		// Create a team for this project
		team := models.Team{
			TeamID:   uuid.New(),
			TeamName: fmt.Sprintf("%s Team", input.Name),
		}

		if err := tx.Create(&team).Error; err != nil {
			return err
		}

		// Link team to project
		teamProjectPair := models.TeamProjectPair{
			TeamID:    team.TeamID,
			ProjectID: project.ProjectID,
		}

		if err := tx.Create(&teamProjectPair).Error; err != nil {
			return err
		}

		// Add creator as first team member
		teamUserPair := models.TeamUserPair{
			TeamID: team.TeamID,
			UserID: userID,
		}

		if err := tx.Create(&teamUserPair).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusCreated, response)
}

// Update updates a project
func (h *ProjectHandler) Update(c *gin.Context) {
	userID, _, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	projectID := c.Param("id")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check if user is authorized (member or admin)
	if !h.isAuthorizedForProject(projectUUID, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You must be a project member or admin to update it"})
		return
	}

	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var input struct {
		Name        string               `json:"name"`
		Description string               `json:"description"`
		Skills      []string             `json:"skills"`
		Status      models.ProjectStatus `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update fields if provided
	if input.Name != "" {
		project.ProjectName = input.Name
	}
	if input.Description != "" {
		project.Description = input.Description
	}
	if input.Skills != nil {
		project.Skills = input.Skills
	}
	if input.Status != "" {
		project.Status = input.Status
	}

	if err := h.db.Save(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusOK, response)
}

// Delete deletes a project
func (h *ProjectHandler) Delete(c *gin.Context) {
	userID, _, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	projectID := c.Param("id")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check if user is authorized (member or admin)
	if !h.isAuthorizedForProject(projectUUID, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You must be a project member or admin to delete it"})
		return
	}

	if err := h.db.Delete(&models.Project{}, "project_id = ?", projectUUID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Project deleted successfully"})
}

// AddMember adds a member to a project
func (h *ProjectHandler) AddMember(c *gin.Context) {
	projectID := c.Param("id")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input struct {
		UserID string `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userUUID, err := uuid.Parse(input.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Find team associated with this project
	var teamProjectPair models.TeamProjectPair
	if err := h.db.Where("project_id = ?", projectUUID).First(&teamProjectPair).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project team not found"})
		return
	}

	// Check if user is already a member
	var existingCount int64
	h.db.Model(&models.TeamUserPair{}).Where("team_id = ? AND user_id = ?", teamProjectPair.TeamID, userUUID).Count(&existingCount)
	if existingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this project"})
		return
	}

	// Verify user exists
	var user models.User
	if err := h.db.First(&user, "id = ?", userUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Add user to team
	teamUserPair := models.TeamUserPair{
		TeamID: teamProjectPair.TeamID,
		UserID: userUUID,
	}

	if err := h.db.Create(&teamUserPair).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member added successfully"})
}

// RemoveMember removes a member from a project
func (h *ProjectHandler) RemoveMember(c *gin.Context) {
	projectID := c.Param("id")
	userID := c.Param("userId")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Find team associated with this project
	var teamProjectPair models.TeamProjectPair
	if err := h.db.Where("project_id = ?", projectUUID).First(&teamProjectPair).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project team not found"})
		return
	}

	// Remove user from team
	if err := h.db.Where("team_id = ? AND user_id = ?", teamProjectPair.TeamID, userUUID).
		Delete(&models.TeamUserPair{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member removed successfully"})
}

// buildProjectResponse constructs a ProjectResponse with all members
func (h *ProjectHandler) buildProjectResponse(project models.Project) ProjectResponse {
	// Find team associated with this project
	var teamProjectPair models.TeamProjectPair
	h.db.Where("project_id = ?", project.ProjectID).First(&teamProjectPair)

	// Get all team members
	var teamUserPairs []models.TeamUserPair
	h.db.Where("team_id = ?", teamProjectPair.TeamID).Find(&teamUserPairs)

	// Get user profiles for all members (initialize to empty slice, not nil)
	members := make([]ProjectMemberResponse, 0)
	for _, pair := range teamUserPairs {
		var profile models.Profile
		if err := h.db.Where("user_id = ?", pair.UserID).First(&profile).Error; err == nil {
			members = append(members, ProjectMemberResponse{
				UserID:         profile.UserID,
				FirstName:      profile.FirstName,
				LastName:       profile.LastName,
				Email:          profile.Email,
				ProfilePicture: profile.ProfilePicture,
			})
		}
	}

	return ProjectResponse{
		ID:          project.ProjectID,
		Name:        project.ProjectName,
		Description: project.Description,
		Skills:      project.Skills,
		Status:      project.Status,
		Members:     members,
		CreatedAt:   project.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   project.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// getUserData retrieves user data from the JWT context
func (h *ProjectHandler) getUserData(c *gin.Context) (uuid.UUID, *models.User, error) {
	userIDStr := c.GetString("user_id")
	if userIDStr == "" {
		return uuid.Nil, nil, fmt.Errorf("user_id not found in context")
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("invalid user_id format: %v", err)
	}

	var user models.User
	if err := h.db.First(&user, "id = ?", userID).Error; err != nil {
		return uuid.Nil, nil, fmt.Errorf("user not found: %v", err)
	}

	return userID, &user, nil
}

// isAuthorizedForProject checks if a user can modify a project (member or admin)
func (h *ProjectHandler) isAuthorizedForProject(projectID, userID uuid.UUID) bool {
	// Check if user is an admin
	var user models.User
	if err := h.db.First(&user, "id = ?", userID).Error; err == nil {
		for _, role := range user.Roles {
			if role == models.RoleAdmin {
				return true
			}
		}
	}

	// Check if user is a project member
	var teamProjectPair models.TeamProjectPair
	if err := h.db.Where("project_id = ?", projectID).First(&teamProjectPair).Error; err != nil {
		return false
	}

	var count int64
	h.db.Model(&models.TeamUserPair{}).Where("team_id = ? AND user_id = ?", teamProjectPair.TeamID, userID).Count(&count)
	return count > 0
}
