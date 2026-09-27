package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestTeamQuestionsBulkSendRequiresHeadOfIT pins the Team Questions
// bulk-send gate to the Head of IT board role: declaring the IT team via the
// self-editable Profile.AdminTeam must not be enough. Needs a real Postgres
// connection, following the same "skip if no .env" convention as the other
// DB-backed handler tests.
func TestTeamQuestionsBulkSendRequiresHeadOfIT(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(
		&models.User{}, &models.Profile{}, &models.GeneralApplication{},
		&models.TeamQuestionsSubmission{}, &models.TeamQuestionsToken{},
		&models.TeamQuestionsSettings{}, &models.TeamQuestion{},
		&models.GeneralApplicationSettings{}, &models.TeamQuestionsDeliveryEvent{},
	))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	// Inside the default Team Questions window, so the closed-window
	// short-circuit doesn't answer before the Head of IT check runs.
	handler := NewTeamQuestionsHandler(db, cfg)
	handler.now = func() time.Time { return time.Date(2026, time.September, 6, 12, 0, 0, 0, teamQuestionsInviteTZ) }
	handler.Register(api)

	headOfIT := mustCreateTeamAdmin(t, db, cfg, "tq-bulk-head-of-it@seed.local", "IT")
	grantHeadOfIT(t, db, headOfIT.email)
	selfDeclaredIT := mustCreateTeamAdmin(t, db, cfg, "tq-bulk-self-declared-it@seed.local", "IT")
	t.Cleanup(func() {
		emails := []string{headOfIT.email, selfDeclaredIT.email}
		db.Where("email IN ?", emails).Unscoped().Delete(&models.Profile{})
		db.Where("email IN ?", emails).Unscoped().Delete(&models.User{})
	})

	canSend := func(t *testing.T, admin testAdmin) bool {
		t.Helper()
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/send-bulk/preview", nil, admin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var body struct {
			CanSend bool `json:"can_send"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.CanSend
	}

	t.Run("declaring the IT team without holding Head of IT is refused", func(t *testing.T) {
		require.False(t, canSend(t, selfDeclaredIT))
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/team-questions/send-bulk", nil, selfDeclaredIT.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	// Preview only: a real bulk send would email every pending applicant in
	// the shared dev database.
	t.Run("the Head of IT board role holder may send", func(t *testing.T) {
		require.True(t, canSend(t, headOfIT))
	})
}
