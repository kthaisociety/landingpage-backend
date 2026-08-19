package handlers

import (
	"fmt"
	"os"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestSendPendingInvites exercises SendPendingInvites directly (the method
// shared by the admin bulk-send endpoint and the daily scheduler), rather
// than the earlier lifecycle test's HTTP flow, so it doesn't disturb that
// test's assumption that its fixture applicant hasn't been invited yet.
//
// Requires local Postgres (via `docker compose up -d`) and a .env file, like
// TestApplicationAndInterviewLifecycle; skips silently when absent.
func TestSendPendingInvites(t *testing.T) {
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
		&models.GeneralApplication{},
		&models.TeamQuestionsToken{},
		&models.TeamQuestionsSettings{},
	))

	suffix := uuid.New().String()[:8]
	applicantEmail := fmt.Sprintf("send-pending-%s@seed.local", suffix)
	applicationID := uuid.New()

	require.NoError(t, db.Create(&models.GeneralApplication{
		Id:                   applicationID,
		ApplicationYear:      generalApplicationYear,
		FirstName:            "Pending",
		LastName:             "Invitee",
		Email:                applicantEmail,
		EmailNormalized:      applicantEmail,
		Programme:            "Computer Science",
		GraduationYear:       2027,
		LinkedinURL:          "https://linkedin.com/in/send-pending-" + suffix,
		ResumeBlobID:         uuid.New(),
		ResumeFileName:       "resume.pdf",
		ResumeContentType:    "application/pdf",
		Teams:                pq.StringArray{"Development"},
		TeamInterestReason:   "test fixture",
		Availability:         "4-6 hours",
		Contribution:         "test fixture",
		DataRetentionConsent: true,
		Status:               models.GeneralApplicationStatusPending,
	}).Error)
	t.Cleanup(func() {
		db.Where("application_id = ?", applicationID).Unscoped().Delete(&models.TeamQuestionsToken{})
		db.Unscoped().Delete(&models.GeneralApplication{}, "id = ?", applicationID)
	})

	h := NewTeamQuestionsHandler(db, cfg)

	t.Run("first run invites the pending applicant", func(t *testing.T) {
		sent, failed, err := h.SendPendingInvites()
		require.NoError(t, err)
		require.Empty(t, failed)
		require.GreaterOrEqual(t, sent, 1, "should have sent at least our fixture applicant's invite")

		var tokenCount int64
		require.NoError(t, db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&tokenCount).Error)
		require.Equal(t, int64(1), tokenCount, "should have issued exactly one token for the fixture applicant")

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.NotNil(t, app.TeamQuestionsInviteSentAt)
	})

	t.Run("re-running does not re-invite the same applicant", func(t *testing.T) {
		sent, failed, err := h.SendPendingInvites()
		require.NoError(t, err)
		require.Empty(t, failed)
		require.Zero(t, sent, "already-invited applicant should not be sent again, and no other pending/uninvited applicants should exist from this test")

		var tokenCount int64
		require.NoError(t, db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&tokenCount).Error)
		require.Equal(t, int64(1), tokenCount, "should still only have the one token from the first run")
	})
}
