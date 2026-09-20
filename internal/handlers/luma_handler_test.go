package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/luma"
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

// TestLumaHandler covers admin-auth gating and validation. No real Luma API
// key is configured, so the "add-member" call itself is never expected to
// succeed here — see internal/luma's AddMemberToTier for the actual Luma
// HTTP call.
func TestLumaHandler(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{JwtSigningKey: jwtKey, JwtValidatingKey: jwtKey}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewLumaHandler(nil, cfg, nil).Register(api)

	adminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	nonAdminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("member@kthais.com", []string{"user", "member"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	addMember := func(t *testing.T, body map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/admin/luma/add-member", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	validBody := map[string]string{"email": "grace.hopper@kthais.com"}

	t.Run("no cookie is rejected", func(t *testing.T) {
		rec := addMember(t, validBody, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-admin is rejected", func(t *testing.T) {
		rec := addMember(t, validBody, nonAdminCookie(t))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-kthais.com email is rejected", func(t *testing.T) {
		rec := addMember(t, map[string]string{"email": "grace.hopper@example.com"}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing email is rejected", func(t *testing.T) {
		rec := addMember(t, map[string]string{}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("admin with valid email reaches the luma call, which fails since luma isn't configured here", func(t *testing.T) {
		rec := addMember(t, validBody, adminCookie(t))
		require.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

// TestLumaHandlerAddMemberSuccess covers the real HTTP contract against a
// fake Luma server — request URL, x-luma-api-key header, JSON body, and a
// successful response — rather than only the "not configured" failure path
// above.
func TestLumaHandlerAddMemberSuccess(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{JwtSigningKey: jwtKey, JwtValidatingKey: jwtKey}
	cfg.Luma.MembersTierID = "tier-123"

	var gotMethod, gotPath, gotAPIKey string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAPIKey = r.Header.Get("x-luma-api-key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"membership_id":"mem-1","status":"approved"}`))
	}))
	t.Cleanup(server.Close)

	lumaApi := &luma.LumaAPI{
		APIKey:        "test-luma-key",
		MembersTierID: cfg.Luma.MembersTierID,
		BaseURL:       server.URL,
		HTTPClient:    server.Client(),
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewLumaHandler(nil, cfg, lumaApi).Register(api)

	token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
	require.NoError(t, err)
	cookie := &http.Cookie{Name: "jwt", Value: token}

	payload, err := json.Marshal(map[string]string{"email": "grace.hopper@kthais.com"})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/v1/admin/luma/add-member", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/memberships/members/add", gotPath)
	require.Equal(t, "test-luma-key", gotAPIKey)
	require.Equal(t, "grace.hopper@kthais.com", gotBody["email"])
	require.Equal(t, "tier-123", gotBody["membership_tier_id"])
}

// TestLumaHandlerSyncAllExcludesDeactivated guards against SyncAll silently
// undoing a deactivation: a deactivated member (DeactivatedAt set — see
// OffboardingHandler.Deactivate) must never be sent to Luma's add-member
// endpoint by the bulk sync, even though their local User row still exists
// and is otherwise indistinguishable from an active member's.
func TestLumaHandlerSyncAllExcludesDeactivated(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.Luma.MembersTierID = "tier-123"

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.User{}))

	suffix := uuid.New().String()[:8]
	activeEmail := fmt.Sprintf("sync-all-active-%s@kthais.com", suffix)
	deactivatedEmail := fmt.Sprintf("sync-all-deactivated-%s@kthais.com", suffix)
	deactivatedAt := time.Now()

	require.NoError(t, db.Create(&models.User{
		UserId: uuid.New(), Email: activeEmail, Provider: "test", Roles: pq.StringArray{"user", "member"},
	}).Error)
	require.NoError(t, db.Create(&models.User{
		UserId: uuid.New(), Email: deactivatedEmail, Provider: "test", Roles: pq.StringArray{"user", "member"},
		DeactivatedAt: &deactivatedAt,
	}).Error)
	t.Cleanup(func() {
		db.Where("email IN ?", []string{activeEmail, deactivatedEmail}).Unscoped().Delete(&models.User{})
	})

	var addedEmails []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		addedEmails = append(addedEmails, body["email"])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"membership_id":"mem-1","status":"approved"}`))
	}))
	t.Cleanup(server.Close)

	lumaApi := &luma.LumaAPI{
		APIKey:        "test-luma-key",
		MembersTierID: cfg.Luma.MembersTierID,
		BaseURL:       server.URL,
		HTTPClient:    server.Client(),
	}

	jwtKey := generateTestJWTKey(t)
	cfg.JwtSigningKey, cfg.JwtValidatingKey = jwtKey, jwtKey

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewLumaHandler(db, cfg, lumaApi).Register(engine.Group("/api/v1"))

	token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/v1/admin/luma/sync-all", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: token})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, addedEmails, activeEmail, "an active member must still be synced")
	require.NotContains(t, addedEmails, deactivatedEmail, "a deactivated member must never be re-added to Luma")
}
