package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
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
		projects.GET("/media", h.GetProjectMedia)
		projects.GET("/:id", h.Get)

		// Authenticated member endpoint for project media updates
		authenticated := projects.Group("")
		authenticated.Use(middleware.AuthRequiredJWT(h.cfg))
		authenticated.PUT("/:id/media", h.UpdateProjectMedia)

		// Admin-only write endpoints
		admin := authenticated.Group("")
		admin.Use(middleware.RoleRequired(h.cfg, "admin"))
		admin.POST("", h.Create)
		admin.PUT("/:id", h.Update)
		admin.DELETE("/:id", h.Delete)
	}
}

type projectScreenshot struct {
	Image   string `json:"image"`
	Caption string `json:"caption"`
	Alt     string `json:"alt,omitempty"`
}

func parseProjectScreenshots(raw string) []projectScreenshot {
	if raw == "" {
		return []projectScreenshot{}
	}
	var screenshots []projectScreenshot
	if err := json.Unmarshal([]byte(raw), &screenshots); err != nil {
		return []projectScreenshot{}
	}
	return screenshots
}

func (h *ProjectHandler) UpdateProjectMedia(c *gin.Context) {
	projectID := c.Param("id")
	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userUUIDStr, ok := claims["user_id"].(string)
	if !ok || userUUIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}
	rolesClaim, _ := claims["roles"].(string)
	isAdmin := false
	if rolesClaim != "" {
		for _, role := range strings.Split(rolesClaim, ",") {
			if strings.TrimSpace(role) == "admin" {
				isAdmin = true
				break
			}
		}
	}

	userUUID, err := uuid.Parse(userUUIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user"})
		return
	}

	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load project"})
		return
	}

	if !isAdmin {
		var profile models.Profile
		if err := h.db.Where("user_uuid = ?", userUUID).First(&profile).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Profile not found for user"})
			return
		}

		// Allow contributors to update project media.
		var contributorCount int64
		if err := h.db.Table("team_members").
			Joins("JOIN team_member_pairs ON team_member_pairs.team_member_id = team_members.id").
			Joins("JOIN team_project_pairs ON team_project_pairs.team_id = team_member_pairs.team_id").
			Where("team_project_pairs.project_id = ? AND team_members.user_id = ? AND team_members.deleted_at IS NULL", project.ID, profile.UserId).
			Count(&contributorCount).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate project contributor"})
			return
		}
		if contributorCount == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "Only project contributors can update project media"})
			return
		}
	}

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage unavailable"})
		return
	}

	existingScreenshots := parseProjectScreenshots(project.Screenshots)
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Expected multipart form data"})
		return
	}

	newCoverUploaded := false
	if coverFiles := form.File["coverImage"]; len(coverFiles) > 0 {
		coverFile := coverFiles[0]
		reader, openErr := coverFile.Open()
		if openErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to open cover image"})
			return
		}
		defer reader.Close()

		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read cover image"})
			return
		}

		parts := strings.Split(coverFile.Filename, ".")
		if len(parts) < 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cover image filename"})
			return
		}
		name := strings.Join(parts[:len(parts)-1], ".")
		ext := parts[len(parts)-1]

		blob, blobErr := models.NewBlobData(name, ext, userUUID, data, h.db, r2)
		if blobErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store cover image"})
			return
		}

		coverURL := fmt.Sprintf("/api/v1/projects/media?id=%s", blob.BlobId.String())
		coverEntry := projectScreenshot{
			Image:   coverURL,
			Caption: "Cover image",
			Alt:     "Project cover image",
		}
		existingScreenshots = append([]projectScreenshot{coverEntry}, existingScreenshots...)
		newCoverUploaded = true
	}

	if screenshotFiles := form.File["screenshots"]; len(screenshotFiles) > 0 {
		for _, shotFile := range screenshotFiles {
			reader, openErr := shotFile.Open()
			if openErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to open screenshot"})
				return
			}

			data, readErr := io.ReadAll(reader)
			if readErr != nil {
				_ = reader.Close()
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read screenshot"})
				return
			}
			_ = reader.Close()

			parts := strings.Split(shotFile.Filename, ".")
			if len(parts) < 2 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid screenshot filename"})
				return
			}
			name := strings.Join(parts[:len(parts)-1], ".")
			ext := parts[len(parts)-1]

			blob, blobErr := models.NewBlobData(name, ext, userUUID, data, h.db, r2)
			if blobErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store screenshot"})
				return
			}

			shotURL := fmt.Sprintf("/api/v1/projects/media?id=%s", blob.BlobId.String())
			existingScreenshots = append(existingScreenshots, projectScreenshot{
				Image:   shotURL,
				Caption: "Project screenshot",
			})
		}
	}

	if !newCoverUploaded && len(form.File["screenshots"]) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No media files were provided"})
		return
	}

	serialized, err := json.Marshal(existingScreenshots)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize project media"})
		return
	}

	project.Screenshots = string(serialized)
	if err := h.db.Save(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project media"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Project media updated successfully",
		"screenshots": existingScreenshots,
	})
}

func (h *ProjectHandler) GetProjectMedia(c *gin.Context) {
	mediaID := c.Query("id")
	mediaUUID, err := uuid.Parse(mediaID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid media ID"})
		return
	}

	var blob models.BlobData
	if err := h.db.First(&blob, "blob_id = ?", mediaUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Media not found"})
		return
	}

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage unavailable"})
		return
	}

	data, err := blob.GetData(r2)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch media"})
		return
	}

	contentType := mime.TypeByExtension("." + strings.ToLower(blob.FType))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(http.StatusOK, contentType, data)
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
		// Enforce required fields based on DB constraints
		Title              string               `json:"title" binding:"required"`
		OneLineDescription string               `json:"oneLineDescription" binding:"required"`
		Categories         string               `json:"categories" binding:"required"`
		TechStack          string               `json:"techStack" binding:"required"`
		ProblemImpact      string               `json:"problemImpact" binding:"required"`
		KeyFeatures        string               `json:"keyFeatures" binding:"required"`
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

	// Validate Status against enum
	if input.Status == "" {
		input.Status = models.ProjectStatusIdea
	} else if !isValidProjectStatus(input.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid project status: %s", input.Status)})
		return
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

	// Prevent passing empty strings to NOT NULL columns
	if input.Title != nil && *input.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title cannot be empty"})
		return
	}
	if input.OneLineDescription != nil && *input.OneLineDescription == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oneLineDescription cannot be empty"})
		return
	}
	if input.Categories != nil && *input.Categories == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "categories cannot be empty"})
		return
	}
	if input.TechStack != nil && *input.TechStack == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "techStack cannot be empty"})
		return
	}
	if input.ProblemImpact != nil && *input.ProblemImpact == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "problemImpact cannot be empty"})
		return
	}
	if input.KeyFeatures != nil && *input.KeyFeatures == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keyFeatures cannot be empty"})
		return
	}

	// Validate Status against Enum if provided
	if input.Status != nil {
		if !isValidProjectStatus(*input.Status) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid project status: %s", *input.Status)})
			return
		}
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

// Deletes a project and its associated teams, team members, and links
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

		var teamIds []uint

		for _, tp := range teamPairs {
			// Track the TeamId so we can delete the Team itself later
			teamIds = append(teamIds, tp.TeamId)

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

		if len(teamIds) > 0 {
			if err := tx.Where("id IN ?", teamIds).Delete(&models.Team{}).Error; err != nil {
				return err
			}
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

// isValidProjectStatus checks if the provided status matches the allowed enum values
func isValidProjectStatus(status models.ProjectStatus) bool {
	switch status {
	case models.ProjectStatusIdea,
		models.ProjectStatusPrototype,
		models.ProjectStatusDevelopment,
		models.ProjectStatusBeta,
		models.ProjectStatusLive:
		return true
	}
	return false
}
