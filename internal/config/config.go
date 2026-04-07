package config

import (
	"log"
	"os"
	"strings"
)

type Config struct {
	Database struct {
		Host     string
		Port     string
		User     string
		Password string
		DBName   string
		SSLMode  string
	}
	Server struct {
		Port string
	}
	OAuth struct {
		GoogleClientID     string
		GoogleClientSecret string
	}
	AllowedOrigins []string
	BackendURL     string
	Redis          struct {
		Host     string
		Port     string
		Password string
	}
	SessionKey      string
	DevelopmentMode bool

	Mailchimp struct {
		APIKey string
		User   string
		ListID string
	}
	JwtSigningKey    string
	JwtValidatingKey string
	R2_bucket_name   string
	R2_access_key    string
	R2_access_key_id string
	R2_endpoint      string
	R2_Account_Id    string // might not be needed

	SES struct {
		Region  string
		Sender  string
		ReplyTo string
	}
}

func LoadConfig() (*Config, error) {
	cfg := &Config{}

	// Database config
	cfg.Database.Host = getEnv("DB_HOST", "localhost")
	cfg.Database.Port = getEnv("DB_PORT", "5432")
	cfg.Database.User = getEnv("DB_USER", "postgres")
	cfg.Database.Password = getEnv("DB_PASSWORD", "password")
	cfg.Database.DBName = getEnv("DB_NAME", "kthais")
	cfg.Database.SSLMode = getEnv("DB_SSLMODE", "disable")
	cfg.Server.Port = getEnv("SERVER_PORT", "8080")

	// Redis config
	cfg.Redis.Host = getEnv("REDIS_HOST", "localhost")
	cfg.Redis.Port = getEnv("REDIS_PORT", "6379")
	cfg.Redis.Password = getEnv("REDIS_PASSWORD", "")

	// Load allowed origins from environment variable
	// Format: comma-separated list of origins
	allowedOriginsStr := getEnv("ALLOWED_ORIGINS", "http://localhost:3000")
	cfg.AllowedOrigins = strings.Split(allowedOriginsStr, ",")

	// Trim spaces from each origin
	for i, origin := range cfg.AllowedOrigins {
		cfg.AllowedOrigins[i] = strings.TrimSpace(origin)
	}

	cfg.BackendURL = getEnv("BACKEND_URL", "http://localhost:8080")

	// Mailchimp config
	cfg.Mailchimp.APIKey = getEnv("MAILCHIMP_API_KEY", "")
	cfg.Mailchimp.User = getEnv("MAILCHIMP_USER", "")
	cfg.Mailchimp.ListID = getEnv("MAILCHIMP_LIST_ID", "")

	// OAuth config
	cfg.OAuth.GoogleClientID = getEnv("GOOGLE_CLIENT_ID", "")
	cfg.OAuth.GoogleClientSecret = getEnv("GOOGLE_CLIENT_SECRET", "")

	cfg.SessionKey = getEnv("SESSION_KEY", "")
	cfg.DevelopmentMode = getEnv("DEVELOPMENT", "true") == "true"

	// Asymetric key (priate/public) is used for jwt
	cfg.JwtSigningKey = getEnv("JWTSigningKey", "test123456")
	cfg.JwtValidatingKey = getEnv("JWTValidatingKey", "test123456")

	//Cloudflare R2
	cfg.R2_bucket_name = getEnv("R2_Bucket", "")
	cfg.R2_access_key = getEnv("R2_Secret_Access_Key", "off key scraper")
	cfg.R2_access_key_id = getEnv("R2_Access_Key_Id", "")
	cfg.R2_endpoint = getEnv("R2_Endpoint", "")
	cfg.R2_Account_Id = getEnv("R2_Account_Id", "")

	// Debug OAuth configuration
	// fmt.Printf("Google Client ID: %s\n", maskString(cfg.OAuth.GoogleClientID))
	// fmt.Printf("Google Client Secret: %s\n", maskString(cfg.OAuth.GoogleClientSecret))

	// Log OAuth configuration status
	if cfg.OAuth.GoogleClientID == "" || cfg.OAuth.GoogleClientSecret == "" {
		log.Fatalf("Warning: Google OAuth credentials not configured. OAuth functionality will be disabled.")
		os.Exit(1)
	}

	// Amazon SES config
	cfg.SES.Region = getEnv("SES_REGION", "")
	cfg.SES.Sender = getEnv("SES_SENDER", "")
	cfg.SES.ReplyTo = getEnv("SES_REPLY_TO", cfg.SES.Sender)

	if cfg.SES.Sender == "" {
		log.Println("Warning: Sender email is not set.")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// maskString returns a masked version of the string for secure logging
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
