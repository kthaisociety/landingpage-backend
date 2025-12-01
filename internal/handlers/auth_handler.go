package handlers

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"backend/internal/mailchimp"
	"backend/internal/models"
	"backend/internal/utils"

	"backend/internal/config"
	"backend/internal/middleware"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/google"
	"gorm.io/gorm"
)

// Add this line to ensure AuthHandler implements Handler interface
type AuthHandler struct {
	db            *gorm.DB
	mailchimp     *mailchimp.MailchimpAPI
	jwtSigningKey string
}

func NewAuthHandler(db *gorm.DB, mailchimp *mailchimp.MailchimpAPI, skey string) *AuthHandler {
	return &AuthHandler{db: db, mailchimp: mailchimp, jwtSigningKey: skey}
}

// Update Register method to match the Handler interface
func (h *AuthHandler) Register(r *gin.RouterGroup) {
	auth := r.Group("/auth")
	{
		// Apply rate limiting to OAuth routes
		oauth := auth.Group("/")
		oauth.Use(middleware.RateLimit())
		{
			oauth.GET("/google", h.BeginGoogleAuth)
			oauth.GET("/google/callback", h.GoogleCallback)
		}

		// Keep only these essential routes
		auth.GET("/status", h.Status)
		auth.GET("/refresh_token", h.RefreshToken)
		auth.GET("/logout", h.Logout)
	}
}

// Add this helper function at the package level
func isOriginAllowed(origin, allowedOrigin string) bool {
	// If allowedOrigin contains a wildcard
	if strings.Contains(allowedOrigin, "*") {
		// Convert the wildcard pattern to a regex pattern
		// Escape special regex characters and convert * to .*
		pattern := "^" + strings.Replace(
			regexp.QuoteMeta(allowedOrigin),
			"\\*",
			".*",
			-1,
		) + "$"

		matched, err := regexp.MatchString(pattern, origin)
		if err != nil {
			log.Printf("Error matching origin pattern: %v", err)
			return false
		}
		return matched
	}

	// Exact match if no wildcard
	return origin == allowedOrigin
}

// viv - usually a separate refresh token is used but I don't know why that is necessary
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	old_token := utils.GetJWT(c)
	claims := utils.GetClaims(old_token)
	userId, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		log.Printf("Refresh failed\n")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse user id"})
		return
	}
	var user models.User
	result := h.db.Where("user_id = ?", userId).First(&user)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not retreive user info"})
	}
	newToken := utils.WriteJWT(user.Email, user.Roles, user.ID, h.jwtSigningKey, 15)
	c.SetCookie("jwt", newToken, 3600, "/", "localhost:3000", false, false)
}

func InitAuth(cfg *config.Config) error {
	clientID := cfg.OAuth.GoogleClientID
	clientSecret := cfg.OAuth.GoogleClientSecret

	fmt.Printf("InitAuth - Client ID length: %d\n", len(clientID))
	fmt.Printf("InitAuth - Client Secret length: %d\n", len(clientSecret))

	goth.UseProviders(
		google.New(
			clientID,
			clientSecret,
			cfg.BackendURL+"/api/v1/auth/google/callback",
			"email",   // Minimal scope
			"profile", // For user info
			"openid",  // Enable OpenID Connect
			"https://www.googleapis.com/auth/userinfo.profile", // Explicit profile access
		),
	)

	// Configure provider options
	provider, err := goth.GetProvider("google")
	if err != nil {
		return fmt.Errorf("failed to get google provider: %v", err)
	}

	if googleProvider, ok := provider.(*google.Provider); ok {
		googleProvider.SetHostedDomain("")                 // Optional: restrict to specific domain
		googleProvider.SetPrompt("select_account consent") // Force consent screen
	}

	return nil
}

func (h *AuthHandler) Status(c *gin.Context) {
	token_str := utils.GetJWTString(c)
	valid, _ := utils.ParseAndVerify(token_str, h.jwtSigningKey)
	if !valid {
		c.JSON(401, gin.H{"authenticate": false})
	} else {
		c.JSON(200, gin.H{"authenticate": true})
	}

}

func (h *AuthHandler) BeginGoogleAuth(c *gin.Context) {
	provider, err := goth.GetProvider("google")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get provider"})
		return
	}

	// Get the origin from the request header
	origin := c.GetHeader("Origin")

	// If Origin header is missing, use the Host header or a default value
	if origin == "" {
		host := c.Request.Host
		// Determine scheme (http/https)
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		origin = fmt.Sprintf("%s://%s", scheme, host)
		log.Printf("Origin header missing, using: %s", origin)
	}

	// Validate that the origin is in the allowed list
	cfg, err := config.LoadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load config"})
		return
	}

	// Check if origin is allowed using the new helper function
	isAllowed := false
	for _, allowed := range cfg.AllowedOrigins {
		if isOriginAllowed(origin, allowed) {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "Origin not allowed"})
		return
	}

	// Generate a secure state that includes the origin
	state := fmt.Sprintf("%s|%s", uuid.New().String(), origin)

	// Store the state in the session
	session := sessions.Default(c)
	session.Set("oauth_state", state)
	if err := session.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save session"})
		return
	}

	authURL, err := provider.BeginAuth(state)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin auth"})
		return
	}

	url, err := authURL.GetAuthURL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get auth URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": url})
}

func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	provider, err := goth.GetProvider("google")
	if err != nil {
		log.Printf("Failed to get provider: %v", err)
		redirectWithError(c, "Authentication failed")
		return
	}

	// Get the state from the query parameters
	params := c.Request.URL.Query()
	receivedState := params.Get("state")

	// Retrieve the stored state from the session
	session := sessions.Default(c)
	expectedState := session.Get("oauth_state")

	// Clear the state from the session immediately
	session.Delete("oauth_state")
	session.Save()

	// Verify the state matches
	if expectedState == nil || receivedState != expectedState.(string) {
		log.Printf("State mismatch: expected %v, got %v", expectedState, receivedState)
		redirectWithError(c, "Invalid authentication state")
		return
	}

	// Extract the frontend URL from the state
	stateParts := strings.Split(receivedState, "|")
	if len(stateParts) != 2 {
		log.Printf("Invalid state format")
		redirectWithError(c, "Invalid authentication state")
		return
	}
	frontendURL := stateParts[1]

	gothSession, err := provider.BeginAuth(receivedState)
	if err != nil {
		log.Printf("Failed to begin auth: %v", err)
		redirectWithError(c, fmt.Sprintf("Failed to authorize: %v", err))
		return
	}

	_, err = gothSession.Authorize(provider, params)
	if err != nil {
		log.Printf("Failed to authorize: %v", err)
		redirectWithError(c, fmt.Sprintf("Failed to authorize: %v", err))
		return
	}
	gSession := gothSession.(*google.Session)
	// parse token here
	valid, token := utils.ParseAndVerifyGoogle(gSession.IDToken)
	if token == nil {
		log.Printf("Error parsing google jwt: %v\n", gSession.IDToken)
	}
	if !valid {
		log.Printf("Invalid Google Token\n")
	}
	token_data := utils.GetClaims(token)
	// log.Printf("Google ID: %v\n", gSession.IDToken)
	// log.Printf("Google Access: %v\n", gSession.AccessToken)
	// Extract name from RawData
	var firstName, lastName, email, name string
	if given, ok := token_data["given_name"].(string); ok {
		firstName = given
	}
	if family, ok := token_data["family_name"].(string); ok {
		lastName = family
	}
	if emejl, ok := token_data["email"].(string); ok {
		email = emejl
	}
	if fullname, ok := token_data["name"].(string); ok {
		name = fullname
	}
	if firstName == "" || lastName == "" && name != "" {
		names := strings.Split(name, " ")
		if len(names) >= 2 {
			if firstName == "" {
				firstName = names[0]
			}
			if lastName == "" {
				lastName = strings.Join(names[1:], " ")
			}
		} else if len(names) == 1 {
			if firstName == "" {
				firstName = names[0]
			}
		}
	}
	// Check if user exists
	var user models.User
	result := h.db.Where("email = ?", email).First(&user)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new user
		user = models.User{
			Email:     email,
			Provider:  "google",
			Roles:     []string{"user"},
			ID:        uuid.New(),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		if err := h.db.Create(&user).Error; err != nil {
			log.Printf("Failed to create user: %v", err)
			redirectWithError(c, "Failed to create account")
			return
		}
	} else if result.Error != nil {
		// Database error (not "record not found")
		log.Printf("Database error: %v", result.Error)
		redirectWithError(c, "Database error")
		return
	}
	// If no error, user already exists and was loaded successfully

	// Check if profile exists
	var profile models.Profile
	profileExists := h.db.Where("user_id = ?", user.ID).First(&profile).Error == nil
	if !profileExists {
		profile.UserID = user.ID
		profile.Email = user.Email
		profile.FirstName = firstName
		profile.LastName = lastName
		profile.Registered = false
		if err := h.db.Create(&profile).Error; err != nil {
			log.Printf("Failed to create profile for user: %v\n", profile)
		}
	}

	// Set session for the user
	session = sessions.Default(c)
	session.Clear()
	session.Set("user_id", user.ID)
	session.Set("authenticated", true)

	if err := session.Save(); err != nil {
		log.Printf("Failed to save session: %v", err)
		redirectWithError(c, "Failed to create session")
		return
	}

	// Redirect based on whether profile exists
	var dashboardURL string
	if profileExists && profile.Registered {
		// Profile exists, redirect to dashboard
		dashboardURL = fmt.Sprintf("%s/dashboard?auth=success", frontendURL)
	} else {
		// Profile doesn't exist, redirect to complete registration
		dashboardURL = fmt.Sprintf("%s/auth/complete-registration?fname=%s&lname=%s", frontendURL, firstName, lastName)
	}
	// create JWT token with user data
	// var roles []string
	// if user.IsAdmin {
	// 	roles = []string{"user", "admin"}
	// } else {
	// 	roles = []string{"user"}
	// }
	authJwt := utils.WriteJWT(email, user.Roles, user.ID, h.jwtSigningKey, 15)
	c.SetCookie("jwt", authJwt, 3600, "/", "localhost:3000", false, false)
	c.Redirect(http.StatusTemporaryRedirect, dashboardURL)
}

// Update the redirectWithError function to use the frontend URL from state
func redirectWithError(c *gin.Context, message string) {
	// Get the state from the query parameters
	params := c.Request.URL.Query()
	receivedState := params.Get("state")

	// Extract the frontend URL from the state
	stateParts := strings.Split(receivedState, "|")
	if len(stateParts) != 2 {
		log.Printf("Invalid state format in error redirect")
		return
	}
	frontendURL := stateParts[1]

	// URL encode the error message
	encodedError := url.QueryEscape(message)
	redirectURL := fmt.Sprintf("%s/auth/login?error=%s", frontendURL, encodedError)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	session.Save()
	c.JSON(http.StatusOK, gin.H{"message": "Successfully logged out"})
}
