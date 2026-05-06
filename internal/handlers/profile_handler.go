package handlers

import (
	"backend/internal/config"
	"backend/internal/mailchimp"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ProfileHandler struct {
	db        *gorm.DB
	mailchimp *mailchimp.MailchimpAPI
	cfg       *config.Config
}

func NewProfileHandler(db *gorm.DB, mailchimp *mailchimp.MailchimpAPI, cfg *config.Config) *ProfileHandler {
	return &ProfileHandler{db: db, mailchimp: mailchimp, cfg: cfg}
}

func (h *ProfileHandler) Register(r *gin.RouterGroup) {
	profile := r.Group("/profile")

	// Public endpoint — no auth needed to serve profile pictures
	profile.GET("/picture", h.GetProfilePicture)

	// Public profile page endpoint
	profile.GET("/public/:profileId", h.GetPublicProfile)

	// Auth-required endpoints
	protected := profile.Group("")
	protected.Use(middleware.AuthRequiredJWT(h.cfg))
	protected.GET("/me", h.GetMyProfile)
	protected.PUT("/update", h.UpdateMyProfile)
	protected.POST("/create", h.CreateMyProfile)
	protected.POST("/picture", h.UploadProfilePicture)

	// Admin-only endpoints
	admin := protected.Group("/admin")
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.GET("", h.ListAllProfiles)
	admin.PUT("/:userId", h.UpdateProfile)
	admin.GET("/:userId", h.GetProfile)
	admin.DELETE("/:userId", h.DeleteProfile)
}

func (h *ProfileHandler) GetMyProfile(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)

	userID, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		log.Printf("Could not get userid")
	}

	var roles []string
	if rolesClaim, ok := claims["roles"].(string); ok && rolesClaim != "" {
		roles = strings.Split(rolesClaim, ",") // Converts "user,admin" -> ["user", "admin"]
	} else {
		roles = []string{"user"}
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{
			"userId": userID,
			"exists": false,
			"roles":  roles,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"userId":         userID,
		"exists":         true,
		"roles":          roles,
		"email":          profile.Email,
		"firstName":      profile.FirstName,
		"lastName":       profile.LastName,
		"university":     profile.University,
		"programme":      profile.Programme,
		"graduationYear": profile.GraduationYear,
		"githubLink":     profile.GitHubLink,
		"linkedInLink":   profile.LinkedInLink,
		"aboutMe":        profile.AboutMe,
		"profilePicture": profile.ProfilePicture,
	})
}

// UpdateMyProfile allows a user to update their own profile
func (h *ProfileHandler) UpdateMyProfile(c *gin.Context) {
	// get userId from jwt now
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userID, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		log.Printf("Could not get userid")
	}
	// Check if profile exists
	var existingProfile models.Profile
	result := h.db.Where("user_uuid = ?", userID).First(&existingProfile)
	// Parse input
	var input struct {
		FirstName      string              `json:"firstName" binding:"required"`
		LastName       string              `json:"lastName" binding:"required"`
		Email          string              `json:"email" binding:"required,email"`
		University     string              `json:"university"`
		Programme      models.StudyProgram `json:"programme"`
		GraduationYear int                 `json:"graduationYear"`
		GitHubLink     string              `json:"githubLink"`
		LinkedInLink   string              `json:"linkedinLink"`
		AboutMe        string              `json:"aboutMe"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If profile exists, update it
	if result.Error == nil {
		existingProfile.FirstName = input.FirstName
		existingProfile.LastName = input.LastName
		existingProfile.Email = input.Email
		existingProfile.University = input.University
		existingProfile.Programme = input.Programme
		existingProfile.GraduationYear = input.GraduationYear
		existingProfile.GitHubLink = input.GitHubLink
		existingProfile.LinkedInLink = input.LinkedInLink
		existingProfile.AboutMe = input.AboutMe

		if err := h.db.Save(&existingProfile).Error; err != nil {
			log.Printf("Exist profile error")
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Update member in Mailchimp
		memberRequest := mailchimp.MemberRequest{
			Email:  existingProfile.Email,
			Status: mailchimp.Subscribed,
			MergeFields: mailchimp.MergeFields{
				FirstName:      existingProfile.FirstName,
				LastName:       existingProfile.LastName,
				Programme:      string(existingProfile.Programme),
				GraduationYear: existingProfile.GraduationYear,
			},
		}
		if _, err := h.mailchimp.UpdateMember(&existingProfile.Email, &memberRequest); err != nil {
			log.Printf("Mailchimp Update Error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, existingProfile)
		return
	}

	// Check if profile exist
	var user models.User
	if err := h.db.First(&user, "user_id = ?", userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	newProfile := models.Profile{
		UserUUID:       userID,
		UserId:         user.ID,
		FirstName:      input.FirstName,
		LastName:       input.LastName,
		Email:          input.Email,
		University:     input.University,
		Programme:      input.Programme,
		GraduationYear: input.GraduationYear,
		GitHubLink:     input.GitHubLink,
		LinkedInLink:   input.LinkedInLink,
		AboutMe:        input.AboutMe,
	}

	if err := h.db.Create(&newProfile).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Add member to Mailchimp
	if err := h.mailchimp.SubscribeMember(&newProfile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newProfile)
}

// CreateMyProfile creates a profile for the authenticated user
func (h *ProfileHandler) CreateMyProfile(c *gin.Context) {
	// get userId from jwt now
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userID, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		log.Printf("Could not get userid")
	}

	// Check if profile already exists
	var existingProfile models.Profile
	result := h.db.Where("user_uuid = ?", userID).First(&existingProfile)
	if result.Error == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "Profile already exists",
			"profile": existingProfile,
		})
		return
	}

	// Parse input
	var input struct {
		FirstName      string              `json:"firstName" binding:"required"`
		LastName       string              `json:"lastName" binding:"required"`
		Email          string              `json:"email" binding:"required,email"`
		University     string              `json:"university"`
		Programme      models.StudyProgram `json:"programme"`
		GraduationYear int                 `json:"graduationYear"`
		GitHubLink     string              `json:"githubLink"`
		LinkedInLink   string              `json:"linkedinLink"`
		AboutMe        string              `json:"aboutMe"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create new profile
	var user models.User
	if err := h.db.First(&user, "user_id = ?", userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	newProfile := models.Profile{
		UserUUID:       userID,
		UserId:         user.ID,
		FirstName:      input.FirstName,
		LastName:       input.LastName,
		Email:          input.Email,
		University:     input.University,
		Programme:      input.Programme,
		GraduationYear: input.GraduationYear,
		GitHubLink:     input.GitHubLink,
		LinkedInLink:   input.LinkedInLink,
		AboutMe:        input.AboutMe,
	}

	if err := h.db.Create(&newProfile).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Add member to Mailchimp
	if err := h.mailchimp.SubscribeMember(&newProfile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newProfile)
}

// GetProfile returns a profile by user ID (requires authentication)
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userId := c.Param("userId")
	userUUID, err := uuid.Parse(userId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	c.JSON(http.StatusOK, profile)
}

// ListAllProfiles returns all profiles (admin only)
func (h *ProfileHandler) ListAllProfiles(c *gin.Context) {
	var profiles []models.Profile
	if err := h.db.Find(&profiles).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, profiles)
}

// UpdateProfile allows an admin to update any profile
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userId := c.Param("userId")
	userUUID, err := uuid.Parse(userId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var existingProfile models.Profile
	result := h.db.Where("user_uuid = ?", userUUID).First(&existingProfile)

	// Parse input
	var input struct {
		FirstName      string              `json:"firstName" binding:"required"`
		LastName       string              `json:"lastName" binding:"required"`
		Email          string              `json:"email" binding:"required,email"`
		University     string              `json:"university"`
		Programme      models.StudyProgram `json:"programme"`
		GraduationYear int                 `json:"graduationYear"`
		GitHubLink     string              `json:"githubLink"`
		LinkedInLink   string              `json:"linkedinLink"`
		AboutMe        string              `json:"aboutMe"`
	}

	// Update profile fields
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if result.Error == nil {
		existingProfile.FirstName = input.FirstName
		existingProfile.LastName = input.LastName
		existingProfile.Email = input.Email
		existingProfile.University = input.University
		existingProfile.Programme = input.Programme
		existingProfile.GraduationYear = input.GraduationYear
		existingProfile.GitHubLink = input.GitHubLink
		existingProfile.LinkedInLink = input.LinkedInLink
		existingProfile.AboutMe = input.AboutMe

		if err := h.db.Save(&existingProfile).Error; err != nil {
			log.Printf("Exist profile error")
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Update member in Mailchimp
		memberRequest := mailchimp.MemberRequest{
			Email:  existingProfile.Email,
			Status: mailchimp.Subscribed,
			MergeFields: mailchimp.MergeFields{
				FirstName:      existingProfile.FirstName,
				LastName:       existingProfile.LastName,
				Programme:      string(existingProfile.Programme),
				GraduationYear: existingProfile.GraduationYear,
			},
		}
		if _, err := h.mailchimp.UpdateMember(&existingProfile.Email, &memberRequest); err != nil {
			log.Printf("Mailchimp Update Error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, existingProfile)
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "User not exist"})
}

// UploadProfilePicture handles profile picture uploads for the authenticated user
func (h *ProfileHandler) UploadProfilePicture(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	userID, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	file, err := c.FormFile("picture")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Picture file is required"})
		return
	}

	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open file"})
		return
	}
	defer f.Close()

	fdata := make([]byte, file.Size)
	if _, err := f.Read(fdata); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}

	parts := strings.Split(file.Filename, ".")
	if len(parts) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file name"})
		return
	}
	name := strings.Join(parts[:len(parts)-1], ".")
	ftype := parts[len(parts)-1]

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage unavailable"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found. Create your profile first."})
		return
	}

	// Delete old picture if one exists
	if profile.ProfilePicture != "" {
		oldUUID, parseErr := uuid.Parse(profile.ProfilePicture)
		if parseErr == nil {
			var oldBlob models.BlobData
			if h.db.First(&oldBlob, "blob_id = ?", oldUUID).Error == nil {
				_ = oldBlob.DeleteData(&oldUUID, h.db, r2)
			}
		}
	}

	blob, err := models.NewBlobData(name, ftype, userID, fdata, h.db, r2)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload picture"})
		return
	}

	profile.ProfilePicture = blob.BlobId.String()
	if err := h.db.Save(&profile).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Profile picture updated",
		"profilePicture": profile.ProfilePicture,
	})
}

// GetProfilePicture serves a profile picture by blob UUID
func (h *ProfileHandler) GetProfilePicture(c *gin.Context) {
	pictureID := c.Query("id")
	picUUID, err := uuid.Parse(pictureID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid picture ID"})
		return
	}

	var blob models.BlobData
	if err := h.db.First(&blob, "blob_id = ?", picUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Picture not found"})
		return
	}

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage unavailable"})
		return
	}

	data, err := blob.GetData(r2)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch picture"})
		return
	}

	contentType := "image/jpeg"
	switch strings.ToLower(blob.FType) {
	case "png":
		contentType = "image/png"
	case "gif":
		contentType = "image/gif"
	case "webp":
		contentType = "image/webp"
	}

	c.Data(http.StatusOK, contentType, data)
}

// GetPublicProfile returns a public-safe profile by profile UUID (no auth required)
func (h *ProfileHandler) GetPublicProfile(c *gin.Context) {
	profileID := c.Param("profileId")

	// Accept either the profile UUID (profiles.id) or the user UUID (profiles.user_uuid)
	var profile models.Profile
	if err := h.db.Where("id = ? OR user_uuid = ?", profileID, profileID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	// Fetch team history
	type teamRow struct {
		ID           uint   `gorm:"column:id" json:"id"`
		Role         string `gorm:"column:role" json:"role"`
		Department   string `gorm:"column:department" json:"department"`
		AcademicYear string `gorm:"column:academic_year" json:"academicYear"`
	}
	var teamHistory []teamRow
	h.db.Table("team_members").
		Select("id, team_member_role AS role, team_member_department AS department, academic_year").
		Where("user_id = ? AND deleted_at IS NULL AND academic_year IS NOT NULL AND academic_year != ''", profile.UserId).
		Order("academic_year DESC").
		Scan(&teamHistory)

	// Fetch contributed projects
	type projectRow struct {
		ProjectID          string `gorm:"column:project_id" json:"id"`
		ProjectName        string `gorm:"column:project_name" json:"title"`
		OneLineDescription string `gorm:"column:one_line_description" json:"oneLineDescription"`
		Status             string `gorm:"column:status" json:"status"`
		CoverImage         string `gorm:"column:cover_image" json:"coverImage"`
	}
	var projects []projectRow
	h.db.Table("team_members").
		Select("DISTINCT projects.project_id, projects.project_name, projects.one_line_description, projects.status, '' AS cover_image").
		Joins("JOIN team_member_pairs ON team_member_pairs.team_member_id = team_members.id").
		Joins("JOIN team_project_pairs ON team_project_pairs.team_id = team_member_pairs.team_id").
		Joins("JOIN projects ON projects.id = team_project_pairs.project_id").
		Where("team_members.user_id = ? AND team_members.deleted_at IS NULL AND projects.deleted_at IS NULL", profile.UserId).
		Scan(&projects)

	c.JSON(http.StatusOK, gin.H{
		"id":             profile.Id,
		"firstName":      profile.FirstName,
		"lastName":       profile.LastName,
		"profilePicture": profile.ProfilePicture,
		"university":     profile.University,
		"programme":      profile.Programme,
		"graduationYear": profile.GraduationYear,
		"githubLink":     profile.GitHubLink,
		"linkedinLink":   profile.LinkedInLink,
		"aboutMe":        profile.AboutMe,
		"teamHistory":    teamHistory,
		"projects":       projects,
	})
}

// DeleteProfile allows an admin to delete a profile
func (h *ProfileHandler) DeleteProfile(c *gin.Context) {
	userId := c.Param("userId")
	userUUID, err := uuid.Parse(userId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Find profile first to check if it exists
	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userUUID).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	if err := h.db.Delete(&profile).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Profile deleted successfully"})
}
