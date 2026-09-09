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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestOffboardingHandler needs a real Postgres connection (requireHeadOfIT
// queries Profile), so it follows the same "skip if no .env" convention as
// TestApplicationAndInterviewLifecycle and
// TestAdminUpdateSettingsPreservesRecruitmentOpensAtWhenOmitted — standalone
// rather than folded into either, so it isn't affected by the former's own
// unrelated failure.
func TestOffboardingHandler(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.Profile{}))

	regularAdmin := mustCreateAdmin(t, db, cfg, "offboarding-regular-admin@example.com")
	headOfIT := mustCreateHeadOfIT(t, db, cfg, "offboarding-head-of-it@example.com")
	otherAdmin := mustCreateAdmin(t, db, cfg, "offboarding-other-admin@example.com")
	t.Cleanup(func() {
		for _, email := range []string{regularAdmin.email, headOfIT.email, otherAdmin.email} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(fakeOnboardingService.Close)
	cfg.OnboardingServiceURL = fakeOnboardingService.URL
	cfg.OnboardingServiceSecret = "test-onboarding-service-secret"

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewOffboardingHandler(db, cfg).Register(engine.Group("/api/v1"))

	post := func(t *testing.T, path string, body map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", path, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("deactivate", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "no cookie at all")

		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, regularAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "an admin who isn't head of IT")

		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kth.se"}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "not a @kthais.com address")

		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("delete requires the exact confirm phrase", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "missing confirm phrase")

		rec = post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": "test.user@kthais.com", "confirm": "delete this account",
		}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "wrong case is not a match")

		rec = post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": "test.user@kthais.com", "confirm": "DELETE THIS ACCOUNT",
		}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("delete is also head-of-IT-only", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": "test.user@kthais.com", "confirm": "DELETE THIS ACCOUNT",
		}, regularAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("fails loudly, not silently, when onboarding service isn't configured", func(t *testing.T) {
		unconfigured := *cfg
		unconfigured.OnboardingServiceURL = ""
		gin.SetMode(gin.TestMode)
		unconfiguredEngine := gin.New()
		NewOffboardingHandler(db, &unconfigured).Register(unconfiguredEngine.Group("/api/v1"))

		payload, err := json.Marshal(map[string]any{"email": "test.user@kthais.com"})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/admin/offboarding/deactivate", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(headOfIT.cookie)
		rec := httptest.NewRecorder()
		unconfiguredEngine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("transfer head of IT", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": otherAdmin.email}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "no cookie at all")

		rec = post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": otherAdmin.email}, regularAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "only the current head of IT can transfer it")

		rec = post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": headOfIT.email}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "can't transfer to yourself")

		rec = post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": "nobody@example.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusNotFound, rec.Code, "target must exist")

		nonAdminEmail := "offboarding-non-admin@example.com"
		nonAdminUserID := uuid.New()
		require.NoError(t, db.Create(&models.User{
			UserId:   nonAdminUserID,
			Email:    nonAdminEmail,
			Provider: "test",
			Roles:    pq.StringArray{"user", "member"},
		}).Error)
		require.NoError(t, db.Create(&models.Profile{
			UserUUID:  nonAdminUserID,
			UserId:    mustFindUserPK(t, db, nonAdminEmail),
			Email:     nonAdminEmail,
			FirstName: "Non",
			LastName:  "Admin",
		}).Error)
		t.Cleanup(func() {
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.User{})
		})
		rec = post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": nonAdminEmail}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "target must already be an admin")

		rec = post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": otherAdmin.email}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		// The old head lost it...
		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "the old head of IT no longer has access")

		// ...and the new head has it, atomically (never zero heads in between).
		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, otherAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the new head of IT now has access")
	})
}

// mustCreateHeadOfIT creates an admin and flips Profile.IsHeadOfIT directly
// in the database — the only way to grant it outside of
// OffboardingHandler.TransferHeadOfIT itself, matching how it'd actually be
// bootstrapped in production.
func mustCreateHeadOfIT(t *testing.T, db *gorm.DB, cfg *config.Config, email string) testAdmin {
	t.Helper()
	admin := mustCreateAdmin(t, db, cfg, email)
	require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", email).Update("is_head_of_it", true).Error)
	return admin
}
