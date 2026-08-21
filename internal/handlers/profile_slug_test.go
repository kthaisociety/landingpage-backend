package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"backend/internal/config"
	"backend/internal/mailchimp"
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

// TestProfileSlugs covers the /members/{uuid} -> /members/{slug} migration:
// new profiles get a collision-safe slug at creation, and the public profile
// lookup resolves by slug, by profile id, or by user uuid — so links shared
// before this change keep working.
//
// Requires local Postgres (via `docker compose up -d`) and a .env file;
// skips silently when absent.
func TestProfileSlugs(t *testing.T) {
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

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewProfileHandler(db, &mailchimp.MailchimpAPI{}, cfg).Register(api)

	suffix := uuid.New().String()[:8]

	mustSignupCookie := func(t *testing.T, email string) *http.Cookie {
		t.Helper()
		userID := uuid.New()
		require.NoError(t, db.Create(&models.User{
			UserId:   userID,
			Email:    email,
			Provider: "test",
			Roles:    pq.StringArray{"user"},
		}).Error)
		token, err := utils.WriteJWT(email, []string{"user"}, userID, cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	t.Cleanup(func() {
		db.Where("email LIKE ?", "slugtest-%-"+suffix+"@seed.local").Unscoped().Delete(&models.Profile{})
		db.Where("email LIKE ?", "slugtest-%-"+suffix+"@seed.local").Unscoped().Delete(&models.User{})
	})

	createProfile := func(t *testing.T, cookie *http.Cookie, firstName, lastName, email string) map[string]any {
		t.Helper()
		body := map[string]any{
			"firstName":      firstName,
			"lastName":       lastName,
			"email":          email,
			"university":     "KTH Royal Institute of Technology",
			"graduationYear": 2027,
		}
		rec := doJSONRequest(t, engine, "POST", "/api/v1/profile/create", body, cookie)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	// Two people sharing a first+last name must get distinct, collision-safe slugs.
	emailA := "slugtest-a-" + suffix + "@seed.local"
	emailB := "slugtest-b-" + suffix + "@seed.local"
	profileA := createProfile(t, mustSignupCookie(t, emailA), "Villim", "Prpić", emailA)
	profileB := createProfile(t, mustSignupCookie(t, emailB), "Villim", "Prpić", emailB)

	slugA, _ := profileA["slug"].(string)
	slugB, _ := profileB["slug"].(string)
	require.Equal(t, "villim-prpic", slugA)
	require.Equal(t, "villim-prpic-2", slugB)
	require.NotEqual(t, slugA, slugB)

	idA, _ := profileA["id"].(string)
	userIDA, _ := profileA["user_id"].(string)
	require.NotEmpty(t, idA)
	require.NotEmpty(t, userIDA)

	fetchPublic := func(t *testing.T, identifier string) int {
		t.Helper()
		rec := doJSONRequest(t, engine, "GET", "/api/v1/profile/public/"+identifier, nil, nil)
		return rec.Code
	}

	t.Run("resolves by slug", func(t *testing.T) {
		require.Equal(t, http.StatusOK, fetchPublic(t, slugA))
	})
	t.Run("resolves by profile id (old-link backward compat)", func(t *testing.T) {
		require.Equal(t, http.StatusOK, fetchPublic(t, idA))
	})
	t.Run("resolves by user uuid (old-link backward compat)", func(t *testing.T) {
		require.Equal(t, http.StatusOK, fetchPublic(t, userIDA))
	})
	t.Run("unknown identifier 404s", func(t *testing.T) {
		require.Equal(t, http.StatusNotFound, fetchPublic(t, "does-not-exist"))
	})
}
