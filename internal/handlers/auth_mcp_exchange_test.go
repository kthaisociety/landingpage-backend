package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestExchangeMCPToken covers the landingpage-mcp token-exchange endpoint
// against a real Postgres connection, like TestApplicationAndInterviewLifecycle:
// skips when no .env is present rather than failing in environments without
// local infra.
func TestExchangeMCPToken(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.MCPServiceSecret = "test-mcp-service-secret"

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.User{}))

	suffix := uuid.New().String()[:8]
	knownEmail := fmt.Sprintf("mcp-exchange-%s@seed.local", suffix)
	unknownEmail := fmt.Sprintf("mcp-exchange-unknown-%s@seed.local", suffix)

	require.NoError(t, db.Create(&models.User{
		UserId:   uuid.New(),
		Email:    knownEmail,
		Provider: "google",
		Roles:    pq.StringArray{"user", "member", "admin"},
	}).Error)
	t.Cleanup(func() {
		db.Where("email = ?", knownEmail).Unscoped().Delete(&models.User{})
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewAuthHandler(db, nil, cfg).Register(api)

	exchange := func(email, secret string) *httptest.ResponseRecorder {
		var body *strings.Reader
		if email == "" {
			body = strings.NewReader(`{}`)
		} else {
			payload, err := json.Marshal(map[string]string{"email": email})
			require.NoError(t, err)
			body = strings.NewReader(string(payload))
		}
		req := httptest.NewRequest("POST", "/api/v1/auth/mcp-exchange", body)
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("X-Service-Secret", secret)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("known email with correct secret returns a valid JWT with the user's roles", func(t *testing.T) {
		rec := exchange(knownEmail, cfg.MCPServiceSecret)
		require.Equal(t, http.StatusOK, rec.Code)

		var respBody struct {
			JWT string `json:"jwt"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody))
		require.NotEmpty(t, respBody.JWT)

		valid, token := utils.ParseAndVerify(respBody.JWT, cfg.JwtValidatingKey)
		require.True(t, valid)
		claims := utils.GetClaims(token)
		require.Equal(t, knownEmail, claims["email"])
		require.Equal(t, "user,member,admin", claims["roles"])
	})

	t.Run("wrong secret is rejected", func(t *testing.T) {
		rec := exchange(knownEmail, "not-the-right-secret")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("missing secret header is rejected", func(t *testing.T) {
		rec := exchange(knownEmail, "")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("unknown email is rejected even with the correct secret", func(t *testing.T) {
		rec := exchange(unknownEmail, cfg.MCPServiceSecret)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}
