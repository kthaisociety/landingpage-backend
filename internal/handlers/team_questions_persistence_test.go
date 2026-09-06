package handlers

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestTeamQuestionsFinalCallMarkerSurvivesOrdinarySaves(t *testing.T) {
	db, _ := newTeamQuestionsSQL(t)
	stale := models.GeneralApplication{
		Model:           gorm.Model{ID: 42},
		Id:              uuid.New(),
		ApplicationYear: 2026,
		Email:           "applicant@example.com",
		Status:          models.GeneralApplicationStatusPending,
		// A handler loaded this record before a final call was recorded.
		TeamQuestionsFinalCallSentAt: nil,
	}
	dryRun := db.Session(&gorm.Session{DryRun: true, SkipDefaultTransaction: true})
	saved := dryRun.Save(&stale)
	require.NoError(t, saved.Error)
	require.True(t, strings.HasPrefix(saved.Statement.SQL.String(), "UPDATE "))
	require.NotContains(t, saved.Statement.SQL.String(), "team_questions_final_call_sent_at")

	field := saved.Statement.Schema.LookUpField("team_questions_final_call_sent_at")
	require.NotNil(t, field)
	require.True(t, field.Readable, "successful markers must still load from the database")
	require.False(t, field.IgnoreMigration, "AutoMigrate must create the nullable column")

	// Save's fallback upsert must also leave an existing success marker alone.
	upsert := dryRun.Clauses(clause.OnConflict{UpdateAll: true}).Create(&stale)
	require.NoError(t, upsert.Error)
	_, updates, found := strings.Cut(upsert.Statement.SQL.String(), "DO UPDATE SET ")
	require.True(t, found)
	require.NotContains(t, updates, "team_questions_final_call_sent_at")
}

func TestTeamQuestionsReminderQueryUsesSevenDayCutoff(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, teamQuestionsInviteTZ)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
	h.now = func() time.Time { return now }
	h.sendFinalCall = func(models.GeneralApplication, string, string, string) error {
		t.Fatal("recipient lookup must not send email")
		return nil
	}
	script.add(tqSQLStep{
		kind: "query",
		contains: []string{
			"application_year =", "status =", "team_questions_invite_sent_at IS NOT NULL",
			"team_questions_invite_sent_at <=", "team_questions_reminder_sent_at IS NULL",
		},
		columns: []string{"id"},
		check: func(_ string, args []driver.NamedValue) {
			require.Len(t, args, 3)
			require.EqualValues(t, 2026, args[0].Value)
			require.Equal(t, "pending", args[1].Value)
			require.Equal(t, now.Add(-7*24*time.Hour), args[2].Value)
		},
	})
	applications, err := h.pendingApplicationsNeedingReminder()
	require.NoError(t, err)
	require.Empty(t, applications)
}

// These helpers return SQL responses only; they never implement eligibility
// rules themselves, so handler changes cannot silently change the test oracle.
func tqFormTokenStep(applicationID uuid.UUID) tqSQLStep {
	return tqSQLStep{
		kind: "query", contains: []string{`FROM "team_questions_tokens"`, "token_hash =", "used_at IS NULL", "expires_at >"},
		columns: []string{"id", "application_id", "token_hash", "expires_at", "used_at"},
		rows:    [][]driver.Value{{int64(11), applicationID.String(), utils.HashToken("test-form-token"), defaultTeamQuestionsSubmissionCutoff, nil}},
	}
}

func tqFormNewerTokenStep(count int64) tqSQLStep {
	return tqSQLStep{
		kind: "query", contains: []string{`FROM "team_questions_tokens"`, "application_id =", "id >"},
		columns: []string{"count"}, rows: [][]driver.Value{{count}},
	}
}

func tqFormApplicationStep(applicationID uuid.UUID, locked bool) tqSQLStep {
	fragments := []string{`FROM "general_applications"`}
	if locked {
		fragments = append(fragments, "FOR UPDATE")
	}
	return tqSQLStep{
		kind: "query", contains: fragments,
		columns: []string{"id", "application_year", "status", "first_name", "email", "teams"},
		rows:    [][]driver.Value{{applicationID.String(), int64(2026), "pending", "Ada", "ada@example.com", `{"Development"}`}},
	}
}

func tqFormQuestionsStep() tqSQLStep {
	return tqSQLStep{
		kind: "query", contains: []string{`FROM "team_questions"`, "team IN"},
		columns: []string{"id", "team", "text", "required", "sort_order"},
	}
}

func tqFormRequest(t *testing.T, h *TeamQuestionsHandler, method string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	// Register the handlers directly: rate-limit/auth middleware would require
	// Redis or signing credentials, which these tests deliberately do not use.
	engine.GET("/team-questions/:token", h.GetForm)
	engine.POST("/team-questions/:token", h.SubmitForm)
	request := httptest.NewRequest(method, "https://example.com/team-questions/test-form-token", strings.NewReader(`{"answers":{"Development":{}}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestTeamQuestionsGetFormAcceptsValidLinkThroughSeptember8(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, time.September, 7, 9, 59, 59, 0, teamQuestionsInviteTZ),
		time.Date(2026, time.September, 7, 10, 0, 0, 0, teamQuestionsInviteTZ),
		time.Date(2026, time.September, 8, 23, 59, 59, 0, teamQuestionsInviteTZ),
	} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			db, script := newTeamQuestionsSQL(t)
			h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
			h.now = func() time.Time { return now }
			h.sendFinalCall = func(models.GeneralApplication, string, string, string) error {
				t.Fatal("form access must not send email")
				return nil
			}
			id := uuid.New()
			script.add(tqFormTokenStep(id), tqFormNewerTokenStep(0), tqFormApplicationStep(id, false), tqFormQuestionsStep())
			response := tqFormRequest(t, h, http.MethodGet)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), `"first_name":"Ada"`)
		})
	}
}

func TestTeamQuestionsSubmitRechecksTimeAndTokenUnderLock(t *testing.T) {
	for _, scenario := range []string{"cutoff while waiting for lock", "token superseded while waiting", "cutoff during writes", "accepted just before cutoff"} {
		t.Run(scenario, func(t *testing.T) {
			db, script := newTeamQuestionsSQL(t)
			var closed atomic.Bool
			before := time.Date(2026, time.September, 8, 23, 59, 59, 0, teamQuestionsInviteTZ)
			h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
			h.now = func() time.Time {
				if closed.Load() {
					return defaultTeamQuestionsSubmissionCutoff
				}
				return before
			}
			h.sendFinalCall = func(models.GeneralApplication, string, string, string) error {
				t.Error("submissions must not send final calls")
				return nil
			}
			id := uuid.New()
			script.add(tqFormTokenStep(id), tqFormNewerTokenStep(0), tqFormApplicationStep(id, false), tqFormQuestionsStep(), tqSQLStep{kind: "begin"})
			locked := tqFormApplicationStep(id, true)
			wantStatus := http.StatusGone
			if scenario == "cutoff while waiting for lock" {
				locked.after = func() { closed.Store(true) }
				script.add(locked, tqSQLStep{kind: "rollback"})
			} else if scenario == "token superseded while waiting" {
				wantStatus = http.StatusNotFound
				script.add(locked, tqFormTokenStep(id), tqFormNewerTokenStep(1), tqSQLStep{kind: "rollback"})
			} else {
				script.add(locked, tqFormTokenStep(id), tqFormNewerTokenStep(0),
					tqSQLStep{kind: "query", contains: []string{`INSERT INTO "team_questions_submissions"`}, columns: []string{"id"}, rows: [][]driver.Value{{int64(21)}}},
					tqSQLStep{kind: "exec", contains: []string{`UPDATE "general_applications"`, `"status"=`, `"teams"=`}, affected: 1})
				used := tqSQLStep{kind: "exec", contains: []string{`UPDATE "team_questions_tokens"`, `"used_at"=`}, affected: 1}
				if scenario == "cutoff during writes" {
					used.after = func() { closed.Store(true) }
					script.add(used, tqSQLStep{kind: "rollback"})
				} else {
					wantStatus = http.StatusOK
					// Commit completes at midnight. The accepted submission remains
					// successful and its asynchronous confirmation still fires
					// regardless of the clock; this test never provides a
					// sendConfirmation override, so it falls through to the
					// package-level sender, which no-ops against the test's nil
					// defaultMailer instead of reaching a real email sender.
					script.add(used, tqSQLStep{kind: "commit", after: func() { closed.Store(true) }})
				}
			}
			response := tqFormRequest(t, h, http.MethodPost)
			require.Equal(t, wantStatus, response.Code, response.Body.String())
			if wantStatus == http.StatusOK {
				require.JSONEq(t, `{"status":"available"}`, response.Body.String())
			}
		})
	}
}
