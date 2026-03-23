package middleware

import (
	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func AuthRequiredJWT(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, cookie := range c.Request.Cookies() {
			if cookie.Name == "jwt" {
				valid, _ := utils.ParseAndVerify(cookie.Value, cfg.JwtSigningKey)
				if valid {
					c.Next()
					return
				} else {
					c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
					c.Abort()
				}
			}
		}
		// no jwt, not authorized
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		c.Abort()
	}
}

func RoleRequired(cfg *config.Config, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		abort := func() {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
		}
		for _, cookie := range c.Request.Cookies() {
			if cookie.Name == "jwt" {
				valid, token := utils.ParseAndVerify(cookie.Value, cfg.JwtSigningKey)
				if !valid {
					log.Printf("JWT Token not Valid!\n")
					abort()
				}
				roles := strings.Split(utils.GetClaims(token)["roles"].(string), ",")
				if slices.Contains(roles, role) {
					c.Next()
					return
				} else {
					abort()
				}
			}
		}
		abort()
	}
}

func RegisteredUserRequired(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")

		// Check if profile exists
		var existingProfile models.Profile
		result := db.Where("user_id = ?", userID).First(&existingProfile)
		if result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
			c.Abort()
			return
		}
		c.Next()
	}
}
