package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"fmt"
	"log"
	"net/http"
	"strings"

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
	}
}

// ProjectResponse includes all project details with members
type ProjectResponse struct {
	ID                 uuid.UUID               `json:"id"`
	TeamID             *uuid.UUID              `json:"team_id,omitempty"`
	Name               string                  `json:"title"`
	OneLineDescription string                  `json:"oneLineDescription"`
	Categories         string                  `json:"categories"`
	TechStack          string                  `json:"techStack"`
	ProblemImpact      string                  `json:"problemImpact"`
	KeyFeatures        string                  `json:"keyFeatures"`
	Status             models.ProjectStatus    `json:"status"`
	Screenshots        string                  `json:"screenshots"`
	RepoUrl            string                  `json:"repoUrl"`
	Affiliations       string                  `json:"affiliations"`
	Timeline           string                  `json:"timeline"`
	MaintenancePlan    string                  `json:"maintenancePlan"`
	Contact            string                  `json:"contact"`
	Members            []ProjectMemberResponse `json:"members"`
}

type ProjectMemberResponse struct {
	UserID         uuid.UUID `json:"user_id"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Email          string    `json:"email"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	Role           string    `json:"role,omitempty"`
	Department     string    `json:"department,omitempty"`
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

	// Build responses independently using the robust builder
	response := make([]ProjectResponse, 0, len(projects))
	for _, project := range projects {
		response = append(response, h.buildProjectResponse(project))
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

// Creates a new project, team, and sets up contributors via TeamMembers
func (h *ProjectHandler) Create(c *gin.Context) {
	var input struct {
		Title              string               `json:"title" binding:"required"`
		OneLineDescription string               `json:"oneLineDescription"`
		Categories         string               `json:"categories"`
		TechStack          string               `json:"techStack"`
		ProblemImpact      string               `json:"problemImpact"`
		KeyFeatures        string               `json:"keyFeatures"`
		Status             models.ProjectStatus `json:"status"`
		Screenshots        string               `json:"screenshots"`
		RepoUrl            string               `json:"repoUrl"`
		Contributors       []string             `json:"contributors"`
		Affiliations       string               `json:"affiliations"`
		Timeline           string               `json:"timeline"`
		MaintenancePlan    string               `json:"maintenancePlan"`
		Contact            string               `json:"contact"`
		TeamName           string               `json:"teamName"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default status if empty
	if input.Status == "" {
		input.Status = "Idea"
	}

	var project models.Project

	// Handle transactions to ensure everything creates or rolls back together
	err := h.db.Transaction(func(tx *gorm.DB) error {
		project = models.Project{
			ProjectId:          uuid.New(),
			ProjectName:        input.Title,
			OneLineDescription: input.OneLineDescription,
			Categories:         input.Categories,
			TechStack:          input.TechStack,
			ProblemImpact:      input.ProblemImpact,
			KeyFeatures:        input.KeyFeatures,
			Status:             input.Status,
			Screenshots:        input.Screenshots,
			RepoUrl:            input.RepoUrl,
			Affiliations:       input.Affiliations,
			Timeline:           input.Timeline,
			MaintenancePlan:    input.MaintenancePlan,
			Contact:            input.Contact,
		}

		if err := tx.Create(&project).Error; err != nil {
			return err
		}

		// Resolve Team Name
		teamName := input.TeamName
		if teamName == "" {
			teamName = input.Title + " Team"
		}

		// Create a dedicated team for this project
		team := models.Team{
			TeamId:   uuid.New(),
			TeamName: teamName,
		}
		if err := tx.Create(&team).Error; err != nil {
			return err
		}

		// Link team to project
		teamProjectPair := models.TeamProjectPair{
			TeamId:    team.ID,
			ProjectId: project.ID,
		}
		if err := tx.Create(&teamProjectPair).Error; err != nil {
			return err
		}

		// Process contributors payload formatting string ("email:role:team")
		for _, contributorString := range input.Contributors {
			parts := strings.Split(contributorString, ":")
			if len(parts) == 0 || parts[0] == "" {
				continue
			}

			email := parts[0]
			role := ""
			department := ""

			if len(parts) > 1 {
				role = parts[1]
			}
			if len(parts) > 2 {
				department = parts[2]
			}

			// Look up the user by email
			var user models.User
			if err := tx.Where("email = ?", email).First(&user).Error; err != nil {
				return fmt.Errorf("user with email %s not found: %w", email, err)
			}

			// Create TeamMember Entity
			teamMember := models.TeamMember{
				UserID:               user.ID,
				TeamMemberRole:       role,
				TeamMemberDepartment: department,
			}
			if err := tx.Create(&teamMember).Error; err != nil {
				return fmt.Errorf("failed to create team member %s: %w", email, err)
			}

			// Pair the new TeamMember to the Team
			teamMemberPair := models.TeamMemberPair{
				TeamId:       team.ID,
				TeamMemberId: teamMember.ID,
			}
			if err := tx.Create(&teamMemberPair).Error; err != nil {
				return fmt.Errorf("failed to pair member to team: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error creating project: %v", err)
		// Ensure PII/Raw error strings are not returned to the client directly
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project and synchronize contributors."})
		return
	}

	response := h.buildProjectResponse(project)
	c.JSON(http.StatusCreated, response)
}

// Updates a project fields and synchronizes contributors
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

	// Updated Input schema for the new DTO definition
	var input struct {
		Title              *string               `json:"title"`
		OneLineDescription *string               `json:"oneLineDescription"`
		Categories         *string               `json:"categories"`
		TechStack          *string               `json:"techStack"`
		ProblemImpact      *string               `json:"problemImpact"`
		KeyFeatures        *string               `json:"keyFeatures"`
		Status             *models.ProjectStatus `json:"status"`
		Screenshots        *string               `json:"screenshots"`
		RepoUrl            *string               `json:"repoUrl"`
		Affiliations       *string               `json:"affiliations"`
		Timeline           *string               `json:"timeline"`
		MaintenancePlan    *string               `json:"maintenancePlan"`
		Contact            *string               `json:"contact"`
		TeamName           *string               `json:"teamName"`
		Contributors       []string              `json:"contributors"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Run all updates and deletions in a transaction
	err = h.db.Transaction(func(tx *gorm.DB) error {

		if input.Title != nil {
			project.ProjectName = *input.Title
		}
		if input.OneLineDescription != nil {
			project.OneLineDescription = *input.OneLineDescription
		}
		if input.Categories != nil {
			project.Categories = *input.Categories
		}
		if input.TechStack != nil {
			project.TechStack = *input.TechStack
		}
		if input.ProblemImpact != nil {
			project.ProblemImpact = *input.ProblemImpact
		}
		if input.KeyFeatures != nil {
			project.KeyFeatures = *input.KeyFeatures
		}
		if input.Status != nil {
			project.Status = *input.Status
		}
		if input.Screenshots != nil {
			project.Screenshots = *input.Screenshots
		}
		if input.RepoUrl != nil {
			project.RepoUrl = *input.RepoUrl
		}
		if input.Affiliations != nil {
			project.Affiliations = *input.Affiliations
		}
		if input.Timeline != nil {
			project.Timeline = *input.Timeline
		}
		if input.MaintenancePlan != nil {
			project.MaintenancePlan = *input.MaintenancePlan
		}
		if input.Contact != nil {
			project.Contact = *input.Contact
		}

		if err := tx.Save(&project).Error; err != nil {
			return err
		}

		var teamProjectPair models.TeamProjectPair
		var team models.Team

		// Check if a team is already linked to this project
		err := tx.Where("project_id = ?", project.ID).First(&teamProjectPair).Error
		if err == gorm.ErrRecordNotFound {
			// Backward compatibility: If no team exists yet, create one
			teamName := project.ProjectName + " Team"
			if input.TeamName != nil && *input.TeamName != "" {
				teamName = *input.TeamName
			}
			team = models.Team{
				TeamId:   uuid.New(),
				TeamName: teamName,
			}
			if err := tx.Create(&team).Error; err != nil {
				return err
			}
			teamProjectPair = models.TeamProjectPair{
				TeamId:    team.ID,
				ProjectId: project.ID,
			}
			if err := tx.Create(&teamProjectPair).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			// Team exists, load it
			if err := tx.First(&team, teamProjectPair.TeamId).Error; err != nil {
				return err
			}
			// Update TeamName if requested
			if input.TeamName != nil && *input.TeamName != "" && team.TeamName != *input.TeamName {
				team.TeamName = *input.TeamName
				if err := tx.Save(&team).Error; err != nil {
					return err
				}
			}
		}

		if input.Contributors != nil {

			// Parse incoming contributors into a map for fast lookup
			type incomingContributor struct {
				Role       string
				Department string
			}
			newContributorsMap := make(map[string]incomingContributor)

			for _, contributorString := range input.Contributors {
				parts := strings.Split(contributorString, ":")
				if len(parts) == 0 || parts[0] == "" {
					continue
				}
				email := parts[0]
				role := ""
				dept := ""
				if len(parts) > 1 {
					role = parts[1]
				}
				if len(parts) > 2 {
					dept = parts[2]
				}
				newContributorsMap[email] = incomingContributor{Role: role, Department: dept}
			}

			// Fetch existing team members associated with this team
			type existingMemberData struct {
				TeamMemberID uint
				Email        string
				Role         string
				Department   string
			}
			var existingMembers []existingMemberData

			// Corrected: Added .Error checking to prevent silent DB failures
			if err := tx.Table("team_members").
				Select("team_members.id as team_member_id, users.email, team_members.team_member_role as role, team_members.team_member_department as department").
				Joins("JOIN team_member_pairs ON team_member_pairs.team_member_id = team_members.id").
				Joins("JOIN users ON users.id = team_members.user_id").
				Where("team_member_pairs.team_id = ? AND team_members.deleted_at IS NULL", team.ID).
				Scan(&existingMembers).Error; err != nil {
				return err
			}

			// Compare existing against incoming
			for _, ext := range existingMembers {
				if incoming, exists := newContributorsMap[ext.Email]; exists {
					// User is in both lists. Check if role/department changed
					if ext.Role != incoming.Role || ext.Department != incoming.Department {
						// Corrected: Added .Error check on updates
						if err := tx.Model(&models.TeamMember{}).Where("id = ?", ext.TeamMemberID).Updates(map[string]interface{}{
							"team_member_role":       incoming.Role,
							"team_member_department": incoming.Department,
						}).Error; err != nil {
							return err
						}
					}
					// Remove from map, so remaining entries in map are purely NEW members
					delete(newContributorsMap, ext.Email)
				} else {
					// User is NOT in the new list. Remove them from the team.
					// Corrected: Added .Error check on deletes
					if err := tx.Where("team_id = ? AND team_member_id = ?", team.ID, ext.TeamMemberID).Delete(&models.TeamMemberPair{}).Error; err != nil {
						return err
					}
					if err := tx.Where("id = ?", ext.TeamMemberID).Delete(&models.TeamMember{}).Error; err != nil {
						return err
					}
				}
			}

			// Add the remaining new contributors
			for email, incoming := range newContributorsMap {
				var user models.User
				if err := tx.Where("email = ?", email).First(&user).Error; err != nil {
					return fmt.Errorf("user with email %s not found: %w", email, err)
				}

				tm := models.TeamMember{
					UserID:               user.ID,
					TeamMemberRole:       incoming.Role,
					TeamMemberDepartment: incoming.Department,
				}
				if err := tx.Create(&tm).Error; err != nil {
					return fmt.Errorf("failed to create team member %s: %w", email, err)
				}

				tmp := models.TeamMemberPair{
					TeamId:       team.ID,
					TeamMemberId: tm.ID,
				}
				if err := tx.Create(&tmp).Error; err != nil {
					return fmt.Errorf("failed to pair member %s to team: %w", email, err)
				}
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error updating project %s: %v", projectUUID, err)
		// Ensure PII/Raw error strings are not returned to the client directly
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project and synchronize contributors."})
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

		var teamPairs []models.TeamProjectPair
		if err := tx.Where("project_id = ?", project.ID).Find(&teamPairs).Error; err != nil {
			return err
		}

		for _, tp := range teamPairs {
			var memberPairs []models.TeamMemberPair
			if err := tx.Where("team_id = ?", tp.TeamId).Find(&memberPairs).Error; err != nil {
				return err
			}

			var memberIds []uint
			for _, mp := range memberPairs {
				memberIds = append(memberIds, mp.TeamMemberId)
			}

			// Delete the TeamMember rows
			if len(memberIds) > 0 {
				if err := tx.Where("id IN ?", memberIds).Delete(&models.TeamMember{}).Error; err != nil {
					return err
				}
			}

			// Delete the TeamMemberPair rows
			if err := tx.Where("team_id = ?", tp.TeamId).Delete(&models.TeamMemberPair{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("project_id = ?", project.ID).Delete(&models.TeamProjectPair{}).Error; err != nil {
			return err
		}

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

// buildProjectResponse constructs a ProjectResponse with all members
func (h *ProjectHandler) buildProjectResponse(project models.Project) ProjectResponse {
	members := make([]ProjectMemberResponse, 0)

	// Fetch team info in one query by joining teams onto team_project_pairs
	var teamInfo struct {
		TeamInternalID uint
		TeamUUID       uuid.UUID
		TeamName       string
	}
	err := h.db.Table("team_project_pairs").
		Select("team_project_pairs.team_id as team_internal_id, teams.team_id as team_uuid, teams.team_name as team_name").
		Joins("JOIN teams ON teams.id = team_project_pairs.team_id AND teams.deleted_at IS NULL").
		Where("team_project_pairs.project_id = ?", project.ID).
		Scan(&teamInfo).Error

	if err != nil {
		log.Printf("Warning: could not find team for project %s: %v", project.ProjectId, err)
	} else {
		if loaded := h.loadTeamMembers(teamInfo.TeamInternalID, project.ProjectId); loaded != nil {
			members = loaded
		}
	}

	response := ProjectResponse{
		ID:                 project.ProjectId,
		Name:               project.ProjectName,
		OneLineDescription: project.OneLineDescription,
		Categories:         project.Categories,
		TechStack:          project.TechStack,
		ProblemImpact:      project.ProblemImpact,
		KeyFeatures:        project.KeyFeatures,
		Status:             project.Status,
		Screenshots:        project.Screenshots,
		RepoUrl:            project.RepoUrl,
		Affiliations:       project.Affiliations,
		Timeline:           project.Timeline,
		MaintenancePlan:    project.MaintenancePlan,
		Contact:            project.Contact,
		Members:            members,
	}

	if err == nil && teamInfo.TeamInternalID != 0 {
		response.TeamID = &teamInfo.TeamUUID
	}

	return response
}

// loadTeamMembers fetches profiles via the new TeamMember relations
func (h *ProjectHandler) loadTeamMembers(teamID uint, projectUUID uuid.UUID) []ProjectMemberResponse {
	var teamMembers []models.TeamMember

	// Join team_user_pairs to get members tied strictly to this team via their TeamMemberId
	if err := h.db.Table("team_members").
		Joins("JOIN team_member_pairs ON team_member_pairs.team_member_id = team_members.id").
		Where("team_member_pairs.team_id = ?", teamID).
		Find(&teamMembers).Error; err != nil {
		log.Printf("Warning: could not find team members for team %d: %v", teamID, err)
		return nil
	}

	if len(teamMembers) == 0 {
		return nil
	}

	// Fetch standard User profiles associated with the extracted user_ids
	userIDs := make([]uint, len(teamMembers))
	for i, m := range teamMembers {
		userIDs[i] = m.UserID
	}

	var profiles []models.Profile
	if err := h.db.Where("user_id IN ?", userIDs).Find(&profiles).Error; err != nil {
		log.Printf("Warning: could not load profiles for project %s: %v", projectUUID, err)
		return nil
	}

	// Map profiles to easily match them back to their TeamMember instance
	profileMap := make(map[uint]models.Profile)
	for _, p := range profiles {
		profileMap[p.UserId] = p
	}

	members := make([]ProjectMemberResponse, 0, len(teamMembers))
	for _, tm := range teamMembers {
		if profile, exists := profileMap[tm.UserID]; exists {
			members = append(members, ProjectMemberResponse{
				UserID:         profile.UserUUID,
				FirstName:      profile.FirstName,
				LastName:       profile.LastName,
				Email:          profile.Email,
				ProfilePicture: profile.ProfilePicture,
				Role:           tm.TeamMemberRole,
				Department:     tm.TeamMemberDepartment,
			})
		}
	}

	return members
}
