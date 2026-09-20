package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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
	t.Cleanup(func() {
		for _, email := range []string{regularAdmin.email, headOfIT.email} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	var deleteCallCount int64
	fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/offboarding/delete" {
			atomic.AddInt64(&deleteCallCount, 1)
		}
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

	// Regression guard for LumaHandler.SyncAll silently undoing a
	// deactivation: DeactivatedAt is the only local signal that an account
	// is deactivated, so it must actually get set.
	t.Run("deactivate marks the local user record as deactivated", func(t *testing.T) {
		email := "offboarding-deactivate-target@kthais.com"
		require.NoError(t, db.Create(&models.User{
			UserId: uuid.New(), Email: email, Provider: "test", Roles: pq.StringArray{"user", "member"},
		}).Error)
		t.Cleanup(func() {
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		})

		rec := post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": email}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var user models.User
		require.NoError(t, db.Where("email = ?", email).First(&user).Error)
		require.NotNil(t, user.DeactivatedAt)
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

	// Regression for a real gap Sam found in the running app: Delete only
	// removed the real Google Workspace + Mattermost accounts, never this
	// app's own local User/Profile row, so the member kept showing up in
	// the admin Users list afterward. It now removes both when a local
	// record exists.
	t.Run("delete also removes the local user record", func(t *testing.T) {
		victim := mustCreateAdmin(t, db, cfg, "offboarding-delete-victim@kthais.com")
		t.Cleanup(func() {
			db.Where("email = ?", victim.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", victim.email).Unscoped().Delete(&models.User{})
		})

		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": victim.email, "confirm": "DELETE THIS ACCOUNT",
		}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var stillExists int64
		db.Model(&models.User{}).Where("email = ?", victim.email).Count(&stillExists)
		require.Zero(t, stillExists, "the local user record should be gone after a successful delete")

		var profileStillExists int64
		db.Model(&models.Profile{}).Where("email = ?", victim.email).Count(&profileStillExists)
		require.Zero(t, profileStillExists, "the local profile record should be gone after a successful delete")
	})

	// Regression for a Greptile finding: the local lookup used to query
	// Profile, not User. RegisteredUserRequired shows a User can exist with
	// no matching Profile at all (signed in but never finished profile
	// setup) — looking up by Profile treated that as "no local record,"
	// skipped deleteUserAndProfile entirely, and left the orphaned User row
	// (still visible in the admin Users list) behind.
	t.Run("delete removes a local user even if they never completed their profile", func(t *testing.T) {
		email := "offboarding-delete-no-profile@kthais.com"
		require.NoError(t, db.Create(&models.User{
			UserId:   uuid.New(),
			Email:    email,
			Provider: "google",
			Roles:    pq.StringArray{"user", "member"},
		}).Error)
		t.Cleanup(func() {
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		})

		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": email, "confirm": "DELETE THIS ACCOUNT",
		}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var stillExists int64
		db.Model(&models.User{}).Where("email = ?", email).Count(&stillExists)
		require.Zero(t, stillExists, "the profile-less local user record should be gone after a successful delete")
	})

	// Regression, generalized for the exactly-one/transfer-only board-role
	// model (see Profile.BoardRole's doc comment): deleting an account that
	// currently holds one of the eight exactly-one roles — not just Head of
	// IT — is refused before touching any real account, since there's no
	// way to transfer the role away from a deleted profile. The check
	// itself now lives in deleteUserAndProfile as a single-row lookup
	// rather than a headcount, since holding a non-empty BoardRole
	// structurally means "I am the sole holder."
	t.Run("delete refuses to remove an account holding a board role", func(t *testing.T) {
		// Target must be a @kthais.com address (isKthaisEmail) — unlike
		// headOfIT (an @example.com admin login used only to authenticate
		// the request), so this needs its own fixture. Treasurer, not Head
		// of IT, to prove the check isn't special-cased to just one role.
		boardMember := mustCreateBoardRoleHolder(t, db, cfg, "offboarding-delete-board-member@kthais.com", models.BoardRoleTreasurer)
		t.Cleanup(func() {
			db.Where("email = ?", boardMember.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", boardMember.email).Unscoped().Delete(&models.User{})
		})

		before := atomic.LoadInt64(&deleteCallCount)
		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": boardMember.email, "confirm": "DELETE THIS ACCOUNT",
		}, headOfIT.cookie)
		require.Equal(t, http.StatusConflict, rec.Code)

		var stillExists int64
		db.Model(&models.User{}).Where("email = ?", boardMember.email).Count(&stillExists)
		require.EqualValues(t, 1, stillExists, "the refused delete must not have touched the local record")
		require.Equal(t, before, atomic.LoadInt64(&deleteCallCount),
			"a refused delete must never reach onboarding-service — the real account must stay untouched")
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

	// GrantHeadOfIT/RevokeHeadOfIT no longer exist — Head of IT is one of
	// the eight exactly-one board roles and only ever moves via
	// BoardRoleHandler.TransferBoardRole (see board_role_handler_test.go).
	// ListHeadsOfIT is kept for backward compatibility (see its own doc
	// comment) and still queries the same underlying fact, just via
	// board_role instead of the old is_head_of_it column.
	t.Run("list heads of IT", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/admin/head-of-it", nil)
		req.AddCookie(regularAdmin.cookie)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "any admin can list heads, not just heads of IT")

		var body struct {
			Emails []string `json:"emails"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Contains(t, body.Emails, headOfIT.email)
		require.NotContains(t, body.Emails, regularAdmin.email)
	})
}

// mustCreateHeadOfIT creates an admin holding models.BoardRoleHeadOfIT —
// matching how the first holder of any of the eight exactly-one board
// roles actually gets bootstrapped in production (see Profile.BoardRole's
// doc comment); every change after that goes through
// BoardRoleHandler.TransferBoardRole. See mustCreateBoardRoleHolder (which
// this delegates to) for why a real ambient holder gets vacated and
// restored around the fixture.
func mustCreateHeadOfIT(t *testing.T, db *gorm.DB, cfg *config.Config, email string) testAdmin {
	t.Helper()
	return mustCreateBoardRoleHolder(t, db, cfg, email, models.BoardRoleHeadOfIT)
}
