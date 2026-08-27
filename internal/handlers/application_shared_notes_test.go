package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

// TestApplicationSharedNotes covers the per-entry shared-notes endpoints:
// listing, appending a comment, editing/deleting your own, and the
// authorship check that keeps everyone else's entries read-only. Requires
// local Postgres (via `docker compose up -d`) and a .env file; skips
// silently when absent, matching the other integration tests in this package.
func TestApplicationSharedNotes(t *testing.T) {
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
		&models.User{},
		&models.Profile{},
		&models.GeneralApplication{},
		&models.ApplicationSharedNoteEntry{},
	))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)

	adminA := mustCreateAdmin(t, db, cfg, "shared-notes-admin-a@seed.local")
	adminB := mustCreateAdmin(t, db, cfg, "shared-notes-admin-b@seed.local")

	suffix := uuid.New().String()[:8]
	applicantEmail := fmt.Sprintf("shared-notes-%s@seed.local", suffix)
	appID := uuid.New()
	require.NoError(t, db.Create(&models.GeneralApplication{
		Id:                   appID,
		ApplicationYear:      generalApplicationYear,
		FirstName:            "Shared",
		LastName:             "Notes",
		Email:                applicantEmail,
		EmailNormalized:      applicantEmail,
		Programme:            "Computer Science",
		GraduationYear:       2027,
		LinkedinURL:          "https://linkedin.com/in/shared-notes-" + suffix,
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
		db.Where("application_id = ?", appID).Unscoped().Delete(&models.ApplicationSharedNoteEntry{})
		db.Unscoped().Delete(&models.GeneralApplication{}, "id = ?", appID)
		for _, admin := range []testAdmin{adminA, adminB} {
			db.Where("email = ?", admin.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", admin.email).Unscoped().Delete(&models.User{})
		}
	})

	basePath := "/api/v1/applications/admin/" + appID.String() + "/notes/shared"

	type entryResponse struct {
		// RawID absorbs the untagged, promoted gorm.Model.ID field the
		// handler also serializes (as "ID", alongside the real "id") —
		// without it, json.Unmarshal's case-insensitive fallback matches
		// "ID" onto Id below and chokes on the type mismatch.
		RawID         uint   `json:"ID"`
		Id            string `json:"id"`
		ApplicationID string `json:"application_id"`
		AuthorID      string `json:"author_id"`
		AuthorEmail   string `json:"author_email"`
		Text          string `json:"text"`
	}

	t.Run("list starts empty", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "GET", basePath, nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Entries []entryResponse `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Empty(t, body.Entries)
	})

	var entryID string

	t.Run("create appends an entry authored by the requester", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", basePath, map[string]string{"text": "First comment"}, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var entry entryResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entry))
		require.NotEmpty(t, entry.Id)
		require.Equal(t, appID.String(), entry.ApplicationID)
		require.Equal(t, adminA.userID.String(), entry.AuthorID)
		require.Equal(t, adminA.email, entry.AuthorEmail)
		require.Equal(t, "First comment", entry.Text)
		entryID = entry.Id
	})

	t.Run("empty text is rejected", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", basePath, map[string]string{"text": "   "}, adminA.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("list returns entries oldest first, from a second author", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", basePath, map[string]string{"text": "Second comment"}, adminB.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		rec = doJSONRequest(t, engine, "GET", basePath, nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Entries []entryResponse `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Entries, 2)
		require.Equal(t, "First comment", body.Entries[0].Text)
		require.Equal(t, adminA.email, body.Entries[0].AuthorEmail)
		require.Equal(t, "Second comment", body.Entries[1].Text)
		require.Equal(t, adminB.email, body.Entries[1].AuthorEmail)
	})

	t.Run("a different admin cannot edit someone else's entry", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PUT", basePath+"/"+entryID, map[string]string{"text": "hijacked"}, adminB.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("a different admin cannot delete someone else's entry", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "DELETE", basePath+"/"+entryID, nil, adminB.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("the author can edit their own entry", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PUT", basePath+"/"+entryID, map[string]string{"text": "Edited comment"}, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var entry entryResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entry))
		require.Equal(t, "Edited comment", entry.Text)
	})

	t.Run("editing a note id from another application 404s", func(t *testing.T) {
		otherAppPath := "/api/v1/applications/admin/" + uuid.New().String() + "/notes/shared/" + entryID
		rec := doJSONRequest(t, engine, "PUT", otherAppPath, map[string]string{"text": "wrong app"}, adminA.cookie)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("the author can delete their own entry", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "DELETE", basePath+"/"+entryID, nil, adminA.cookie)
		require.Equal(t, http.StatusNoContent, rec.Code)

		rec = doJSONRequest(t, engine, "GET", basePath, nil, adminA.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			Entries []entryResponse `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Entries, 1)
		require.Equal(t, "Second comment", body.Entries[0].Text)
	})
}
