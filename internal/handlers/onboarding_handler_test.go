package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

// TestOnboardingHandler covers the /internal/onboarding/* endpoints against
// a real Postgres connection, same skip-if-no-.env pattern as
// TestExchangeMCPToken. send-email's actual delivery isn't exercised here
// (that needs a real or mocked mailer, see internal/email's own tests) —
// only its auth gating and validation.
func TestOnboardingHandler(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.OnboardingServiceSecret = "test-onboarding-service-secret"

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.GeneralApplication{}))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewOnboardingHandler(db, cfg).Register(api)

	newApplication := func(t *testing.T, status models.GeneralApplicationStatus) models.GeneralApplication {
		t.Helper()
		suffix := uuid.New().String()[:8]
		application := models.GeneralApplication{
			Id:                 uuid.New(),
			ApplicationYear:    time.Now().Year(),
			FirstName:          "Onboarding",
			LastName:           "Test",
			Email:              fmt.Sprintf("onboarding-test-%s@seed.local", suffix),
			EmailNormalized:    fmt.Sprintf("onboarding-test-%s@seed.local", suffix),
			Programme:          "TCOMK",
			LinkedinURL:        "https://linkedin.com/in/test",
			ResumeFileName:     "resume.pdf",
			Teams:              pq.StringArray{"IT"},
			TeamInterestReason: "test",
			Availability:       "test",
			Contribution:       "test",
			Status:             status,
			AssignedTeam:       "IT",
		}
		require.NoError(t, db.Create(&application).Error)
		t.Cleanup(func() {
			db.Unscoped().Delete(&application)
		})
		return application
	}

	recordAccount := func(applicationID, kthaisEmail, secret string) *httptest.ResponseRecorder {
		payload, err := json.Marshal(map[string]string{
			"application_id": applicationID,
			"kthais_email":   kthaisEmail,
		})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/internal/onboarding/record-account", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("X-Service-Secret", secret)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("accepted application can have its account recorded", func(t *testing.T) {
		application := newApplication(t, models.GeneralApplicationStatusAccepted)
		rec := recordAccount(application.Id.String(), "new.member@kthais.com", cfg.OnboardingServiceSecret)
		require.Equal(t, http.StatusOK, rec.Code)

		var saved models.GeneralApplication
		require.NoError(t, db.First(&saved, "id = ?", application.Id).Error)
		require.Equal(t, "new.member@kthais.com", saved.KthaisEmail)
		require.NotNil(t, saved.OnboardedAt)
	})

	t.Run("non-accepted application is rejected", func(t *testing.T) {
		application := newApplication(t, models.GeneralApplicationStatusAvailable)
		rec := recordAccount(application.Id.String(), "new.member@kthais.com", cfg.OnboardingServiceSecret)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown application id is rejected", func(t *testing.T) {
		rec := recordAccount(uuid.New().String(), "new.member@kthais.com", cfg.OnboardingServiceSecret)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("wrong secret is rejected", func(t *testing.T) {
		application := newApplication(t, models.GeneralApplicationStatusAccepted)
		rec := recordAccount(application.Id.String(), "new.member@kthais.com", "not-the-right-secret")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("missing secret is rejected", func(t *testing.T) {
		application := newApplication(t, models.GeneralApplicationStatusAccepted)
		rec := recordAccount(application.Id.String(), "new.member@kthais.com", "")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("send-email requires the shared secret", func(t *testing.T) {
		payload, err := json.Marshal(map[string]string{
			"to":      "someone@example.com",
			"subject": "Test",
			"body":    "Test body",
		})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/internal/onboarding/send-email", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("send-email validates required fields", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/internal/onboarding/send-email", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Service-Secret", cfg.OnboardingServiceSecret)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
