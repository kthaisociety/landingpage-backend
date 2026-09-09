package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestDeleteUserProtectsLastHeadOfIT is a regression for a Greptile finding
// on the offboarding PR: DeleteUser (the plain "Delete user" admin action,
// unrelated to member offboarding) deleted a profile outright with no
// awareness of Profile.IsHeadOfIT — deleting the sole remaining Head of
// IT's profile would zero out the flag with no way to grant it back through
// the app (GrantHeadOfIT requires already being a head), leaving offboarding
// unusable until someone manually fixed the database. Needs a real Postgres
// connection (the check locks and counts head-of-IT rows), so it follows
// the same "skip if no .env" convention as TestOffboardingHandler.
func TestDeleteUserProtectsLastHeadOfIT(t *testing.T) {
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

	soleHead := mustCreateHeadOfIT(t, db, cfg, "delete-user-sole-head@example.com")
	otherHead := mustCreateHeadOfIT(t, db, cfg, "delete-user-other-head@example.com")
	requester := mustCreateAdmin(t, db, cfg, "delete-user-requester@example.com")
	t.Cleanup(func() {
		for _, email := range []string{soleHead.email, otherHead.email, requester.email} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAdminHandler(db, cfg).Register(engine.Group("/api/v1"))

	del := func(t *testing.T, email string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+email, nil)
		req.AddCookie(requester.cookie)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	// Two heads exist right now — deleting one is fine, the other remains.
	rec := del(t, otherHead.email)
	require.Equal(t, http.StatusOK, rec.Code, "safe to delete a head of IT while another remains")

	var otherStillExists int64
	db.Model(&models.User{}).Where("email = ?", otherHead.email).Count(&otherStillExists)
	require.Zero(t, otherStillExists, "the deleted user should actually be gone")

	// Now soleHead is the only head of IT left — deleting them must be refused.
	rec = del(t, soleHead.email)
	require.Equal(t, http.StatusConflict, rec.Code)

	var soleHeadStillExists int64
	db.Model(&models.User{}).Where("email = ?", soleHead.email).Count(&soleHeadStillExists)
	require.EqualValues(t, 1, soleHeadStillExists, "the refused delete must not have taken effect")

	var stillHead bool
	require.NoError(t, db.Model(&models.Profile{}).Select("is_head_of_it").
		Where("email = ?", soleHead.email).Scan(&stillHead).Error)
	require.True(t, stillHead, "the sole head's profile must be untouched")
}
