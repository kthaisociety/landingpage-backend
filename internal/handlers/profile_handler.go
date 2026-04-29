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
	{
		// Auth required endpoints
		// changed the endpoints as there was a url parsing problem in frontend. (me, update, create) are added.
		profile.Use(middleware.AuthRequiredJWT(h.cfg))
		profile.GET("/me", h.GetMyProfile)
		profile.PUT("/update", h.UpdateMyProfile)
		profile.POST("/create", h.CreateMyProfile)

		// Admin-only endpoints
		admin := profile.Group("/admin")
		admin.Use(middleware.RoleRequired(h.cfg, "admin"))
		admin.GET("", h.ListAllProfiles)
		admin.PUT("/:userId", h.UpdateProfile)
		admin.GET("/:userId", h.GetProfile)
		admin.DELETE("/:userId", h.DeleteProfile)
	}
}

// GetMyProfile returns the current user's profile
// func (h *ProfileHandler) GetMyProfile(c *gin.Context) {
// 	// get userId from jwt now
// 	token := utils.GetJWT(c)
// 	claims := utils.GetClaims(token)
// 	userID, err := uuid.Parse(claims["user_id"].(string))
// 	if err != nil {
// 		log.Printf("Could not get userid")
// 	}

// 	var profile models.Profile
// 	if err := h.db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
// 		// If profile doesn't exist, return empty profile
// 		c.JSON(http.StatusOK, gin.H{
// 			"userId": userID,
// 			"exists": false,
// 		})
// 		return
// 	}

// 	c.JSON(http.StatusOK, gin.H{
// 		"userId":         userID,
// 		"exists":         true,
// 		"email":          profile.Email,
// 		"firstName":      profile.FirstName,
// 		"lastName":       profile.LastName,
// 		"university":     profile.University,
// 		"programme":      profile.Programme,
// 		"graduationYear": profile.GraduationYear,
// 		"githubLink":     profile.GitHubLink,
// 		"linkedInLink":   profile.LinkedInLink,
// 	})
// }

// Added Roles to the response JSON

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
		"AboutMe":        profile.AboutMe,
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

	// log.Printf("Profile retrieved: %+v", existingProfile)

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
