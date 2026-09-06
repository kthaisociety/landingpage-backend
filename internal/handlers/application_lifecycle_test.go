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
		&models.TeamQuestion{},
		&models.GeneralApplicationSettings{},
		&models.TeamQuestionsDeliveryEvent{},
	))

	// The public "/applications/general" and "/applications/team-questions/:token"
	// routes are rate-limited (5/min per client IP) via Redis, keyed
	// independently of this test. httptest requests always report the same
	// synthetic client IP, so without clearing that bucket, either re-running
	// this test within a minute, or this test itself making more than 5 public
	// requests in one run, would spuriously 429 on a completely correct
	// handler. Called at setup and again mid-test, since the token-supersede
	// check below alone uses several of the five requests.
	resetRateLimit := func() {
		redisClient, err := database.GetRedisClient(cfg)
		if err != nil {
			return
		}
		ctx := context.Background()
		keys, _ := redisClient.Keys(ctx, "rate_limit:*").Result()
		if len(keys) > 0 {
			redisClient.Del(ctx, keys...)
		}
	}
	resetRateLimit()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)
	teamQuestionsNow := time.Date(2026, time.September, 6, 12, 0, 0, 0, teamQuestionsInviteTZ)
	teamQuestionsHandler := NewTeamQuestionsHandler(db, cfg)
	teamQuestionsHandler.now = func() time.Time { return teamQuestionsNow }
	teamQuestionsHandler.Register(api)

	// --- test fixtures: two distinct admins ---
	// No team_questions_settings row is needed — getSettings() falls back to
	// a built-in default template when none is saved, so sending works either way.

	adminA := mustCreateAdmin(t, db, cfg, "lifecycle-admin-a@example.com")
	adminB := mustCreateAdmin(t, db, cfg, "lifecycle-admin-b@example.com")
	adminIT := mustCreateTeamAdmin(t, db, cfg, "lifecycle-admin-it@example.com", "IT")

	// Team questions are admin-configured data now (no hardcoded fallback),
	// so this test seeds exactly the questions it needs directly.
	devMotivationID := uuid.New()
	devStackID := uuid.New()
	require.NoError(t, db.Create(&models.TeamQuestion{
		Id: devMotivationID, Team: "Development", Text: "Why Development?", Required: true, SortOrder: 0,
	}).Error)
	require.NoError(t, db.Create(&models.TeamQuestion{
		Id: devStackID, Team: "Development", Text: "What's your stack?", Required: true, SortOrder: 1,
	}).Error)
	require.NoError(t, db.Create(&models.TeamQuestion{
		Id: uuid.New(), Team: "Research", Text: "Why Research?", Required: true, SortOrder: 0,
	}).Error)
	t.Cleanup(func() {
		db.Where("team IN ?", []string{"Development", "Research"}).Unscoped().Delete(&models.TeamQuestion{})
	})

	suffix := uuid.New().String()[:8]
	applicantEmail := fmt.Sprintf("lifecycle-%s@example.com", suffix)

	t.Cleanup(func() {
		var app models.GeneralApplication
		if err := db.Where("email_normalized = ?", applicantEmail).First(&app).Error; err == nil {
			db.Where("application_id = ?", app.Id).Delete(&models.TeamQuestionsSubmission{})
			db.Where("application_id = ?", app.Id).Delete(&models.TeamQuestionsToken{})
			db.Where("application_id = ?", app.Id).Unscoped().Delete(&models.TeamQuestionsDeliveryEvent{})
			db.Unscoped().Delete(&app)
		}
		db.Where("email = ?", adminA.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", adminA.email).Unscoped().Delete(&models.User{})
		db.Where("email = ?", adminB.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", adminB.email).Unscoped().Delete(&models.User{})
		db.Where("email = ?", adminIT.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", adminIT.email).Unscoped().Delete(&models.User{})
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

	// Regression test: general_applications.id is a text column while
	// team_questions_tokens.application_id is uuid — the bulk-send/preview
	// query compares them directly and previously errored at the DB level
	// (500) even though the count came back as an innocuous-looking 0/error
	// on the frontend. Individual send/resend never hit this because those
	// always compare via a bound parameter, not column-to-column.
	t.Run("bulk-send preview counts this still-pending, uninvited applicant", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/send-bulk/preview", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.GreaterOrEqual(t, body.Count, 1)
	})

	// Bulk-sending is disruptive and hard to undo, so it's gated to the head
	// of IT specifically — a plain admin, or even a regular IT team member,
	// must not be able to trigger it. Only checks the preview's can_send flag
	// and the actual POST's rejection for the non-head case here: actually
	// letting adminIT send would stamp TeamQuestionsInviteSentAt on this
	// test's own applicant and break the "resend issues a usable token"
	// subtest below, which expects that field to still be nil at this point.
	t.Run("bulk-send is restricted to the head of IT", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/send-bulk/preview", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var notHeadBody struct {
			CanSend bool `json:"can_send"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &notHeadBody))
		require.False(t, notHeadBody.CanSend, "a plain admin with no declared team should not be able to bulk-send")

		rec = doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/team-questions/send-bulk", nil, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/send-bulk/preview", nil, adminIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var headBody struct {
			CanSend bool `json:"can_send"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &headBody))
		require.True(t, headBody.CanSend, "the declared head of IT should be able to bulk-send")
	})

	var rawToken string

	t.Run("resend issues a usable token and stamps team_questions_invite_sent_at", func(t *testing.T) {
		var before models.GeneralApplication
		require.NoError(t, db.First(&before, "id = ?", applicationID).Error)
		require.Nil(t, before.TeamQuestionsInviteSentAt, "should not be set before the first invite is sent")

		var beforeCount int64
		db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&beforeCount)

		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+applicationID+"/team-questions/resend", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var afterCount int64
		db.Model(&models.TeamQuestionsToken{}).Where("application_id = ?", applicationID).Count(&afterCount)
		require.Equal(t, beforeCount+1, afterCount)

		var after models.GeneralApplication
		require.NoError(t, db.First(&after, "id = ?", applicationID).Error)
		require.NotNil(t, after.TeamQuestionsInviteSentAt, "sending the first invite should stamp team_questions_invite_sent_at")
	})

	t.Run("resending supersedes the previous token — the old link stops working", func(t *testing.T) {
		rawA, hashA, err := utils.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, db.Create(&models.TeamQuestionsToken{
			ApplicationID: uuid.MustParse(applicationID),
			TokenHash:     hashA,
			ExpiresAt:     teamQuestionsNow.Add(30 * 24 * time.Hour),
		}).Error)

		// Token A works on its own.
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/team-questions/"+rawA, nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, "the newest unused token should be usable")

		// A resend (or a second manually-issued token, same effect) supersedes it.
		rawB, hashB, err := utils.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, db.Create(&models.TeamQuestionsToken{
			ApplicationID: uuid.MustParse(applicationID),
			TokenHash:     hashB,
			ExpiresAt:     teamQuestionsNow.Add(30 * 24 * time.Hour),
		}).Error)

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/team-questions/"+rawA, nil, nil)
		require.Equal(t, http.StatusNotFound, rec.Code, "the old link must stop working once a newer one exists, even though it was never used or expired")

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/team-questions/"+rawB, nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, "the newest token should still work")

		// This subtest alone used 3 of the 5 requests allowed per minute —
		// reset so the rest of the test isn't spuriously rate-limited.
		resetRateLimit()
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
			ExpiresAt:     teamQuestionsNow.Add(30 * 24 * time.Hour),
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
			"answers":         map[string]any{"Development": map[string]string{devMotivationID.String(): ""}},
			"withdrawn_teams": []string{},
		}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid submission withdraws a team and unlocks claiming", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/team-questions/"+rawToken, map[string]any{
			"answers": map[string]any{
				"Development": map[string]string{
					devMotivationID.String(): "I want to ship real products with a team.",
					devStackID.String():      "Go, TypeScript",
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

	// Regression test: a nil Go slice (the zero value of effectiveTeams when
	// every team is withdrawn) serializes as SQL NULL via pq.StringArray,
	// which violates the teams column's NOT NULL constraint — this used to
	// 500 instead of actually withdrawing the application.
	t.Run("withdrawing every team withdraws the application, not a 500", func(t *testing.T) {
		withdrawAllID := uuid.New()
		withdrawAllApp := models.GeneralApplication{
			Id:                    withdrawAllID,
			ApplicationYear:       generalApplicationYear,
			FirstName:             "Withdraw",
			LastName:              "Everything",
			Email:                 fmt.Sprintf("withdraw-all-%s@example.com", suffix),
			EmailNormalized:       fmt.Sprintf("withdraw-all-%s@example.com", suffix),
			Gender:                "Prefer not to say",
			University:            "KTH Royal Institute of Technology",
			Programme:             "Computer Science",
			GraduationYear:        2027,
			LinkedinURL:           "https://linkedin.com/in/withdraw-all-" + suffix,
			ResumeFileName:        "resume.pdf",
			ResumeContentType:     "application/pdf",
			Teams:                 pq.StringArray{"Development", "Growth"},
			TeamPreferencesRanked: true,
			Interests:             pq.StringArray{"Machine Learning"},
			Availability:          "4-6 hours",
			Contribution:          "Applying broadly across a couple of teams.",
			DataRetentionConsent:  true,
			Status:                models.GeneralApplicationStatusPending,
		}
		require.NoError(t, db.Create(&withdrawAllApp).Error)
		t.Cleanup(func() {
			db.Where("application_id = ?", withdrawAllID).Delete(&models.TeamQuestionsToken{})
			db.Unscoped().Delete(&withdrawAllApp)
		})

		raw, hash, err := utils.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, db.Create(&models.TeamQuestionsToken{
			ApplicationID: withdrawAllID,
			TokenHash:     hash,
			ExpiresAt:     teamQuestionsNow.Add(30 * 24 * time.Hour),
		}).Error)

		resetRateLimit()
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/team-questions/"+raw, map[string]any{
			"answers":         map[string]any{},
			"withdrawn_teams": []string{"Development", "Growth"},
		}, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var after models.GeneralApplication
		require.NoError(t, db.First(&after, "id = ?", withdrawAllID).Error)
		require.Equal(t, models.GeneralApplicationStatusWithdrawn, after.Status)
		require.Empty(t, []string(after.Teams))
	})

	t.Run("recruitment period settings are public to read, admin-only to write, and enforced on submission", func(t *testing.T) {
		t.Cleanup(func() {
			db.Unscoped().Where("1 = 1").Delete(&models.GeneralApplicationSettings{})
		})

		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/settings", nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, "the deadline must be readable with no auth")
		var publicBody struct {
			SubmissionDeadline time.Time `json:"submission_deadline"`
			ClosedHeading      string    `json:"closed_heading"`
			ClosedMessage      string    `json:"closed_message"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &publicBody))
		require.False(t, publicBody.SubmissionDeadline.IsZero(), "an unconfigured deadline must still fall back to a default, not a zero time")
		require.NotEmpty(t, publicBody.ClosedHeading, "unconfigured closed-screen copy must still fall back to a default")
		require.NotEmpty(t, publicBody.ClosedMessage, "unconfigured closed-screen copy must still fall back to a default")

		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings",
			map[string]any{"submission_deadline": publicBody.SubmissionDeadline}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "an unauthenticated caller must not be able to change the deadline")

		future := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings", map[string]any{
			"submission_deadline": future,
			"closed_heading":      "Thanks for applying!",
			"closed_message":      "Custom closed-page copy for this test.",
		}, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "any admin, not just IT, may set the recruitment deadline")
		var updateBody struct {
			SubmissionDeadline time.Time `json:"submission_deadline"`
			ClosedHeading      string    `json:"closed_heading"`
			ClosedMessage      string    `json:"closed_message"`
			UpdatedByEmail     string    `json:"updated_by_email"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updateBody))
		require.True(t, future.Equal(updateBody.SubmissionDeadline))
		require.Equal(t, "Thanks for applying!", updateBody.ClosedHeading)
		require.Equal(t, "Custom closed-page copy for this test.", updateBody.ClosedMessage)
		require.Equal(t, adminA.email, updateBody.UpdatedByEmail)

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/settings", nil, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &publicBody))
		require.True(t, future.Equal(publicBody.SubmissionDeadline), "the public endpoint must reflect the saved value, not the default")
		require.Equal(t, "Thanks for applying!", publicBody.ClosedHeading)
		require.Equal(t, "Custom closed-page copy for this test.", publicBody.ClosedMessage)

		// Clearing the copy resets it to the default rather than saving an
		// empty heading/message — same convention as the Team Questions
		// email template fields.
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings", map[string]any{
			"submission_deadline": future,
			"closed_heading":      "",
			"closed_message":      "",
		}, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updateBody))
		require.NotEmpty(t, updateBody.ClosedHeading, "an empty heading must resolve back to the default, not save as blank")
		require.NotEmpty(t, updateBody.ClosedMessage, "an empty message must resolve back to the default, not save as blank")

		past := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings",
			map[string]any{"submission_deadline": past}, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		rec = doMultipartRequest(t, engine, "POST", "/api/v1/applications/general", map[string]string{
			"firstName":            "Late",
			"lastName":             "Applicant",
			"email":                fmt.Sprintf("lifecycle-late-%s@example.com", suffix),
			"gender":               "Prefer not to say",
			"university":           "KTH Royal Institute of Technology",
			"programme":            "Computer Science",
			"graduationYear":       "2027",
			"linkedinUrl":          "https://linkedin.com/in/lifecycle-late-" + suffix,
			"availability":         "4-6 hours",
			"contribution":         "I will contribute meaningfully to this team over the coming year.",
			"dataRetentionConsent": "true",
		}, map[string][]string{
			"teams":     {"Development"},
			"interests": {"Machine Learning"},
		})
		require.Equal(t, http.StatusForbidden, rec.Code, "submissions must be rejected once the configured deadline has passed")
	})

	t.Run("team question CRUD is scoped to that team's own admins", func(t *testing.T) {
		// Clean slate for IT regardless of what the dev seeder left behind,
		// so this test's assertions about count/order aren't at the mercy of
		// ambient data in a shared local database.
		require.NoError(t, db.Where("team = ?", "IT").Delete(&models.TeamQuestion{}).Error)

		// Only a member of the team may create a question for it.
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/team-questions/questions", map[string]any{
			"team": "IT", "text": "Should be rejected", "required": true,
		}, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)

		rec = doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/team-questions/questions", map[string]any{
			"team": "IT", "text": "First question", "required": true,
		}, adminIT.cookie)
		require.Equal(t, http.StatusCreated, rec.Code)
		var first struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
		require.NotEmpty(t, first.ID)

		rec = doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/team-questions/questions", map[string]any{
			"team": "IT", "text": "Second question", "required": false,
		}, adminIT.cookie)
		require.Equal(t, http.StatusCreated, rec.Code)
		var second struct {
			ID       string `json:"id"`
			Required bool   `json:"required"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &second))
		// Regression check: a bool column with a gorm "default:true" tag makes
		// GORM silently substitute the DB default whenever the Go zero value
		// (false) is set, so an explicit "required: false" would come back as
		// true. Assert both the response and the persisted row directly.
		require.False(t, second.Required, "an explicit required:false must not be coerced to true")
		var secondPersisted models.TeamQuestion
		require.NoError(t, db.First(&secondPersisted, "id = ?", second.ID).Error)
		require.False(t, secondPersisted.Required, "required:false must round-trip through the database as false")

		// The list endpoint shows can_edit true only for the team the
		// requester actually belongs to — no IT-wide override, unlike the
		// shared email template.
		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/questions", nil, adminIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var listedByIT struct {
			Teams map[string]struct {
				CanEdit   bool `json:"can_edit"`
				Questions []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"questions"`
			} `json:"teams"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listedByIT))
		require.True(t, listedByIT.Teams["IT"].CanEdit)
		require.False(t, listedByIT.Teams["Business"].CanEdit)
		require.Len(t, listedByIT.Teams["IT"].Questions, 2)
		require.Equal(t, "First question", listedByIT.Teams["IT"].Questions[0].Text)

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/questions", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var listedByA struct {
			Teams map[string]struct {
				CanEdit bool `json:"can_edit"`
			} `json:"teams"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listedByA))
		require.False(t, listedByA.Teams["IT"].CanEdit, "an admin on no team must not be able to edit IT's questions")

		// Only a member of the team may edit or delete its questions.
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/questions/"+first.ID, map[string]any{
			"text": "Should be rejected", "required": true,
		}, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)

		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/questions/"+first.ID, map[string]any{
			"text": "First question, edited", "required": true,
		}, adminIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var editedQuestion models.TeamQuestion
		require.NoError(t, db.First(&editedQuestion, "id = ?", first.ID).Error)
		require.Equal(t, "First question, edited", editedQuestion.Text)

		// Reordering (put the second question first) is also team-gated and
		// applies in one call.
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/questions/reorder", map[string]any{
			"team":        "IT",
			"ordered_ids": []string{second.ID, first.ID},
		}, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)

		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/questions/reorder", map[string]any{
			"team":        "IT",
			"ordered_ids": []string{second.ID, first.ID},
		}, adminIT.cookie)
		require.Equal(t, http.StatusNoContent, rec.Code)

		var reorderedFirst, reorderedSecond models.TeamQuestion
		require.NoError(t, db.First(&reorderedFirst, "id = ?", second.ID).Error)
		require.NoError(t, db.First(&reorderedSecond, "id = ?", first.ID).Error)
		require.Less(t, reorderedFirst.SortOrder, reorderedSecond.SortOrder, "the second question should now sort before the first")

		rec = doJSONRequest(t, engine, "DELETE", "/api/v1/applications/admin/team-questions/questions/"+first.ID, nil, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)

		rec = doJSONRequest(t, engine, "DELETE", "/api/v1/applications/admin/team-questions/questions/"+first.ID, nil, adminIT.cookie)
		require.Equal(t, http.StatusNoContent, rec.Code)

		var remaining int64
		db.Model(&models.TeamQuestion{}).Where("team = ?", "IT").Count(&remaining)
		require.EqualValues(t, 1, remaining)

		t.Cleanup(func() {
			db.Where("team = ?", "IT").Unscoped().Delete(&models.TeamQuestion{})
		})
	})

	t.Run("team questions deadline overrides are IT-only to write, fall back to defaults, and are readable by any admin", func(t *testing.T) {
		t.Cleanup(func() {
			db.Unscoped().Where("1 = 1").Delete(&models.TeamQuestionsSettings{})
			// Restore the shared handler's cache to defaults so no later run
			// (or a re-run of this test) inherits this subtest's override.
			teamQuestionsHandler.applyDeadlineCache(models.TeamQuestionsSettings{})
		})

		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/template", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "any admin may read the template and deadline settings")
		var readBody struct {
			FinalCallStart           time.Time  `json:"final_call_start"`
			SubmissionCutoff         time.Time  `json:"submission_cutoff"`
			FinalCallStartOverride   *time.Time `json:"final_call_start_override"`
			SubmissionCutoffOverride *time.Time `json:"submission_cutoff_override"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &readBody))
		require.True(t, defaultTeamQuestionsFinalCallStart.Equal(readBody.FinalCallStart), "unconfigured must resolve to the hardcoded default final call start")
		require.True(t, defaultTeamQuestionsSubmissionCutoff.Equal(readBody.SubmissionCutoff), "unconfigured must resolve to the hardcoded default submission cutoff")
		require.Nil(t, readBody.FinalCallStartOverride)
		require.Nil(t, readBody.SubmissionCutoffOverride)

		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/template", map[string]any{
			"final_call_start_override":  defaultTeamQuestionsSubmissionCutoff,
			"submission_cutoff_override": defaultTeamQuestionsFinalCallStart,
		}, adminIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "a final call start on or after the cutoff must be rejected")

		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/template", map[string]any{
			"final_call_start_override":  defaultTeamQuestionsFinalCallStart,
			"submission_cutoff_override": defaultTeamQuestionsSubmissionCutoff,
		}, adminA.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "only IT may edit the deadline overrides")

		overrideStart := defaultTeamQuestionsFinalCallStart.Add(-24 * time.Hour)
		overrideCutoff := defaultTeamQuestionsSubmissionCutoff.Add(-24 * time.Hour)
		rec = doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/team-questions/template", map[string]any{
			"final_call_start_override":  overrideStart,
			"submission_cutoff_override": overrideCutoff,
		}, adminIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var updateBody struct {
			FinalCallStart           time.Time  `json:"final_call_start"`
			SubmissionCutoff         time.Time  `json:"submission_cutoff"`
			FinalCallStartOverride   *time.Time `json:"final_call_start_override"`
			SubmissionCutoffOverride *time.Time `json:"submission_cutoff_override"`
			UpdatedByEmail           string     `json:"updated_by_email"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updateBody))
		require.True(t, overrideStart.Equal(updateBody.FinalCallStart))
		require.True(t, overrideCutoff.Equal(updateBody.SubmissionCutoff))
		require.NotNil(t, updateBody.FinalCallStartOverride)
		require.True(t, overrideStart.Equal(*updateBody.FinalCallStartOverride))
		require.Equal(t, adminIT.email, updateBody.UpdatedByEmail)

		// The live handler's cache — the thing GetForm/SubmitForm/the
		// scheduler actually read — must reflect the saved override
		// immediately, since AdminUpdateTemplate refreshes it explicitly.
		require.True(t, overrideStart.Equal(teamQuestionsHandler.effectiveFinalCallStart()))
		require.True(t, overrideCutoff.Equal(teamQuestionsHandler.effectiveSubmissionCutoff()))

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/template", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &readBody))
		require.True(t, overrideStart.Equal(readBody.FinalCallStart), "the read endpoint must reflect the saved override, not the default")
	})

	t.Run("delivery events are readable by any admin and reflect earlier sends", func(t *testing.T) {
		// Earlier subtests (the resend above) already triggered at least one
		// recorded send outcome; this just confirms it surfaced.
		rec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/delivery-events", nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "any admin, not just IT, may read delivery events")
		var body struct {
			Events []struct {
				ApplicationID string `json:"application_id"`
				Kind          string `json:"kind"`
				Outcome       string `json:"outcome"`
				Automatic     bool   `json:"automatic"`
			} `json:"events"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.NotEmpty(t, body.Events, "the earlier resend should have recorded a delivery event")

		var sawInviteSent bool
		for _, event := range body.Events {
			if event.ApplicationID == applicationID && event.Kind == "invite" && event.Outcome == "sent" {
				sawInviteSent = true
			}
		}
		require.True(t, sawInviteSent, "the earlier admin resend should show up as a sent invite event")

		rec = doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/team-questions/delivery-events", nil, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "an unauthenticated caller must not see delivery events")
	})
}

type testAdmin struct {
	email  string
	userID uuid.UUID
	cookie *http.Cookie
}

func mustCreateAdmin(t *testing.T, db *gorm.DB, cfg *config.Config, email string) testAdmin {
	return mustCreateTeamAdmin(t, db, cfg, email, "")
}

// mustCreateTeamAdmin creates an admin declared as the head of adminTeam
// (via Profile.AdminTeam, the same field requesterIsOnTeam checks), so tests
// can exercise per-team permission gating like the Team Questions CRUD
// endpoints. Pass "" for a plain admin belonging to no team.
func mustCreateTeamAdmin(t *testing.T, db *gorm.DB, cfg *config.Config, email string, adminTeam string) testAdmin {
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
		AdminTeam:              adminTeam,
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
