package handlers

import (
	"fmt"
	"os"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestResolveTeamFromAcceptedApplication guards the exact bug this function
// used to have: it must match on KthaisEmail (the @kthais.com address
// onboarding-service provisions, which is what a real member actually logs
// in with — see AuthHandler.GoogleCallback) and NOT on EmailNormalized
// (their original recruitment application email), which a genuinely
// onboarded member's login email will essentially never equal.
//
// Requires local Postgres (via `docker compose up -d`) and a .env file;
// skips silently when absent, same as TestProfileSlugs.
func TestResolveTeamFromAcceptedApplication(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&models.GeneralApplication{}))

	newAcceptedApplication := func(t *testing.T, personalEmail, kthaisEmail, team string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		app := models.GeneralApplication{
			Id:                id,
			ApplicationYear:   2026,
			FirstName:         "Resolve",
			LastName:          "Team",
			Email:             personalEmail,
			EmailNormalized:   personalEmail,
			Programme:         "Computer Science",
			GraduationYear:    2027,
			LinkedinURL:       "https://linkedin.com/in/resolve-team",
			ResumeFileName:    "resume.pdf",
			ResumeContentType: "application/pdf",
			Teams:             []string{team},
			Availability:      "4-6 hours",
			Contribution:      "Contribution text.",
			Status:            models.GeneralApplicationStatusAccepted,
			KthaisEmail:       kthaisEmail,
			AssignedTeam:      team,
		}
		require.NoError(t, db.Create(&app).Error)
		t.Cleanup(func() {
			db.Unscoped().Delete(&app)
		})
		return id
	}

	suffix := uuid.New().String()[:8]
	personalEmail := fmt.Sprintf("resolve-team-personal-%s@example.com", suffix)
	kthaisEmail := fmt.Sprintf("resolve.team.%s@kthais.local", suffix)
	newAcceptedApplication(t, personalEmail, kthaisEmail, "Research")

	t.Run("matches on kthais email", func(t *testing.T) {
		require.Equal(t, "Research", resolveTeamFromAcceptedApplication(db, kthaisEmail))
	})

	t.Run("case-insensitive and untrimmed", func(t *testing.T) {
		require.Equal(t, "Research", resolveTeamFromAcceptedApplication(db, "  "+kthaisEmail+"  "))
	})

	t.Run("does not match on the original application email", func(t *testing.T) {
		require.Equal(t, "", resolveTeamFromAcceptedApplication(db, personalEmail))
	})

	t.Run("no match returns empty string", func(t *testing.T) {
		require.Equal(t, "", resolveTeamFromAcceptedApplication(db, "nobody@kthais.local"))
	})
}
