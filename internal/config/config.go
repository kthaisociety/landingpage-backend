package config

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultSESRegion = "eu-north-1"
	defaultSESSender = "applications@kthais.com"

	defaultOAuthRateLimitRequests = 5
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
		// Host is the bind address for the API's listener. Empty (the
		// default) binds all interfaces, which production/Docker needs since
		// the container's own network namespace has no other interface to
		// bind. Set to "127.0.0.1" for local dev to keep the dev server off
		// the network entirely.
		Host string
	}
	OAuth struct {
		GoogleClientID     string
		GoogleClientSecret string
		// RateLimitRequests is the per-IP requests-per-minute budget for the
		// OAuth login routes. See middleware.OAuthRateLimit.
		RateLimitRequests int
	}
	AllowedOrigins []string
	BackendURL     string
	FrontendURL    string
	// TrustedProxyHost is the DNS name of the reverse proxy in front of this service
	// (e.g. the Traefik service name in the Dokploy stack). Resolved at startup via the
	// platform's service discovery DNS so c.ClientIP() honors X-Forwarded-For only from
	// that proxy - see the SetTrustedProxies call in cmd/api/main.go.
	TrustedProxyHost string
	Redis            struct {
		Host     string
		Port     string
		Password string
	}
	SessionKey      string
	DevelopmentMode bool
	// DevRoleOverrides maps a lowercased email to the roles that account is
	// given on every Google sign-in, so local devs can see the app as an
	// admin, a member, or a plain user without a separate login bypass.
	// Parsed from DEV_ROLE_OVERRIDES; LoadConfig refuses to start if it's
	// set outside DevelopmentMode.
	DevRoleOverrides map[string][]string
	// CookieDomain is the Domain attribute for JWT cookies. Empty means omit Domain (host-only for the API host).
	// Never include a port; see normalizeCookieDomain.
	CookieDomain    string
	JwtCookieSecure bool

	Mailchimp struct {
		APIKey string
		User   string
		ListID string
	}
	Luma struct {
		APIKey string
		// MembersTierID is the Luma tier new @kthais.com members are added
		// to on onboarding. See internal/luma's AddMemberToTier and
		// onboarding_handler.go's AddToLuma.
		MembersTierID string
	}
	JwtSigningKey    string
	JwtValidatingKey string
	// MCPServiceSecret authenticates the landingpage-mcp service when it calls
	// POST /api/v1/auth/mcp-exchange to trade a verified Google email for a
	// backend JWT. Must match the value configured on that service.
	MCPServiceSecret string
	// OnboardingServiceSecret authenticates calls in both directions between
	// this backend and onboarding-service: it's checked on the backend's own
	// /internal/onboarding/* endpoints (onboarding-service calling in), and
	// sent as X-Service-Secret on this backend's outbound call to
	// onboarding-service's /notify endpoint (this backend calling out).
	// Deliberately separate from MCPServiceSecret so the two services can be
	// rotated/revoked independently.
	OnboardingServiceSecret string
	// OnboardingServiceURL is onboarding-service's base URL, used only for
	// this backend's outbound POST to /notify when an application is
	// accepted. Empty by default — the notify call is skipped (logged, not
	// fatal) until onboarding-service is actually deployed.
	OnboardingServiceURL string
	R2_bucket_name       string
	R2_access_key        string
	R2_access_key_id     string
	R2_endpoint          string
	R2_Account_Id        string // might not be needed

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
	cfg.Server.Host = getEnv("SERVER_HOST", "")

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
	cfg.FrontendURL = getEnv("FRONTEND_URL", "https://kthais.com")

	// Default is an educated guess at Dokploy's Traefik service name, not a confirmed
	// value - verify against the actual Dokploy stack (e.g. `docker service ls`) and
	// override via env if it differs. See cmd/api/main.go for how this is used.
	cfg.TrustedProxyHost = getEnv("TRUSTED_PROXY_HOST", "dokploy-traefik")

	// Mailchimp config
	cfg.Mailchimp.APIKey = getEnv("MAILCHIMP_API_KEY", "")
	cfg.Mailchimp.User = getEnv("MAILCHIMP_USER", "kthais")
	cfg.Mailchimp.ListID = firstNonEmptyEnv("MAILCHIMP_LIST_ID", "MAILCHIMP_AUDIENCE_ID")

	// Luma config
	cfg.Luma.APIKey = getEnv("LUMA_API_KEY", "")
	cfg.Luma.MembersTierID = getEnv("LUMA_MEMBERS_TIER_ID", "")

	// OAuth config
	cfg.OAuth.GoogleClientID = getEnv("GOOGLE_CLIENT_ID", "")
	cfg.OAuth.GoogleClientSecret = getEnv("GOOGLE_CLIENT_SECRET", "")
	cfg.OAuth.RateLimitRequests = getPositiveIntEnv("OAUTH_RATE_LIMIT_REQUESTS", defaultOAuthRateLimitRequests)

	cfg.SessionKey = getEnv("SESSION_KEY", "")
	// Development mode must be set via env var DEVELOPMENT_MODE=true
	// Defaults to false
	cfg.DevelopmentMode = strings.EqualFold(os.Getenv("DEVELOPMENT_MODE"), "true")

	rawOverrides := getEnv("DEV_ROLE_OVERRIDES", "")
	overrides, err := parseDevRoleOverrides(rawOverrides)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rawOverrides) != "" && !cfg.DevelopmentMode {
		return nil, fmt.Errorf("DEV_ROLE_OVERRIDES is set but DEVELOPMENT_MODE is not true; it is for local development only")
	}
	cfg.DevRoleOverrides = overrides

	cfg.CookieDomain = normalizeCookieDomain(getEnv("COOKIE_DOMAIN", ""))
	cfg.JwtCookieSecure = strings.EqualFold(getEnv("SECURE_COOKIE", "false"), "true")

	// RS256 key pair for the member jwt cookie. There is no default:
	// ValidateJWTKeys rejects a missing or malformed pair at startup.
	cfg.JwtSigningKey = strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY"))
	cfg.JwtValidatingKey = strings.TrimSpace(os.Getenv("JWT_PUBLIC_KEY"))

	cfg.MCPServiceSecret = getEnv("MCP_SERVICE_SECRET", "")
	cfg.OnboardingServiceSecret = getEnv("ONBOARDING_SERVICE_SECRET", "")
	cfg.OnboardingServiceURL = getEnv("ONBOARDING_SERVICE_URL", "")

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
	cfg.SES.Region = getEnv("SES_REGION", defaultSESRegion)
	cfg.SES.Sender = getEnv("SES_SENDER", defaultSESSender)
	cfg.SES.ReplyTo = getEnv("SES_REPLY_TO", cfg.SES.Sender)

	return cfg, nil
}

// parseDevRoleOverrides parses "a@x.com=user,member,admin;b@x.com=user"
// into a lowercased-email -> roles map. Unknown roles are rejected so a
// typo fails loudly at startup instead of silently granting nothing.
func parseDevRoleOverrides(raw string) (map[string][]string, error) {
	valid := map[string]bool{"user": true, "member": true, "admin": true}
	overrides := map[string][]string{}
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		email, rolesStr, ok := strings.Cut(entry, "=")
		email = strings.ToLower(strings.TrimSpace(email))
		if !ok || email == "" {
			return nil, fmt.Errorf("DEV_ROLE_OVERRIDES: invalid entry %q, want email=role1,role2", entry)
		}
		var roles []string
		for _, role := range strings.Split(rolesStr, ",") {
			role = strings.ToLower(strings.TrimSpace(role))
			if role == "" {
				continue
			}
			if !valid[role] {
				return nil, fmt.Errorf("DEV_ROLE_OVERRIDES: unknown role %q for %s (want user, member, admin)", role, email)
			}
			roles = append(roles, role)
		}
		if len(roles) == 0 {
			return nil, fmt.Errorf("DEV_ROLE_OVERRIDES: no roles given for %s", email)
		}
		overrides[email] = roles
	}
	if len(overrides) == 0 && strings.TrimSpace(raw) != "" {
		return nil, fmt.Errorf("DEV_ROLE_OVERRIDES is set but has no entries, want email=role1,role2")
	}
	return overrides, nil
}

// ValidateJWTKeys checks that the JWT signing key is a PEM RSA private key,
// the validating key is a PEM RSA public key, and that they form a pair.
// Called at startup so a missing or wrong key fails the deploy instead of
// every sign-in.
func ValidateJWTKeys(cfg *Config) error {
	if cfg.JwtSigningKey == "" {
		return fmt.Errorf("JWT_PRIVATE_KEY is not set")
	}
	if cfg.JwtValidatingKey == "" {
		return fmt.Errorf("JWT_PUBLIC_KEY is not set")
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(cfg.JwtSigningKey))
	if err != nil {
		return fmt.Errorf("JWT_PRIVATE_KEY is not a PEM-encoded RSA private key: %w", err)
	}
	publicKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(cfg.JwtValidatingKey))
	if err != nil {
		return fmt.Errorf("JWT_PUBLIC_KEY is not a PEM-encoded RSA public key: %w", err)
	}
	if !privateKey.PublicKey.Equal(publicKey) {
		return fmt.Errorf("JWT_PUBLIC_KEY does not match JWT_PRIVATE_KEY")
	}
	return nil
}

// normalizeCookieDomain strips an accidental :port suffix. Cookie Domain attributes must not contain ports.
func normalizeCookieDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return host
	}
	return raw
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getPositiveIntEnv falls back to defaultValue (with a log line) when the
// variable is unset, unparseable, or not positive.
func getPositiveIntEnv(key string, defaultValue int) int {
	raw, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(raw) == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		log.Printf("Invalid %s=%q, using default %d", key, raw, defaultValue)
		return defaultValue
	}
	return value
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

// maskString returns a masked version of the string for secure logging
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
