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
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestFinalizeRecruitmentPhase exercises the finalize phase end to end: no
// decision can be made while it's closed, only an IT admin can open it, any
// admin can act while it's open (with accept forced onto their own team, and
// an admin with no team unable to accept at all), and only the head of IT —
// not just any IT admin — can close it.
//
// Like TestApplicationAndInterviewLifecycle, this needs real Postgres, so it
// skips when .env isn't present.
func TestFinalizeRecruitmentPhase(t *testing.T) {
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
		&models.TeamMember{}, &models.FinalizeRecruitmentPhase{},
	))

	// FinalizeRecruitmentPhase is a singleton — start from a clean slate so
	// this test's "closed by default" assumptions don't depend on whatever
	// state a previous run (or a real admin) left it in.
	require.NoError(t, db.Exec("DELETE FROM finalize_recruitment_phases").Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)

	plainAdmin := mustCreateAdmin(t, db, cfg, "finalize-admin-plain@seed.local")
	itAdmin := mustCreateTeamAdmin(t, db, cfg, "finalize-admin-it@seed.local", "IT")
	businessHead := mustCreateTeamAdmin(t, db, cfg, "finalize-admin-business@seed.local", "Business")

	// On the IT team (so they can open the phase) but not its declared head
	// (so they must not be able to close it).
	itMember := mustCreateAdmin(t, db, cfg, "finalize-admin-it-member@seed.local")
	require.NoError(t, db.Create(&models.TeamMember{
		UserID:               mustFindUserPK(t, db, itMember.email),
		TeamMemberDepartment: "IT",
		AcademicYear:         "2025/2026",
	}).Error)

	t.Cleanup(func() {
		emails := []string{plainAdmin.email, itAdmin.email, businessHead.email, itMember.email}
		db.Where("email IN ?", emails).Unscoped().Delete(&models.Profile{})
		db.Where("email IN ?", emails).Unscoped().Delete(&models.User{})
		db.Exec("DELETE FROM finalize_recruitment_phases")
	})

	newInterviewingApplication := func(t *testing.T) uuid.UUID {
		t.Helper()
		id := uuid.New()
		app := models.GeneralApplication{
			Id:                id,
			ApplicationYear:   generalApplicationYear,
			FirstName:         "Finalize",
			LastName:          "Candidate",
			Email:             fmt.Sprintf("finalize-candidate-%s@seed.local", id.String()[:8]),
			EmailNormalized:   fmt.Sprintf("finalize-candidate-%s@seed.local", id.String()[:8]),
			Programme:         "Computer Science",
			GraduationYear:    2027,
			LinkedinURL:       "https://linkedin.com/in/finalize-candidate",
			ResumeFileName:    "resume.pdf",
			ResumeContentType: "application/pdf",
			Teams:             []string{"Development"},
			Availability:      "4-6 hours",
			Contribution:      "Contribution text.",
			Status:            models.GeneralApplicationStatusInterviewing,
		}
		require.NoError(t, db.Create(&app).Error)
		t.Cleanup(func() {
			db.Unscoped().Delete(&app)
		})
		return id
	}

	newApplicationWithStatus := func(t *testing.T, status models.GeneralApplicationStatus, interviewedBy []string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		app := models.GeneralApplication{
			Id:                id,
			ApplicationYear:   generalApplicationYear,
			FirstName:         "Finalize",
			LastName:          "Candidate",
			Email:             fmt.Sprintf("finalize-candidate-%s@seed.local", id.String()[:8]),
			EmailNormalized:   fmt.Sprintf("finalize-candidate-%s@seed.local", id.String()[:8]),
			Programme:         "Computer Science",
			GraduationYear:    2027,
			LinkedinURL:       "https://linkedin.com/in/finalize-candidate",
			ResumeFileName:    "resume.pdf",
			ResumeContentType: "application/pdf",
			Teams:             []string{"Development"},
			Availability:      "4-6 hours",
			Contribution:      "Contribution text.",
			Status:            status,
			InterviewedBy:     interviewedBy,
		}
		require.NoError(t, db.Create(&app).Error)
		t.Cleanup(func() {
			db.Unscoped().Delete(&app)
		})
		return id
	}

	t.Run("decision is rejected before the phase is open", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, businessHead.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("only IT admins can open the phase", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/open",
			map[string]string{"confirm": finalizePhaseOpenConfirmPhrase}, plainAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("opening requires the exact confirmation phrase", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/open",
			map[string]string{"confirm": "yes please"}, itAdmin.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)

		statusRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/phase", nil, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, statusRec.Code)
		var status models.FinalizeRecruitmentPhase
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
		require.False(t, status.IsOpen())
	})

	t.Run("an IT team member who isn't its head can also open the phase", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/open",
			map[string]string{"confirm": finalizePhaseOpenConfirmPhrase}, itMember.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		statusRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/phase", nil, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, statusRec.Code)
		var status models.FinalizeRecruitmentPhase
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
		require.True(t, status.IsOpen())
		require.Equal(t, itMember.email, status.OpenedByEmail)
	})

	t.Run("an admin with no team cannot accept", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "accepted"}, plainAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("an admin with no team can still reject", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", appID).Error)
		require.Equal(t, models.GeneralApplicationStatusRejected, app.Status)
		require.Equal(t, plainAdmin.email, app.FinalizedByEmail)
	})

	t.Run("the head of a team accepts onto their own team, ignoring any client-supplied team", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			// Even if a client sent assigned_team, the server must ignore it —
			// there is no such field in the request contract anymore.
			map[string]any{"decision": "accepted", "assigned_team": "IT"}, businessHead.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", appID).Error)
		require.Equal(t, models.GeneralApplicationStatusAccepted, app.Status)
		require.Equal(t, "Business", app.AssignedTeam)
		require.Equal(t, businessHead.email, app.FinalizedByEmail)
		require.NotNil(t, app.FinalizedAt)
	})

	t.Run("an available applicant who was already interviewed can be finalized", func(t *testing.T) {
		appID := newApplicationWithStatus(t, models.GeneralApplicationStatusAvailable, []string{"some-interviewer@seed.local"})
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var app models.GeneralApplication
		require.NoError(t, db.First(&app, "id = ?", appID).Error)
		require.Equal(t, models.GeneralApplicationStatusRejected, app.Status)
	})

	t.Run("an available applicant who was never interviewed cannot be finalized", func(t *testing.T) {
		appID := newApplicationWithStatus(t, models.GeneralApplicationStatusAvailable, nil)
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, plainAdmin.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("accepted applications are visible through the admin list status filter", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		acceptRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "accepted"}, businessHead.cookie)
		require.Equal(t, http.StatusOK, acceptRec.Code)

		listRec := doJSONRequest(t, engine, "GET",
			fmt.Sprintf("/api/v1/applications/admin?year=%d&status=accepted", generalApplicationYear), nil, businessHead.cookie)
		require.Equal(t, http.StatusOK, listRec.Code)

		var listed []models.GeneralApplication
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listed))
		found := false
		for _, app := range listed {
			require.Equal(t, models.GeneralApplicationStatusAccepted, app.Status)
			if app.Id == appID {
				found = true
			}
		}
		require.True(t, found, "accepted application should appear in the status=accepted list")
	})

	t.Run("an application already decided cannot be finalized again", func(t *testing.T) {
		appID := newInterviewingApplication(t)
		first := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, first.Code)

		second := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "accepted"}, businessHead.cookie)
		require.Equal(t, http.StatusBadRequest, second.Code)
	})

	t.Run("an IT team member who isn't its head cannot close the phase", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/close",
			map[string]string{"confirm": finalizePhaseCloseConfirmPhrase}, itMember.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("closing requires the exact confirmation phrase", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/close",
			map[string]string{"confirm": "yes please"}, itAdmin.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	// requesterIsHeadOfTeam(db, userID, "IT") also accepts a verified
	// Profile.IsHeadOfIT, on top of the self-declared AdminTeam == "IT" it
	// already checked — a real head of IT (granted via OffboardingHandler)
	// shouldn't have to separately self-declare "IT" to close this phase
	// too. Declares "Marketing" specifically to prove this isn't just
	// falling back to the self-declared check.
	t.Run("a verified head of IT can close it even without self-declaring the IT team", func(t *testing.T) {
		verifiedHead := mustCreateTeamAdmin(t, db, cfg, "finalize-admin-verified-head@seed.local", "Marketing")
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", verifiedHead.email).Update("is_head_of_it", true).Error)
		t.Cleanup(func() {
			db.Where("email = ?", verifiedHead.email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", verifiedHead.email).Unscoped().Delete(&models.User{})
		})

		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/close",
			map[string]string{"confirm": finalizePhaseCloseConfirmPhrase}, verifiedHead.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		// Reopen so the remaining subtests still see their expected state.
		reopenRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/open",
			map[string]string{"confirm": finalizePhaseOpenConfirmPhrase}, itAdmin.cookie)
		require.Equal(t, http.StatusOK, reopenRec.Code)
	})

	t.Run("the head of IT closes the phase, then decisions are blocked again", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/close",
			map[string]string{"confirm": finalizePhaseCloseConfirmPhrase}, itAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		statusRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/phase", nil, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, statusRec.Code)
		var status models.FinalizeRecruitmentPhase
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
		require.False(t, status.IsOpen())
		require.Equal(t, itAdmin.email, status.ClosedByEmail)

		appID := newInterviewingApplication(t)
		decisionRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/"+appID.String()+"/finalize",
			map[string]string{"decision": "rejected"}, plainAdmin.cookie)
		require.Equal(t, http.StatusForbidden, decisionRec.Code)
	})
}

// TestSendRejectionsBulk exercises the end-of-cycle bulk rejection sweep:
// blocked until the finalize phase has been closed, restricted to the head
// of IT, and — the actual point of the feature — reaches applications
// AdminFinalizeDecision itself never could (never interviewed, or marked
// ineligible), while leaving accepted and withdrawn applications alone.
// Needs real Postgres, so it skips when .env isn't present, like its
// siblings above.
func TestSendRejectionsBulk(t *testing.T) {
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
		&models.TeamMember{}, &models.FinalizeRecruitmentPhase{}, &models.GeneralApplicationSettings{},
	))

	require.NoError(t, db.Exec("DELETE FROM finalize_recruitment_phases").Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)

	itAdmin := mustCreateTeamAdmin(t, db, cfg, "bulk-reject-admin-it@seed.local", "IT")
	plainAdmin := mustCreateAdmin(t, db, cfg, "bulk-reject-admin-plain@seed.local")
	t.Cleanup(func() {
		emails := []string{itAdmin.email, plainAdmin.email}
		db.Where("email IN ?", emails).Unscoped().Delete(&models.Profile{})
		db.Where("email IN ?", emails).Unscoped().Delete(&models.User{})
		db.Exec("DELETE FROM finalize_recruitment_phases")
	})

	newApplication := func(t *testing.T, status models.GeneralApplicationStatus, rejectionEmailSentAt *time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		app := models.GeneralApplication{
			Id:                   id,
			ApplicationYear:      generalApplicationYear,
			FirstName:            "Bulk",
			LastName:             "Candidate",
			Email:                fmt.Sprintf("bulk-reject-candidate-%s@seed.local", id.String()[:8]),
			EmailNormalized:      fmt.Sprintf("bulk-reject-candidate-%s@seed.local", id.String()[:8]),
			Programme:            "Computer Science",
			GraduationYear:       2027,
			LinkedinURL:          "https://linkedin.com/in/bulk-reject-candidate",
			ResumeFileName:       "resume.pdf",
			ResumeContentType:    "application/pdf",
			Teams:                []string{"Development"},
			Availability:         "4-6 hours",
			Contribution:         "Contribution text.",
			Status:               status,
			RejectionEmailSentAt: rejectionEmailSentAt,
		}
		require.NoError(t, db.Create(&app).Error)
		t.Cleanup(func() {
			db.Unscoped().Delete(&app)
		})
		return id
	}

	pastSend := time.Now().Add(-24 * time.Hour)
	pending := newApplication(t, models.GeneralApplicationStatusPending, nil)
	ineligible := newApplication(t, models.GeneralApplicationStatusIneligible, nil)
	rejectedRetry := newApplication(t, models.GeneralApplicationStatusRejected, nil)
	accepted := newApplication(t, models.GeneralApplicationStatusAccepted, nil)
	withdrawn := newApplication(t, models.GeneralApplicationStatusWithdrawn, nil)
	alreadyEmailed := newApplication(t, models.GeneralApplicationStatusRejected, &pastSend)

	t.Run("preview reports phase not closed and refuses to send", func(t *testing.T) {
		previewRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/rejections/preview", nil, itAdmin.cookie)
		require.Equal(t, http.StatusOK, previewRec.Code)
		var preview struct {
			Count       int  `json:"count"`
			CanSend     bool `json:"can_send"`
			PhaseClosed bool `json:"phase_closed"`
		}
		require.NoError(t, json.Unmarshal(previewRec.Body.Bytes(), &preview))
		require.False(t, preview.PhaseClosed)
		require.Equal(t, 0, preview.Count)

		sendRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/rejections/send", nil, itAdmin.cookie)
		require.Equal(t, http.StatusBadRequest, sendRec.Code)
	})

	// Open then immediately close the phase — this test only cares about it
	// having been closed at least once, not about the accept/reject flow the
	// phase otherwise gates.
	openRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/open",
		map[string]string{"confirm": finalizePhaseOpenConfirmPhrase}, itAdmin.cookie)
	require.Equal(t, http.StatusOK, openRec.Code)
	closeRec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/phase/close",
		map[string]string{"confirm": finalizePhaseCloseConfirmPhrase}, itAdmin.cookie)
	require.Equal(t, http.StatusOK, closeRec.Code)

	t.Run("preview counts every unaccepted, unemailed application once the phase is closed", func(t *testing.T) {
		previewRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/rejections/preview", nil, itAdmin.cookie)
		require.Equal(t, http.StatusOK, previewRec.Code)
		var preview struct {
			Count       int  `json:"count"`
			CanSend     bool `json:"can_send"`
			PhaseClosed bool `json:"phase_closed"`
		}
		require.NoError(t, json.Unmarshal(previewRec.Body.Bytes(), &preview))
		require.True(t, preview.PhaseClosed)
		require.True(t, preview.CanSend)
		// pending, ineligible, and rejectedRetry — accepted/withdrawn/already-emailed excluded.
		require.GreaterOrEqual(t, preview.Count, 3)

		plainPreviewRec := doJSONRequest(t, engine, "GET", "/api/v1/applications/admin/finalize/rejections/preview", nil, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, plainPreviewRec.Code)
		var plainPreview struct {
			CanSend bool `json:"can_send"`
		}
		require.NoError(t, json.Unmarshal(plainPreviewRec.Body.Bytes(), &plainPreview))
		require.False(t, plainPreview.CanSend)
	})

	t.Run("only the head of IT can trigger the send", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/rejections/send", nil, plainAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("sending targets the right applications, leaving accepted/withdrawn/already-emailed alone", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/applications/admin/finalize/rejections/send", nil, itAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		var result struct {
			Sent   int      `json:"sent"`
			Failed []string `json:"failed"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

		// No SES configured in this test environment, so every real send
		// attempt fails — what matters here is *which* applications were
		// attempted, not that the email itself went out (see
		// email_test.go's captureMailer for that).
		attempted := append([]string{}, result.Failed...)
		require.Contains(t, attempted, pending.String())
		require.Contains(t, attempted, ineligible.String())
		require.Contains(t, attempted, rejectedRetry.String())
		require.NotContains(t, attempted, accepted.String())
		require.NotContains(t, attempted, withdrawn.String())
		require.NotContains(t, attempted, alreadyEmailed.String())

		// A failed send must never be recorded as if it succeeded.
		var untouched models.GeneralApplication
		require.NoError(t, db.First(&untouched, "id = ?", pending).Error)
		require.Nil(t, untouched.RejectionEmailSentAt)
		require.Equal(t, models.GeneralApplicationStatusPending, untouched.Status)
	})
}
