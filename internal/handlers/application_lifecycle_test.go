package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/database"
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

// TestApplicationAndInterviewLifecycle walks a single applicant through the
// entire pipeline end to end, over real HTTP handlers and a real Postgres
// connection: initial application -> pending -> Team Questions invite ->
// submission (with a team withdrawal) -> available -> claim -> interview
// invite sent/resent -> release -> ineligible -> restore. This is the
// integration test analogue of the manual curl walkthrough used to validate
// the feature during development.
//
// Like TestBlob in internal/models, this requires local infra (Postgres +
// Redis, via `docker compose up -d`) and a .env file, so it silently skips
// when .env isn't present rather than failing in environments without it.
func TestApplicationAndInterviewLifecycle(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres + Redis)")
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
		&models.User{},
		&models.Profile{},
		&models.GeneralApplication{},
		&models.TeamQuestionsSubmission{},
		&models.TeamQuestionsToken{},
		&models.TeamQuestionsSettings{},
	))

	// The public "/applications/general" route is rate-limited (5/min per
	// client IP) via Redis, keyed independently of this test. httptest
	// requests always report the same synthetic client IP, so without
	// clearing that bucket first, re-running this test twice within a minute
	// would spuriously 429 on a completely correct handler.
	if redisClient, err := database.GetRedisClient(cfg); err == nil {
		ctx := context.Background()
		keys, _ := redisClient.Keys(ctx, "rate_limit:*").Result()
		if len(keys) > 0 {
			redisClient.Del(ctx, keys...)
		}
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)
	NewTeamQuestionsHandler(db, cfg).Register(api)

	// --- test fixtures: two distinct admins ---
	// No team_questions_settings row is needed — getSettings() falls back to
	// a built-in default template when none is saved, so sending works either way.

	adminA := mustCreateAdmin(t, db, cfg, "lifecycle-admin-a@seed.local")
	adminB := mustCreateAdmin(t, db, cfg, "lifecycle-admin-b@seed.local")

	suffix := uuid.New().String()[:8]
	applicantEmail := fmt.Sprintf("lifecycle-%s@seed.local", suffix)

	t.Cleanup(func() {
		var app models.GeneralApplication
		if err := db.Where("email_normalized = ?", applicantEmail).First(&app).Error; err == nil {
			db.Where("application_id = ?", app.Id).Delete(&models.TeamQuestionsSubmission{})
			db.Where("application_id = ?", app.Id).Delete(&models.TeamQuestionsToken{})
			db.Unscoped().Delete(&app)
		}
		db.Where("email = ?", adminA.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", adminA.email).Unscoped().Delete(&models.User{})
		db.Where("email = ?", adminB.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", adminB.email).Unscoped().Delete(&models.User{})
	})

	var applicationID string

	t.Run("initial application defaults to pending", func(t *testing.T) {
		rec := doMultipartRequest(t, engine, "POST", "/api/v1/applications/general", map[string]string{
			"firstName":            "Lifecycle",
			"lastName":             "Tester",
			"email":                applicantEmail,
			"gender":               "Prefer not to say",
			"university":           "KTH Royal Institute of Technology",
			"programme":            "Computer Science",
			"graduationYear":       "2027",
			"linkedinUrl":          "https://linkedin.com/in/lifecycle-" + suffix,
			"availability":         "4-6 hours",
			"contribution":         "I will contribute meaningfully to this team over the coming year.",
			"dataRetentionConsent": "true",
		}, map[string][]string{
			"teams":     {"Development", "Research"},
			"interests": {"Machine Learning"},
		})
		require.Equal(t, http.StatusCreated, rec.Code)

		var body struct {
			Id     string `json:"id"`
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "pending", body.Status)
		applicationID = body.Id
	})

	t.Run("claiming before Team Questions is rejected", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/claim", nil, adminA.cookie)
		require.Equal(t, http.StatusConflict, rec.Code)
	})

	var rawToken string

	t.Run("resend issues a usable token", func(t *testing.T) {
		var beforeCount int64
		db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&beforeCount)

		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/team-questions/resend", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var afterCount int64
		db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&afterCount)
		require.Equal(t, beforeCount+1, afterCount)
	})

	t.Run("form is scoped to the applicant's own teams", func(t *testing.T) {
		// The raw token is never returned by the API (only emailed), so to
		// drive the public endpoints directly we mint one the same way
		// issueAndSend does and insert it ourselves.
		raw, hash, err := utils.GenerateToken()
		require.NoError(t, err)
		rawToken = raw
		require.NoError(t, db.Create(&models.TeamQuestionsToken{
			ApplicationID: uuid.MustParse(applicationID),
			TokenHash:     hash,
			ExpiresAt:     time.Now().Add(30 * 24 * time.Hour),
		}).Error)

		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/team-questions/"+rawToken, nil, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			FirstName string                      `json:"first_name"`
			Teams     []string                    `json:"teams"`
			Questions map[string][]map[string]any `json:"questions"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "Lifecycle", body.FirstName)
		require.ElementsMatch(t, []string{"Development", "Research"}, body.Teams)
		require.Contains(t, body.Questions, "Development")
		require.Contains(t, body.Questions, "Research")
		require.NotContains(t, body.Questions, "IT")
	})

	t.Run("submit rejects missing required answers", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/team-questions/"+rawToken, map[string]any{
			"answers":         map[string]any{"Development": map[string]string{"motivation": ""}},
			"withdrawn_teams": []string{},
		}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid submission withdraws a team and unlocks claiming", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/team-questions/"+rawToken, map[string]any{
			"answers": map[string]any{
				"Development": map[string]string{
					"motivation": "I want to ship real products with a team.",
					"stack":      "Go, TypeScript",
				},
			},
			"withdrawn_teams": []string{"Research"},
		}, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.Equal(t, models.GeneralApplicationStatusAvailable, app.Status)
		require.Equal(t, []string{"Development"}, []string(app.Teams))

		var submission models.TeamQuestionsSubmission
		require.NoError(t, db.Where("application_id = ?", applicationID).First(&submission).Error)
		require.Equal(t, []string{"Research"}, []string(submission.WithdrawnTeams))
	})

	t.Run("the same link cannot be submitted twice", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/team-questions/"+rawToken, nil, nil)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("claim now succeeds", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/claim", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.Equal(t, models.GeneralApplicationStatusInterviewing, app.Status)
		require.Equal(t, adminA.email, app.InterviewingByEmail)
	})

	t.Run("a second admin cannot claim the same application", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/claim", nil, adminB.cookie)
		require.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("sending the interview invite stamps interview_invite_sent_at", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/send-invite", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &app))
		require.NotNil(t, app.InterviewInviteSentAt)
		firstSentAt := *app.InterviewInviteSentAt

		// Resending is allowed and just moves the timestamp forward — this is
		// what flips the admin UI's button from "Send" to "Resend".
		time.Sleep(10 * time.Millisecond)
		rec = doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/send-invite", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &app))
		require.True(t, app.InterviewInviteSentAt.After(firstSentAt))
	})

	t.Run("releasing returns the application to available", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/release", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.Equal(t, models.GeneralApplicationStatusAvailable, app.Status)
		require.Contains(t, []string(app.InterviewedBy), adminA.email)
	})

	t.Run("marking ineligible then restoring returns to available, not pending", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PATCH", "/api/v1/applications/admin/"+applicationID+"/ineligible", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.Equal(t, models.GeneralApplicationStatusIneligible, app.Status)

		rec = doJSONRequest(t, engine, "PATCH", "/api/v1/applications/admin/"+applicationID+"/restore", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		require.NoError(t, db.First(&app, "id = ?", applicationID).Error)
		require.Equal(t, models.GeneralApplicationStatusAvailable, app.Status,
			"an application that already submitted Team Questions must restore to available, not pending")
	})
}

type testAdmin struct {
	email  string
	userID uuid.UUID
	cookie *http.Cookie
}

func mustCreateAdmin(t *testing.T, db *gorm.DB, cfg *config.Config, email string) testAdmin {
	t.Helper()
	userID := uuid.New()

	require.NoError(t, db.Create(&models.User{
		UserId:   userID,
		Email:    email,
		Provider: "test",
		Roles:    pq.StringArray{"user", "member", "admin"},
	}).Error)

	require.NoError(t, db.Create(&models.Profile{
		UserUUID:               userID,
		UserId:                 mustFindUserPK(t, db, email),
		Email:                  email,
		FirstName:              "Admin",
		LastName:               "Tester",
		BookingPageURL:         "https://calendar.example.com/" + email,
		InterviewEmailTemplate: "Congrats {{first_name}}, let's talk!",
	}).Error)

	token, err := utils.WriteJWT(email, []string{"user", "member", "admin"}, userID, cfg.JwtSigningKey, 60)
	require.NoError(t, err)

	return testAdmin{
		email:  email,
		userID: userID,
		cookie: &http.Cookie{Name: "jwt", Value: token},
	}
}

func mustFindUserPK(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()
	var user models.User
	require.NoError(t, db.Where("email = ?", email).First(&user).Error)
	return user.ID
}

func doMultipartRequest(t *testing.T, engine *gin.Engine, method, path string, fields map[string]string, listFields map[string][]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	for key, values := range listFields {
		for _, value := range values {
			require.NoError(t, writer.WriteField(key, value))
		}
	}
	part, err := writer.CreateFormFile("resume", "resume.pdf")
	require.NoError(t, err)
	_, err = part.Write([]byte("%PDF-1.4 test resume"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func doJSONRequest(t *testing.T, engine *gin.Engine, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
