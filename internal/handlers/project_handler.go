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

		// Authenticated member endpoints
		authenticated := projects.Group("")
		authenticated.Use(middleware.AuthRequiredJWT(h.cfg))
		authenticated.PUT("/:id/media", h.UpdateProjectMedia)
		authenticated.GET("/my-associations", h.GetMyProjectAssociations)
		authenticated.PUT("/my-associations", h.UpdateMyProjectAssociations)

		// Admin-only write endpoints
		admin := authenticated.Group("")
		admin.Use(middleware.RoleRequired(h.cfg, "admin"))
		admin.POST("", h.Create)
		admin.PUT("/:id", h.Update)
		admin.DELETE("/:id", h.Delete)
		admin.POST("/:id/members", h.AdminAddMember)
		admin.DELETE("/:id/members", h.AdminRemoveMember)
	}
}

// ProjectResponse includes all project details with members.
type ProjectResponse struct {
	ID                 uuid.UUID               `json:"id"`
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

// ProjectMemberResponse — no role/department, projects carry no elaboration on what a member did.
type ProjectMemberResponse struct {
	UserID         uuid.UUID `json:"user_id"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Email          string    `json:"email"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
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

// buildProjectResponse constructs a ProjectResponse with members from project_members.
func (h *ProjectHandler) buildProjectResponse(project models.Project) ProjectResponse {
	return ProjectResponse{
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
		Members:            h.loadProjectMembers(project.ID, project.ProjectId),
	}
}

// loadProjectMembers fetches contributors via the project_members join table.
func (h *ProjectHandler) loadProjectMembers(projectID uint, projectUUID uuid.UUID) []ProjectMemberResponse {
	type memberRow struct {
		UserUUID       uuid.UUID `gorm:"column:user_uuid"`
		FirstName      string    `gorm:"column:first_name"`
		LastName       string    `gorm:"column:last_name"`
		Email          string    `gorm:"column:email"`
		ProfilePicture string    `gorm:"column:profile_picture"`
	}
	var rows []memberRow
	if err := h.db.Table("project_members").
		Select("profiles.user_uuid, profiles.first_name, profiles.last_name, profiles.email, profiles.profile_picture").
		Joins("JOIN profiles ON profiles.user_id = project_members.user_id AND profiles.deleted_at IS NULL").
		Where("project_members.project_id = ?", projectID).
		Scan(&rows).Error; err != nil {
		log.Printf("Warning: could not load members for project %s: %v", projectUUID, err)
		return nil
	}
	members := make([]ProjectMemberResponse, 0, len(rows))
	for _, r := range rows {
		members = append(members, ProjectMemberResponse{
			UserID:         r.UserUUID,
			FirstName:      r.FirstName,
			LastName:       r.LastName,
			Email:          r.Email,
			ProfilePicture: r.ProfilePicture,
		})
	}
	return members
}

// syncProjectMembers diffs desired emails against current project_members rows.
// Unknown emails are skipped with a warning — one bad email won't fail the whole operation.
func (h *ProjectHandler) syncProjectMembers(tx *gorm.DB, projectID uint, emails []string) error {
	desired := map[string]bool{}
	for _, e := range emails {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			desired[e] = true
		}
	}

	type currentRow struct {
		UserID uint   `gorm:"column:user_id"`
		Email  string `gorm:"column:email"`
	}
	var current []currentRow
	if err := tx.Table("project_members").
		Select("project_members.user_id, users.email").
		Joins("JOIN users ON users.id = project_members.user_id").
		Where("project_members.project_id = ?", projectID).
		Scan(&current).Error; err != nil {
		return err
	}

	// Remove members not in the desired set
	for _, cm := range current {
		lower := strings.ToLower(cm.Email)
		if !desired[lower] {
			if err := tx.Where("project_id = ? AND user_id = ?", projectID, cm.UserID).
				Delete(&models.ProjectMember{}).Error; err != nil {
				return err
			}
		}
		delete(desired, lower) // already present
	}

	// Add new members; skip emails not in users table
	for email := range desired {
		var user models.User
		if err := tx.Where("LOWER(email) = ?", email).First(&user).Error; err != nil {
			log.Printf("Warning: contributor %q not found, skipping: %v", email, err)
			continue
		}
		pm := models.ProjectMember{ProjectID: projectID, UserID: user.ID}
		if err := tx.Where(pm).FirstOrCreate(&pm).Error; err != nil {
			return err
		}
	}
	return nil
}

// List returns all projects with their members.
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
	response := make([]ProjectResponse, 0, len(projects))
	for _, project := range projects {
		response = append(response, h.buildProjectResponse(project))
	}
	c.JSON(http.StatusOK, response)
}

// Get returns a single project with all details.
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
	c.JSON(http.StatusOK, h.buildProjectResponse(project))
}

// Create creates a new project and records contributors in project_members.
func (h *ProjectHandler) Create(c *gin.Context) {
	var input struct {
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
		TeamName           string               `json:"teamName"` // accepted for API compat
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.Status == "" {
		input.Status = models.ProjectStatusIdea
	} else if !isValidProjectStatus(input.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid project status: %s", input.Status)})
		return
	}

	var project models.Project
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
		return h.syncProjectMembers(tx, project.ID, input.Contributors)
	})

	if err != nil {
		log.Printf("Error creating project: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project"})
		return
	}
	c.JSON(http.StatusCreated, h.buildProjectResponse(project))
}

// Update updates project fields and syncs contributors via project_members.
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
		TeamName           *string               `json:"teamName"` // accepted for API compat
		Contributors       []string              `json:"contributors"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Guard against blanking NOT NULL columns
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
	if input.Status != nil && !isValidProjectStatus(*input.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid project status: %s", *input.Status)})
		return
	}

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

		if input.Contributors != nil {
			return h.syncProjectMembers(tx, project.ID, input.Contributors)
		}
		return nil
	})

	if err != nil {
		log.Printf("Error updating project %s: %v", projectUUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
		return
	}
	c.JSON(http.StatusOK, h.buildProjectResponse(project))
}

// Delete removes a project and its project_members entries.
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
		if err := tx.Where("project_id = ?", project.ID).Delete(&models.ProjectMember{}).Error; err != nil {
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

// AdminAddMember lets an admin add a user (by email) to a project.
func (h *ProjectHandler) AdminAddMember(c *gin.Context) {
	projectID := c.Param("id")
	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	var input struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}
	var user models.User
	if err := h.db.Where("LOWER(email) = LOWER(?)", input.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	pm := models.ProjectMember{ProjectID: project.ID, UserID: user.ID}
	if err := h.db.Where(pm).FirstOrCreate(&pm).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Member added"})
}

// AdminRemoveMember lets an admin remove a user (by email) from a project.
func (h *ProjectHandler) AdminRemoveMember(c *gin.Context) {
	projectID := c.Param("id")
	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	var input struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var project models.Project
	if err := h.db.First(&project, "project_id = ?", projectUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}
	var user models.User
	if err := h.db.Where("LOWER(email) = LOWER(?)", input.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if err := h.db.Where("project_id = ? AND user_id = ?", project.ID, user.ID).
		Delete(&models.ProjectMember{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Member removed"})
}

// GetMyProjectAssociations returns all projects, marking ones the current user is a member of.
func (h *ProjectHandler) GetMyProjectAssociations(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userUUIDStr, ok := claims["user_id"].(string)
	if !ok || userUUIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}
	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUIDStr).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	var memberProjectIDs []uint
	if err := h.db.Table("project_members").
		Where("user_id = ?", profile.UserId).
		Pluck("project_id", &memberProjectIDs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load current associations"})
		return
	}
	selectedSet := make(map[uint]bool, len(memberProjectIDs))
	for _, id := range memberProjectIDs {
		selectedSet[id] = true
	}

	var projects []models.Project
	if err := h.db.Order("project_name ASC").Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load projects"})
		return
	}

	type responseRow struct {
		ID       uuid.UUID            `json:"id"`
		Title    string               `json:"title"`
		Status   models.ProjectStatus `json:"status"`
		Selected bool                 `json:"selected"`
	}
	result := make([]responseRow, 0, len(projects))
	for _, p := range projects {
		result = append(result, responseRow{
			ID:       p.ProjectId,
			Title:    p.ProjectName,
			Status:   p.Status,
			Selected: selectedSet[p.ID],
		})
	}
	c.JSON(http.StatusOK, result)
}

// UpdateMyProjectAssociations replaces the current user's full set of project memberships.
func (h *ProjectHandler) UpdateMyProjectAssociations(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userUUIDStr, ok := claims["user_id"].(string)
	if !ok || userUUIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token"})
		return
	}
	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUIDStr).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	var input struct {
		ProjectIDs []string `json:"projectIds"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		// Delete all existing memberships for this user, then re-add desired ones
		if err := tx.Where("user_id = ?", profile.UserId).Delete(&models.ProjectMember{}).Error; err != nil {
			return err
		}
		for _, pid := range input.ProjectIDs {
			projectUUID, err := uuid.Parse(pid)
			if err != nil {
				return fmt.Errorf("invalid project ID: %s", pid)
			}
			var project models.Project
			if err := tx.First(&project, "project_id = ?", projectUUID).Error; err != nil {
				return fmt.Errorf("project not found: %s", pid)
			}
			pm := models.ProjectMember{ProjectID: project.ID, UserID: profile.UserId}
			if err := tx.Create(&pm).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("UpdateMyProjectAssociations error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project associations"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Project associations updated"})
}

// UpdateProjectMedia handles cover image and screenshot uploads for a project.
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

	isAdmin := false
	if rolesClaim, _ := claims["roles"].(string); rolesClaim != "" {
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
		var count int64
		if err := h.db.Table("project_members").
			Where("project_id = ? AND user_id = ?", project.ID, profile.UserId).
			Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate project membership"})
			return
		}
		if count == 0 {
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
		existingScreenshots = append(
			[]projectScreenshot{{Image: coverURL, Caption: "Cover image", Alt: "Project cover image"}},
			existingScreenshots...,
		)
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
			_ = reader.Close()
			if readErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read screenshot"})
				return
			}
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
			existingScreenshots = append(existingScreenshots, projectScreenshot{
				Image:   fmt.Sprintf("/api/v1/projects/media?id=%s", blob.BlobId.String()),
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
	c.JSON(http.StatusOK, gin.H{"message": "Project media updated successfully", "screenshots": existingScreenshots})
}

// GetProjectMedia serves a stored blob by UUID.
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

// isValidProjectStatus checks if the provided status matches the allowed enum values.
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
