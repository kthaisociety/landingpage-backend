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

	// Regression for the race Greptile found: requireHeadOfIT's read happens
	// outside TransferHeadOfIT's transaction, so two overlapping requests
	// from the same still-current head could both pass that check before
	// either transaction commits. Fires both at once against real Postgres
	// and asserts the compare-and-swap in the transaction lets exactly one
	// through, never leaving two heads.
	t.Run("concurrent transfers can't create two heads", func(t *testing.T) {
		// otherAdmin holds it after the previous subtest — hand it back to
		// headOfIT so this subtest starts from a known single holder.
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", headOfIT.email).Update("is_head_of_it", true).Error)
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", otherAdmin.email).Update("is_head_of_it", false).Error)

		thirdAdmin := mustCreateAdmin(t, db, cfg, "offboarding-third-admin@example.com")
		t.Cleanup(func() {
			db.Where("email = ?", thirdAdmin.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", thirdAdmin.email).Unscoped().Delete(&models.User{})
		})

		targets := []string{otherAdmin.email, thirdAdmin.email}
		codes := make([]int, len(targets))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, target := range targets {
			wg.Add(1)
			go func(i int, target string) {
				defer wg.Done()
				<-start
				payload, _ := json.Marshal(map[string]any{"email": target})
				req := httptest.NewRequest("POST", "/api/v1/admin/head-of-it/transfer", strings.NewReader(string(payload)))
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
				// The loser gets 403 if the outer requireHeadOfIT check runs
				// after the winner's transaction has already committed, or
				// 409 if it loses the in-transaction compare-and-swap
				// instead — which one depends on goroutine scheduling, but
				// either way it must be a clean rejection, not a 500.
				require.Contains(t, []int{http.StatusForbidden, http.StatusConflict}, code,
					"the loser must get a clean rejection, not a 500")
			}
		}
		require.Equal(t, 1, successes, "exactly one of the two concurrent transfers should win")

		var headCount int64
		require.NoError(t, db.Model(&models.Profile{}).
			Where("email IN ? AND is_head_of_it = ?", []string{headOfIT.email, otherAdmin.email, thirdAdmin.email}, true).
			Count(&headCount).Error)
		require.Equal(t, int64(1), headCount, "there must be exactly one head of IT after the race")
	})

	// Regression for the second Greptile finding: the successor is validated
	// to exist before the transaction starts, but the grant update inside
	// the transaction didn't check RowsAffected — if the successor's
	// profile vanished in between, GORM reports no error for a zero-row
	// UPDATE, so the transaction would still commit the requester's own
	// revocation and leave nobody as head. A GORM "before update" callback
	// deletes the successor at the exact moment the grant UPDATE is about
	// to run, deterministically reproducing that gap without needing a real
	// goroutine race.
	t.Run("successor vanishing mid-transfer doesn't leave zero heads", func(t *testing.T) {
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", headOfIT.email).Update("is_head_of_it", true).Error)
		require.NoError(t, db.Model(&models.Profile{}).
			Where("email IN ?", []string{regularAdmin.email, otherAdmin.email}).
			Update("is_head_of_it", false).Error)

		victim := mustCreateAdmin(t, db, cfg, "offboarding-vanishing-admin@example.com")
		t.Cleanup(func() {
			db.Where("email = ?", victim.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", victim.email).Unscoped().Delete(&models.User{})
		})

		var triggered bool
		const callbackName = "test:delete-successor-before-grant"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			if triggered || tx.Statement.Table != "profiles" {
				return
			}
			changes, ok := tx.Statement.Dest.(map[string]interface{})
			if !ok {
				return
			}
			granting, ok := changes["is_head_of_it"].(bool)
			if !ok || !granting {
				return
			}
			triggered = true
			require.NoError(t, db.Unscoped().Where("email = ?", victim.email).Delete(&models.Profile{}).Error)
		}))
		t.Cleanup(func() {
			require.NoError(t, db.Callback().Update().Remove(callbackName))
		})

		rec := post(t, "/api/v1/admin/head-of-it/transfer", map[string]any{"email": victim.email}, headOfIT.cookie)
		require.NotEqual(t, http.StatusOK, rec.Code, "must not report success when the successor vanished mid-transfer")
		require.True(t, triggered, "the callback should have fired — otherwise this test isn't exercising the race at all")

		var stillHead bool
		require.NoError(t, db.Model(&models.Profile{}).Select("is_head_of_it").
			Where("email = ?", headOfIT.email).Scan(&stillHead).Error)
		require.True(t, stillHead, "the original head must keep the role since the handover never completed")
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
