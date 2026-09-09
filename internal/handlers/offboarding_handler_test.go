package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

	t.Run("delete refuses to remove the only remaining head of IT, before touching any real account", func(t *testing.T) {
		soleHead := mustCreateHeadOfIT(t, db, cfg, "offboarding-delete-sole-head@kthais.com")
		t.Cleanup(func() {
			db.Where("email = ?", soleHead.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", soleHead.email).Unscoped().Delete(&models.User{})
		})
		// Make headOfIT temporarily not a head, so soleHead really is the
		// only one for the duration of this subtest.
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", headOfIT.email).Update("is_head_of_it", false).Error)
		t.Cleanup(func() {
			db.Model(&models.Profile{}).Where("email = ?", headOfIT.email).Update("is_head_of_it", true)
		})

		rec := post(t, "/api/v1/admin/offboarding/delete", map[string]any{
			"email": soleHead.email, "confirm": "DELETE THIS ACCOUNT",
		}, soleHead.cookie)
		require.Equal(t, http.StatusConflict, rec.Code)

		var stillExists int64
		db.Model(&models.User{}).Where("email = ?", soleHead.email).Count(&stillExists)
		require.EqualValues(t, 1, stillExists, "the refused delete must not have touched the local record")
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

	t.Run("list heads of IT", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": "nope@example.com"}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "sanity check the route exists before listing")

		req := httptest.NewRequest("GET", "/api/v1/admin/head-of-it", nil)
		req.AddCookie(regularAdmin.cookie)
		rec = httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "any admin can list heads, not just heads of IT")

		var body struct {
			Emails []string `json:"emails"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Contains(t, body.Emails, headOfIT.email)
		require.NotContains(t, body.Emails, regularAdmin.email)
	})

	t.Run("grant head of IT", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": otherAdmin.email}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "no cookie at all")

		rec = post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": otherAdmin.email}, regularAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "only a current head of IT can grant it")

		rec = post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": "nobody@example.com"}, headOfIT.cookie)
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
		rec = post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": nonAdminEmail}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "target must already be an admin")

		rec = post(t, "/api/v1/admin/head-of-it/grant", map[string]any{"email": otherAdmin.email}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		// Both are now heads — granting never costs the granter their own status.
		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the original head keeps access")
		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, otherAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the newly granted head also has access")
	})

	t.Run("revoke head of IT", func(t *testing.T) {
		// otherAdmin was granted in the previous subtest, so there are two
		// heads right now — safe to revoke one.
		rec := post(t, "/api/v1/admin/head-of-it/revoke", map[string]any{"email": otherAdmin.email}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "no cookie at all")

		rec = post(t, "/api/v1/admin/head-of-it/revoke", map[string]any{"email": otherAdmin.email}, regularAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "only a current head of IT can revoke it")

		rec = post(t, "/api/v1/admin/head-of-it/revoke", map[string]any{"email": regularAdmin.email}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "target isn't a head of IT at all")

		rec = post(t, "/api/v1/admin/head-of-it/revoke", map[string]any{"email": otherAdmin.email}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "safe to revoke while another head remains")

		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, otherAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "revoked admin lost access")
		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the remaining head still has access")
	})

	t.Run("can't revoke the only remaining head of IT", func(t *testing.T) {
		// Only headOfIT holds it at this point (previous subtest revoked
		// otherAdmin). Revoking headOfIT themselves must be refused.
		rec := post(t, "/api/v1/admin/head-of-it/revoke", map[string]any{"email": headOfIT.email}, headOfIT.cookie)
		require.Equal(t, http.StatusConflict, rec.Code)

		rec = post(t, "/api/v1/admin/offboarding/deactivate", map[string]any{"email": "test.user@kthais.com"}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the refused revoke must not have taken effect")
	})

	// Regression for the exact race this endpoint exists to prevent: with
	// exactly two heads, two concurrent "revoke a different one of the two"
	// requests must not both succeed — that would leave zero. The
	// SELECT ... FOR UPDATE lock inside the transaction should serialize
	// them so the loser re-counts after the winner's commit and refuses.
	t.Run("concurrent revokes can't drop below one head", func(t *testing.T) {
		thirdAdmin := mustCreateAdmin(t, db, cfg, "offboarding-third-admin@example.com")
		t.Cleanup(func() {
			db.Where("email = ?", thirdAdmin.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", thirdAdmin.email).Unscoped().Delete(&models.User{})
		})
		// Exactly two heads: headOfIT (still holds it) and thirdAdmin.
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", thirdAdmin.email).Update("is_head_of_it", true).Error)

		targets := []string{headOfIT.email, thirdAdmin.email}
		codes := make([]int, len(targets))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, target := range targets {
			wg.Add(1)
			go func(i int, target string) {
				defer wg.Done()
				<-start
				payload, _ := json.Marshal(map[string]any{"email": target})
				req := httptest.NewRequest("POST", "/api/v1/admin/head-of-it/revoke", strings.NewReader(string(payload)))
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(headOfIT.cookie)
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, req)
				codes[i] = rec.Code
			}(i, target)
		}
		close(start)
		wg.Wait()

		successes := 0
		for _, code := range codes {
			if code == http.StatusOK {
				successes++
			} else {
				// Both requests authenticate as headOfIT. If the winner's
				// target was headOfIT themselves, the loser's own outer
				// requireHeadOfIT check can now fail (403) since their
				// status just got revoked by the request that beat them —
				// otherwise the loser hits the in-transaction count check
				// instead (409). Either is a clean rejection, not a 500.
				require.Contains(t, []int{http.StatusForbidden, http.StatusConflict}, code,
					"the loser must get a clean rejection, not a 500")
			}
		}
		require.Equal(t, 1, successes, "exactly one of the two concurrent revokes should win")

		var headCount int64
		require.NoError(t, db.Model(&models.Profile{}).
			Where("email IN ? AND is_head_of_it = ?", targets, true).
			Count(&headCount).Error)
		require.Equal(t, int64(1), headCount, "there must still be exactly one head of IT after the race")

		// Restore a clean single-head state for any later subtest.
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", headOfIT.email).Update("is_head_of_it", true).Error)
	})
}

// mustCreateHeadOfIT creates an admin and flips Profile.IsHeadOfIT directly
// in the database, matching how the very first Head of IT actually gets
// bootstrapped in production (every one after that goes through
// GrantHeadOfIT).
func mustCreateHeadOfIT(t *testing.T, db *gorm.DB, cfg *config.Config, email string) testAdmin {
	t.Helper()
	admin := mustCreateAdmin(t, db, cfg, email)
	require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", email).Update("is_head_of_it", true).Error)
	return admin
}
