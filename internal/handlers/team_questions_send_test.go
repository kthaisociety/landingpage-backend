package handlers

import (
	"fmt"
	"os"
	"testing"
	"time"

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
	cfg.DevelopmentMode = true // Integration fixtures must never send real email.

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
		&models.TeamQuestionsDeliveryEvent{},
	))

	suffix := uuid.New().String()[:8]
	applicantEmail := fmt.Sprintf("send-pending-%s@example.com", suffix)
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
	h.now = func() time.Time { return time.Date(2026, time.September, 6, 12, 0, 0, 0, teamQuestionsInviteTZ) }

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

// TestSendPendingReminders covers the 7-day Team Questions reminder: due
// applications (invited 7+ days ago, still pending, never reminded) get a
// fresh token and a reminder email; applications invited too recently don't;
// and re-running never reminds the same application twice.
//
// Requires local Postgres (via `docker compose up -d`) and a .env file;
// skips silently when absent.
func TestSendPendingReminders(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.DevelopmentMode = true // Integration fixtures must never send real email.

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
		&models.TeamQuestionsDeliveryEvent{},
	))

	suffix := uuid.New().String()[:8]

	mustCreateApplication := func(t *testing.T, label string, invitedAt time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		email := fmt.Sprintf("reminder-%s-%s@example.com", label, suffix)
		require.NoError(t, db.Create(&models.GeneralApplication{
			Id:                        id,
			ApplicationYear:           generalApplicationYear,
			FirstName:                 "Reminder",
			LastName:                  label,
			Email:                     email,
			EmailNormalized:           email,
			Programme:                 "Computer Science",
			GraduationYear:            2027,
			LinkedinURL:               "https://linkedin.com/in/reminder-" + label + "-" + suffix,
			ResumeBlobID:              uuid.New(),
			ResumeFileName:            "resume.pdf",
			ResumeContentType:         "application/pdf",
			Teams:                     pq.StringArray{"Development"},
			TeamInterestReason:        "test fixture",
			Availability:              "4-6 hours",
			Contribution:              "test fixture",
			DataRetentionConsent:      true,
			Status:                    models.GeneralApplicationStatusPending,
			TeamQuestionsInviteSentAt: &invitedAt,
		}).Error)
		t.Cleanup(func() {
			db.Where("application_id = ?", id).Unscoped().Delete(&models.TeamQuestionsToken{})
			db.Unscoped().Delete(&models.GeneralApplication{}, "id = ?", id)
		})
		return id
	}

	testNow := time.Date(2026, time.September, 6, 12, 0, 0, 0, teamQuestionsInviteTZ)
	overdueID := mustCreateApplication(t, "overdue", testNow.Add(-7*24*time.Hour))
	recentID := mustCreateApplication(t, "recent", testNow.Add(-7*24*time.Hour+time.Second))

	h := NewTeamQuestionsHandler(db, cfg)
	h.now = func() time.Time { return testNow }

	t.Run("only the 7+ day overdue applicant is reminded", func(t *testing.T) {
		sent, failed, err := h.SendPendingReminders()
		require.NoError(t, err)
		require.Empty(t, failed)
		require.GreaterOrEqual(t, sent, 1, "should have reminded at least our overdue fixture")

		var overdue models.GeneralApplication
		require.NoError(t, db.First(&overdue, "id = ?", overdueID).Error)
		require.NotNil(t, overdue.TeamQuestionsReminderSentAt)

		var overdueTokenCount int64
		require.NoError(t, db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", overdueID).Count(&overdueTokenCount).Error)
		require.Equal(t, int64(1), overdueTokenCount, "reminder should have issued a fresh token")

		var recent models.GeneralApplication
		require.NoError(t, db.First(&recent, "id = ?", recentID).Error)
		require.Nil(t, recent.TeamQuestionsReminderSentAt, "one second short of 7 days — not due yet")

		var recentTokenCount int64
		require.NoError(t, db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", recentID).Count(&recentTokenCount).Error)
		require.Zero(t, recentTokenCount)
	})

	t.Run("re-running does not remind the same applicant twice", func(t *testing.T) {
		sent, failed, err := h.SendPendingReminders()
		require.NoError(t, err)
		require.Empty(t, failed)
		require.Zero(t, sent, "already-reminded applicant should not be reminded again")

		var tokenCount int64
		require.NoError(t, db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", overdueID).Count(&tokenCount).Error)
		require.Equal(t, int64(1), tokenCount, "should still only have the one token from the first reminder")
	})
}
