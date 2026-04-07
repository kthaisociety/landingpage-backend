package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AdminHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewAdminHandler(db *gorm.DB, cfg *config.Config) *AdminHandler {
	return &AdminHandler{db: db, cfg: cfg}
}

func (h *AdminHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))

	// Auth required endpoints
	admin.GET("/users", h.ListAllUsers)
	admin.GET("/users/uuid", h.GetUUIDByLookup)
	admin.GET("/users/:id", h.GetUserByUUID)
	admin.GET("/listadmins", h.ListAdmins)
	admin.GET("/checkadmin", h.IsAdmin) // Requires auth?

	admin.POST("/users", h.AddUser)
	admin.PUT("/setadmin", h.PromoteToAdmin)
	admin.PUT("/unsetadmin", h.DemoteAdmin)
}

func (h *AdminHandler) IsAdmin(c *gin.Context) {
	retValid := func(isAd bool) {
		c.JSON(http.StatusOK, gin.H{"is_admin": isAd})
	}

	// Implementation for checking if the user is an admin
	for _, cookie := range c.Request.Cookies() {
		if cookie.Name == "jwt" {
			// valid, token := utils.ParseAndVerify(cookie.Value, h.cfg.JwtSigningKey)
			valid, token := utils.ParseAndVerify(cookie.Value, h.cfg.JwtValidatingKey)
			if !valid {
				retValid(false)
				return
			}
			rolesClaim, ok := utils.GetClaims(token)["roles"].(string)
			if !ok {
				retValid(false)
				return
			}

			roles := strings.Split(rolesClaim, ",")
			retValid(slices.Contains(roles, "admin"))
			return
		}
	}
	// Fallback in case of no jwt cookie
	retValid(false)
}

func (h *AdminHandler) ListAdmins(c *gin.Context) {
	var admins []models.User
	if err := h.db.Where("roles @> ARRAY[?]::text[]", "admin").Find(&admins).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve admins"})
		return
	}
	c.JSON(http.StatusOK, admins)
}

func (h *AdminHandler) ListAllUsers(c *gin.Context) {
	var users []models.User
	if err := h.db.Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve users"})
		return
	}
	c.JSON(http.StatusOK, users)
}

func (h *AdminHandler) GetUserByUUID(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid UUID"})
		return
	}

	var user models.User
	if err := h.db.First(&user, "user_id = ?", userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve user"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func (h *AdminHandler) GetUUIDByLookup(c *gin.Context) {
	email := strings.TrimSpace(c.Query("email"))
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide email"})
		return
	}

	var user models.User
	if err := h.db.First(&user, "email = ?", email).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": user.UserId})
}

func (h *AdminHandler) PromoteToAdmin(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Concurrency safety
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&user, "user_id = ?", userID).Error; err != nil {
			return err
		}

		if !slices.Contains(user.Roles, "admin") {
			user.Roles = append(user.Roles, "admin")
			if err := tx.Model(&user).Update("roles", user.Roles).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "Update failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": req.UserID, "status": "success"})
}

func (h *AdminHandler) DemoteAdmin(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"user_id": req.UserID, "error": "Invalid user ID"})
		return
	}

	// Concurrency safety
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&user, "user_id = ?", userID).Error; err != nil {
			return err
		}

		if slices.Contains(user.Roles, "admin") {
			var newRoles pq.StringArray
			for _, r := range user.Roles {
				if r != "admin" {
					newRoles = append(newRoles, r)
				}
			}
			if err := tx.Model(&user).Update("roles", newRoles).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Update failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": req.UserID, "status": "success"})
}

func (h *AdminHandler) AddUser(c *gin.Context) {
	var req struct {
		Email    string   `json:"email"`
		Provider string   `json:"provider"`
		Roles    []string `json:"roles"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email is required"})
		return
	}

	provider := req.Provider
	if provider == "" {
		provider = "magic-link"
	}

	roles := req.Roles
	if len(roles) == 0 {
		roles = []string{"user"}
	}

	user := models.User{
		UserId:   uuid.New(),
		Email:    strings.TrimSpace(req.Email),
		Provider: provider,
		Roles:    roles,
	}

	if err := h.db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create user"})
		return
	}

	c.JSON(http.StatusCreated, user)
}
