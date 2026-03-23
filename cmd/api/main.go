package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/handlers"
	"backend/internal/mailchimp"
	"backend/internal/models"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func generateSecureKey() ([]byte, error) {
	key := make([]byte, 32) // 256 bits
	_, err := rand.Read(key)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func setupStore() (sessions.Store, error) {
	// Generate a secure key or load from environment
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}
	key := cfg.SessionKey
	var sessionKey []byte

	if key == "" {
		var err error
		sessionKey, err = generateSecureKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate session key: %v", err)
		}
		// Optionally log the key for first-time setup
		log.Printf("Generated new session key: %s", base64.StdEncoding.EncodeToString(sessionKey))
	} else {
		var err error
		sessionKey, err = base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("invalid session key in environment: %v", err)
		}
	}

	// Create store with secure settings
	store := cookie.NewStore(sessionKey)
	store.Options(sessions.Options{
		Path:     "/",       // Cookie is valid for entire site
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,      // Prevent JavaScript access
		Secure:   true,      // Require HTTPS
		SameSite: http.SameSiteStrictMode,
	})

	return store, nil
}

func main() {
	// Get working directory
	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: Could not get working directory: %v", err)
	} else {
		log.Printf("Working directory: %s", wd)
	}

	// Try loading environment variables from multiple locations
	envFiles := []string{
		".env",
		".env.local",
		"../../.env",
		"../../.env.local",
	}

	for _, file := range envFiles {
		if err := godotenv.Load(file); err == nil {
			log.Printf("Successfully loaded environment from: %s", file)
			break
		} else {
			log.Printf("Could not load %s: %v", file, err)
		}
	}

	// Load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// Initialize DB
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto migrate the schema
	err = db.AutoMigrate(
		&models.User{},
		&models.Profile{},
		&models.Event{},
		&models.Registration{},
		&models.TeamMember{},
		&models.BlobData{},
		&models.JobListing{},
		&models.Company{},
		&models.Project{},
		&models.Team{},
		&models.TeamProjectPair{},
		&models.TeamUserPair{},
	)
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	// Initialize auth
	if err := handlers.InitAuth(cfg); err != nil {
		log.Printf("Warning: OAuth initialization failed: %v", err)
	}

	// Initialize router
	r := gin.Default()

	// Add CORS middleware with configurable origins
	corsConfig := cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "Cookie"},
		ExposeHeaders:    []string{"Set-Cookie"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

	// Setup session store with development-friendly settings
	store, err := setupStore()
	if err != nil {
		log.Fatal("Failed to setup session store:", err)
	}

	// Modify cookie settings for development
	if cfg.DevelopmentMode {
		store.Options(sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7,
			HttpOnly: true,
			Secure:   false,                // Set to false for development
			SameSite: http.SameSiteLaxMode, // Use Lax for development
		})
	}

	r.Use(sessions.Sessions("kthais_session", store))

	// Initialize SES
	if err := email.InitEmailService(cfg); err != nil {
		log.Fatal("Failed to initialize SES:", err)
	}

	// Initialize mailchimp client
	mailchimpApi, err := mailchimp.InitMailchimpApi(cfg)
	if err != nil {
		log.Fatal("Failed to initialize mailchimp client:", err)
	}

	// Initialize handlers
	setupRoutes(r, db, mailchimpApi, cfg)

	// Run the server
	r.Run(":" + cfg.Server.Port)
}

func setupRoutes(r *gin.Engine, db *gorm.DB, mailchimpApi *mailchimp.MailchimpAPI, cfg *config.Config) {
	api := r.Group("/api/v1")

	// Public routes
	api.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Register all handlers
	allHandlers := []handlers.Handler{
		handlers.NewAuthHandler(db, mailchimpApi, cfg.JwtSigningKey),
		handlers.NewProfileHandler(db, mailchimpApi, cfg),
		handlers.NewAdminHandler(db, cfg),
		handlers.NewCompanyHandler(db, cfg),
		handlers.NewJobListingHandler(db, cfg),
		handlers.NewProjectHandler(db, cfg),
	}

	for _, h := range allHandlers {
		h.Register(api)
	}

	// Add alias routes for frontend compatibility
	jobHandler := handlers.NewJobListingHandler(db, cfg)
	api.GET("/jobs", jobHandler.GetAllListings)
	// Handle /jobs/:id by converting path param to query param
	api.GET("/jobs/:id", func(c *gin.Context) {
		id := c.Param("id")
		c.Request.URL.RawQuery = "id=" + id
		jobHandler.GetJobListing(c)
	})

}
