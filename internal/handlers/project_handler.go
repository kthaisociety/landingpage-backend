package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"log"
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
		// Public endpoints
		projects.GET("", h.List)
		projects.GET("/:id", h.Get)

		// Authenticated endpoints — admin only for writes
		authenticated := projects.Group("")
		authenticated.Use(middleware.AuthRequiredJWT(h.cfg))
		authenticated.Use(middleware.RoleRequired(h.cfg, "admin"))
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
	TeamID      *uuid.UUID              `json:"team_id,omitempty"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Skills      []string                `json:"skills"`
	Status      models.ProjectStatus    `json:"status"`
	Members     []ProjectMemberResponse `json:"members"`
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
		log.Printf("Error listing projects: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list projects"})
		return
	}

	if len(projects) == 0 {
		c.JSON(http.StatusOK, []ProjectResponse{})
		return
	}

	// Batch-load all team-project pairs
	projectIDs := make([]uint, len(projects))
	for i, p := range projects {
		projectIDs[i] = p.ID
	}

	var teamProjectPairs []models.TeamProjectPair
	if err := h.db.Where("project_id IN ?", projectIDs).Find(&teamProjectPairs).Error; err != nil {
		log.Printf("Error loading team-project pairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list projects"})
		return
	}

	// Map project_id -> team_id
	projectToTeam := make(map[uint]uint)
	teamIDs := make([]uint, 0, len(teamProjectPairs))
	for _, tp := range teamProjectPairs {
		projectToTeam[tp.ProjectId] = tp.TeamId
		teamIDs = append(teamIDs, tp.TeamId)
	}

	// Batch-load all team-user pairs
	var teamUserPairs []models.TeamUserPair
	if len(teamIDs) > 0 {
		if err := h.db.Where("team_id IN ?", teamIDs).Find(&teamUserPairs).Error; err != nil {
			log.Printf("Error loading team-user pairs: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list projects"})
			return
		}
	}

	// Map team_id -> []user_id
	teamToUsers := make(map[uint][]uint)
	allUserIDs := make([]uint, 0)
	for _, tu := range teamUserPairs {
		teamToUsers[tu.TeamId] = append(teamToUsers[tu.TeamId], tu.UserId)
		allUserIDs = append(allUserIDs, tu.UserId)
	}

	// Batch-load all profiles
	profileMap := make(map[uint]models.Profile)
	if len(allUserIDs) > 0 {
		var profiles []models.Profile
		h.db.Where("user_id IN ?", allUserIDs).Find(&profiles)
		for _, p := range profiles {
			profileMap[p.UserId] = p
		}
	}

	// Build response
	response := make([]ProjectResponse, 0, len(projects))
	for _, project := range projects {
		members := make([]ProjectMemberResponse, 0)
		if teamID, ok := projectToTeam[project.ID]; ok {
			for _, userID := range teamToUsers[teamID] {
				if profile, ok := profileMap[userID]; ok {
					members = append(members, ProjectMemberResponse{
						UserID:         profile.UserUUID,
						FirstName:      profile.FirstName,
						LastName:       profile.LastName,
						Email:          profile.Email,
						ProfilePicture: profile.ProfilePicture,
					})
				}
			}
		}

		skills := project.Skills
		if skills == nil {
			skills = []string{}
		}

		response = append(response, ProjectResponse{
			ID:          project.ProjectId,
			Name:        project.ProjectName,
			Description: project.Description,
			Skills:      skills,
			Status:      project.Status,
			Members:     members,
		})
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
		log.Printf("Error fetching project %s: %v", projectUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project"})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusOK, response)
}

// Creates a new project
func (h *ProjectHandler) Create(c *gin.Context) {
	var input struct {
		Name        string               `json:"name" binding:"required"`
		Description string               `json:"description"`
		Skills      []string             `json:"skills"`
		Status      models.ProjectStatus `json:"status"`
		TeamID      *string              `json:"team_id"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default to planning if no status provided
	if input.Status == "" {
		input.Status = models.ProjectStatusPlanning
	}

	if !isValidProjectStatus(input.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status. Must be one of: planning, active, completed"})
		return
	}

	var providedTeamID *uuid.UUID
	if input.TeamID != nil {
		parsedTeamID, err := uuid.Parse(*input.TeamID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team_id"})
			return
		}
		providedTeamID = &parsedTeamID
	}

	var project models.Project

	// Use Transaction method - handles nested transactions via savepoints
	err := h.db.Transaction(func(tx *gorm.DB) error {
		project = models.Project{
			ProjectId:   uuid.New(),
			ProjectName: input.Name,
			Description: input.Description,
			Skills:      input.Skills,
			Status:      input.Status,
		}

		if err := tx.Create(&project).Error; err != nil {
			return err
		}

		var teamInternalID uint
		if providedTeamID != nil {
			var team models.Team
			if err := tx.First(&team, "team_id = ?", *providedTeamID).Error; err != nil {
				return err
			}
			teamInternalID = team.ID
		} else {
			// Backward-compatible behavior: create a dedicated team when none is provided.
			team := models.Team{
				TeamId:   uuid.New(),
				TeamName: input.Name + " Team",
			}

			if err := tx.Create(&team).Error; err != nil {
				return err
			}
			teamInternalID = team.ID
		}

		// Link team to project
		teamProjectPair := models.TeamProjectPair{
			TeamId:    teamInternalID,
			ProjectId: project.ID,
		}

		if err := tx.Create(&teamProjectPair).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
			return
		}
		log.Printf("Error creating project: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project"})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusCreated, response)
}

// Updates a project
func (h *ProjectHandler) Update(c *gin.Context) {
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
		log.Printf("Error fetching project %s for update: %v", projectUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project"})
		return
	}

	var input struct {
		Name        *string              `json:"name"`
		Description *string              `json:"description"`
		Skills      []string             `json:"skills"`
		Status      models.ProjectStatus `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update fields if provided
	if input.Name != nil {
		if *input.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Name cannot be empty"})
			return
		}
		project.ProjectName = *input.Name
	}
	if input.Description != nil {
		project.Description = *input.Description
	}
	if input.Skills != nil {
		project.Skills = input.Skills
	}
	if input.Status != "" {
		if !isValidProjectStatus(input.Status) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status. Must be one of: planning, active, completed"})
			return
		}
		project.Status = input.Status
	}

	if err := h.db.Save(&project).Error; err != nil {
		log.Printf("Error updating project %s: %v", projectUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusOK, response)
}

// Deletes a project and removes its team-project links without deleting teams
func (h *ProjectHandler) Delete(c *gin.Context) {
	projectID := c.Param("id")

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		var project models.Project
		if err := tx.First(&project, "project_id = ?", projectUUID).Error; err != nil {
			return err
		}

		// Remove all links between this project and any teams.
		if err := tx.Where("project_id = ?", project.ID).Delete(&models.TeamProjectPair{}).Error; err != nil {
			return err
		}

		// Delete the project
		result := tx.Delete(&models.Project{}, "id = ?", project.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		log.Printf("Error deleting project %s: %v", projectUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete project"})
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

	// Find project and team associated with this project
	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var teamProjectPair models.TeamProjectPair
	if err := h.db.Where("project_id = ?", project.ID).First(&teamProjectPair).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project team not found"})
		return
	}

	// Verify user exists
	var user models.User
	if err := h.db.First(&user, "user_id = ?", userUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Add user to team within a transaction to avoid race conditions
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.TeamUserPair{}).
			Where("team_id = ? AND user_id = ?", teamProjectPair.TeamId, user.ID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return gorm.ErrDuplicatedKey
		}

		return tx.Create(&models.TeamUserPair{
			TeamId: teamProjectPair.TeamId,
			UserId: user.ID,
		}).Error
	})

	if err != nil {
		if err == gorm.ErrDuplicatedKey {
			c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this project"})
			return
		}
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

	// Find project and team associated with this project
	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var teamProjectPair models.TeamProjectPair
	if err := h.db.Where("project_id = ?", project.ID).First(&teamProjectPair).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project team not found"})
		return
	}

	// Verify user exists
	var user models.User
	if err := h.db.First(&user, "user_id = ?", userUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Remove user from team
	result := h.db.Where("team_id = ? AND user_id = ?", teamProjectPair.TeamId, user.ID).
		Delete(&models.TeamUserPair{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User is not a member of this project"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member removed successfully"})
}

// buildProjectResponse constructs a ProjectResponse with all members
func (h *ProjectHandler) buildProjectResponse(project models.Project) ProjectResponse {
	members := make([]ProjectMemberResponse, 0)

	// Find team associated with this project
	var teamProjectPair models.TeamProjectPair
	err := h.db.Where("project_id = ?", project.ID).First(&teamProjectPair).Error
	if err != nil {
		log.Printf("Warning: could not find team for project %s: %v", project.ProjectId, err)
	} else {
		// Only query members if we found a team
		if loaded := h.loadTeamMembers(teamProjectPair.TeamId, project.ProjectId); loaded != nil {
			members = loaded
		}
	}

	skills := project.Skills
	if skills == nil {
		skills = []string{}
	}

	response := ProjectResponse{
		ID:          project.ProjectId,
		Name:        project.ProjectName,
		Description: project.Description,
		Skills:      skills,
		Status:      project.Status,
		Members:     members,
	}

	if err == nil {
		var team models.Team
		if err := h.db.First(&team, teamProjectPair.TeamId).Error; err == nil {
			response.TeamID = &team.TeamId
		}
	}

	return response
}

// loadTeamMembers fetches profiles for all users in the given team.
func (h *ProjectHandler) loadTeamMembers(teamID uint, projectUUID uuid.UUID) []ProjectMemberResponse {
	var teamUserPairs []models.TeamUserPair
	if err := h.db.Where("team_id = ?", teamID).Find(&teamUserPairs).Error; err != nil {
		log.Printf("Warning: could not find team members for team %d: %v", teamID, err)
		return nil
	}

	if len(teamUserPairs) == 0 {
		return nil
	}

	userIDs := make([]uint, len(teamUserPairs))
	for i, pair := range teamUserPairs {
		userIDs[i] = pair.UserId
	}

	var profiles []models.Profile
	if err := h.db.Where("user_id IN ?", userIDs).Find(&profiles).Error; err != nil {
		log.Printf("Warning: could not load profiles for project %s: %v", projectUUID, err)
		return nil
	}

	members := make([]ProjectMemberResponse, 0, len(profiles))
	for _, profile := range profiles {
		members = append(members, ProjectMemberResponse{
			UserID:         profile.UserUUID,
			FirstName:      profile.FirstName,
			LastName:       profile.LastName,
			Email:          profile.Email,
			ProfilePicture: profile.ProfilePicture,
		})
	}
	return members
}

// isValidProjectStatus checks if a status string is one of the allowed values
func isValidProjectStatus(status models.ProjectStatus) bool {
	switch status {
	case models.ProjectStatusPlanning, models.ProjectStatusActive, models.ProjectStatusCompleted:
		return true
	}
	return false
}
