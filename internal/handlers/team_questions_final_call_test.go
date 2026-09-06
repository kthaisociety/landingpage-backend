package handlers

import (
	"database/sql/driver"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// All database responses are scripted in memory. Every sender is replaced;
// these tests neither configure credentials nor contact an email service.
func newTQFinalHandler(t *testing.T) (*TeamQuestionsHandler, *tqSQLScript) {
	t.Helper()
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
	h.now = func() time.Time { return teamQuestionsFinalCallStart.Add(time.Hour) }
	h.sendFinalCall = func(models.GeneralApplication, string, string, string) error {
		t.Fatal("unexpected final-call send")
		return nil
	}
	return h, script
}

func tqFinalColumns() []string {
	return []string{"id", "application_year", "status", "email", "first_name", "last_name", "teams", "team_questions_final_call_sent_at"}
}

func tqFinalRow(id uuid.UUID, year int, status models.GeneralApplicationStatus, stamp driver.Value) []driver.Value {
	return []driver.Value{id.String(), int64(year), string(status), "applicant@example.com", "Alex", "Applicant", "{Development}", stamp}
}

func tqFinalLockedApplication(t *testing.T, id uuid.UUID, row []driver.Value) tqSQLStep {
	t.Helper()
	step := tqSQLStep{
		kind: "query", contains: []string{`FROM "general_applications"`, "FOR UPDATE"},
		columns: tqFinalColumns(),
		check: func(_ string, args []driver.NamedValue) {
			require.NotEmpty(t, args)
			require.Equal(t, id.String(), args[0].Value)
		},
	}
	if row != nil {
		step.rows = [][]driver.Value{row}
	}
	return step
}

func tqFinalSubmissionCount(t *testing.T, id uuid.UUID, count int64) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "query", contains: []string{`FROM "team_questions_submissions"`, "application_id ="},
		columns: []string{"count"}, rows: [][]driver.Value{{count}},
		check: func(query string, args []driver.NamedValue) {
			require.NotContains(t, query, "deleted_at", "historical submissions must also exclude the applicant")
			require.Len(t, args, 1)
			require.Equal(t, id.String(), args[0].Value)
		},
	}
}

func tqFinalTokenInsert(t *testing.T, id uuid.UUID, insertedHash *string) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "query", contains: []string{`INSERT INTO "team_questions_tokens"`, "RETURNING"},
		columns: []string{"id"}, rows: [][]driver.Value{{int64(101)}},
		check: func(query string, args []driver.NamedValue) {
			// Read the INSERT column list so this assertion does not depend on
			// incidental GORM field order or timestamp parameter positions.
			start, end := strings.Index(query, "("), strings.Index(query, ")")
			require.Greater(t, end, start)
			columns := strings.Split(query[start+1:end], ",")
			require.Len(t, args, len(columns))
			values := make(map[string]driver.Value, len(columns))
			for i, column := range columns {
				values[strings.Trim(strings.TrimSpace(column), `"`)] = args[i].Value
			}
			require.Equal(t, id.String(), values["application_id"])
			expiresAt, ok := values["expires_at"].(time.Time)
			require.True(t, ok)
			require.True(t, expiresAt.Equal(teamQuestionsSubmissionCutoff), "fresh links expire at the Stockholm cutoff")
			hash, ok := values["token_hash"].(string)
			require.True(t, ok)
			require.NotEmpty(t, hash)
			if insertedHash != nil {
				*insertedHash = hash
			}
		},
	}
}

func tqFinalStamp(t *testing.T, id uuid.UUID, at time.Time) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "exec", affected: 1,
		contains: []string{"UPDATE general_applications SET team_questions_final_call_sent_at =", "AND team_questions_final_call_sent_at IS NULL"},
		check: func(query string, args []driver.NamedValue) {
			require.NotContains(t, query, "team_questions_invite_sent_at")
			require.NotContains(t, query, "team_questions_reminder_sent_at")
			require.Len(t, args, 2)
			stamp, ok := args[0].Value.(time.Time)
			require.True(t, ok)
			require.True(t, stamp.Equal(at))
			require.Equal(t, id.String(), args[1].Value)
		},
	}
}

func TestTeamQuestionsFinalCallFailureRetryAndSuccessfulSkip(t *testing.T) {
	h, script := newTQFinalHandler(t)
	id := uuid.New()
	row := tqFinalRow(id, 2026, models.GeneralApplicationStatusPending, nil)
	var insertedHash string
	var rawTokens []string
	sendFailure := errors.New("fake delivery failure")
	h.sendFinalCall = func(application models.GeneralApplication, body, subject, link string) error {
		require.Equal(t, id, application.Id)
		require.Equal(t, "applicant@example.com", application.Email)
		parsed, err := url.Parse(link)
		require.NoError(t, err)
		require.Equal(t, "example.com", parsed.Host)
		raw := parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
		require.NotEmpty(t, raw)
		require.Equal(t, insertedHash, utils.HashToken(raw), "the emailed token must match the newly inserted hash")
		rawTokens = append(rawTokens, raw)
		renderedSubject, html, err := email.RenderTeamQuestionsInvite(application.FirstName, application.LastName, application.Teams, body, subject, link)
		require.NoError(t, err)
		require.Contains(t, renderedSubject, "FINAL CALL")
		require.Contains(t, html, "September 8, 2026")
		require.Contains(t, html, "Europe/Stockholm")
		require.Contains(t, html, "00:00 on September 9")
		require.Contains(t, html, link)
		if len(rawTokens) == 1 {
			return sendFailure
		}
		return nil
	}

	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row), tqFinalSubmissionCount(t, id, 0),
		tqFinalTokenInsert(t, id, &insertedHash), tqSQLStep{kind: "rollback"})
	delivered, err := h.issueAndSendFinalCall(id)
	require.ErrorIs(t, err, sendFailure)
	require.False(t, delivered)

	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row), tqFinalSubmissionCount(t, id, 0),
		tqFinalTokenInsert(t, id, &insertedHash), tqFinalStamp(t, id, h.now()), tqSQLStep{kind: "commit"})
	delivered, err = h.issueAndSendFinalCall(id)
	require.NoError(t, err)
	require.True(t, delivered)
	require.Len(t, rawTokens, 2)
	require.NotEqual(t, rawTokens[0], rawTokens[1], "a retry must issue a fresh token")

	// This also represents a second worker acquiring the lock after the
	// successful worker committed: its previously selected candidate is stale.
	stamped := tqFinalRow(id, 2026, models.GeneralApplicationStatusPending, h.now())
	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, stamped), tqSQLStep{kind: "commit"})
	delivered, err = h.issueAndSendFinalCall(id)
	require.NoError(t, err)
	require.False(t, delivered)
	require.Len(t, rawTokens, 2, "successful final calls must not be sent again")
}

func TestTeamQuestionsFinalCallRechecksEligibilityUnderLock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		year      int
		status    models.GeneralApplicationStatus
		missing   bool
		submitted bool
	}{
		{name: "different recruitment year", year: 2025, status: models.GeneralApplicationStatusPending},
		{name: "no longer pending", year: 2026, status: models.GeneralApplicationStatusAvailable},
		{name: "deleted application", missing: true},
		{name: "submitted then reset to pending", year: 2026, status: models.GeneralApplicationStatusPending, submitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, script := newTQFinalHandler(t)
			id := uuid.New()
			row := tqFinalRow(id, tc.year, tc.status, nil)
			if tc.missing {
				row = nil
			}
			script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row))
			if tc.submitted {
				script.add(tqFinalSubmissionCount(t, id, 1))
			}
			script.add(tqSQLStep{kind: "commit"})
			delivered, err := h.issueAndSendFinalCall(id)
			require.NoError(t, err)
			require.False(t, delivered)
		})
	}
}

func TestTeamQuestionsFinalCallDatabaseFailuresRemainUnsuccessful(t *testing.T) {
	for _, stage := range []string{"insert", "stamp", "stamp affected no row", "commit"} {
		t.Run(stage, func(t *testing.T) {
			h, script := newTQFinalHandler(t)
			id := uuid.New()
			failure := errors.New("scripted database failure")
			calls := 0
			h.sendFinalCall = func(models.GeneralApplication, string, string, string) error { calls++; return nil }
			script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, tqFinalRow(id, 2026, models.GeneralApplicationStatusPending, nil)), tqFinalSubmissionCount(t, id, 0))
			insert := tqFinalTokenInsert(t, id, nil)
			if stage == "insert" {
				insert.err = failure
				script.add(insert, tqSQLStep{kind: "rollback"})
			} else {
				stamp := tqFinalStamp(t, id, h.now())
				if stage == "stamp" {
					stamp.err = failure
				} else if stage == "stamp affected no row" {
					stamp.affected = 0
				}
				script.add(insert, stamp)
				if stage == "commit" {
					script.add(tqSQLStep{kind: "commit", err: failure})
				} else {
					script.add(tqSQLStep{kind: "rollback"})
				}
			}
			delivered, err := h.issueAndSendFinalCall(id)
			require.Error(t, err)
			if stage != "stamp affected no row" {
				require.ErrorIs(t, err, failure)
			}
			require.False(t, delivered)
			if stage == "insert" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestTeamQuestionsFinalCallCrossingCutoff(t *testing.T) {
	for _, stage := range []string{"waiting for application lock", "after token insert", "after accepted delivery"} {
		t.Run(stage, func(t *testing.T) {
			h, script := newTQFinalHandler(t)
			id := uuid.New()
			now := teamQuestionsSubmissionCutoff.Add(-time.Second)
			h.now = func() time.Time { return now }
			locked := tqFinalLockedApplication(t, id, tqFinalRow(id, 2026, models.GeneralApplicationStatusPending, nil))
			if stage == "waiting for application lock" {
				locked.after = func() { now = teamQuestionsSubmissionCutoff }
			}
			script.add(tqSQLStep{kind: "begin"}, locked)
			calls := 0
			h.sendFinalCall = func(models.GeneralApplication, string, string, string) error {
				calls++
				now = teamQuestionsSubmissionCutoff
				return nil
			}
			if stage == "waiting for application lock" {
				script.add(tqSQLStep{kind: "commit"})
			} else {
				insert := tqFinalTokenInsert(t, id, nil)
				if stage == "after token insert" {
					insert.after = func() { now = teamQuestionsSubmissionCutoff }
				}
				script.add(tqFinalSubmissionCount(t, id, 0), insert)
				if stage == "after token insert" {
					script.add(tqSQLStep{kind: "rollback"})
				} else {
					script.add(tqFinalStamp(t, id, teamQuestionsSubmissionCutoff), tqSQLStep{kind: "commit"})
				}
			}
			delivered, err := h.issueAndSendFinalCall(id)
			if stage == "after token insert" {
				require.ErrorIs(t, err, errTeamQuestionsClosed)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, stage == "after accepted delivery", delivered)
			if delivered {
				require.Equal(t, 1, calls, "accepted sends must remain stamped even when completion crosses cutoff")
			} else {
				require.Zero(t, calls)
			}
		})
	}
}

func tqFinalCandidates(t *testing.T, rows ...[]driver.Value) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "query", columns: tqFinalColumns(), rows: rows,
		contains: []string{"application_year =", "status =", "team_questions_final_call_sent_at IS NULL", "NOT EXISTS (SELECT 1 FROM team_questions_submissions WHERE application_id::text = general_applications.id)"},
		check: func(query string, args []driver.NamedValue) {
			require.Len(t, args, 2)
			require.EqualValues(t, 2026, args[0].Value)
			require.Equal(t, string(models.GeneralApplicationStatusPending), args[1].Value)
			require.NotContains(t, query, "team_questions_invite_sent_at", "never-invited applicants also need the final call")
		},
	}
}

func TestTeamQuestionsFinalCallBatchContinuesAfterFailedDelivery(t *testing.T) {
	h, script := newTQFinalHandler(t)
	firstID, secondID := uuid.New(), uuid.New()
	first := tqFinalRow(firstID, 2026, models.GeneralApplicationStatusPending, nil)
	second := tqFinalRow(secondID, 2026, models.GeneralApplicationStatusPending, nil)
	script.add(tqFinalCandidates(t, first, second),
		tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, firstID, first), tqFinalSubmissionCount(t, firstID, 0), tqFinalTokenInsert(t, firstID, nil), tqSQLStep{kind: "rollback"},
		tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, secondID, second), tqFinalSubmissionCount(t, secondID, 0), tqFinalTokenInsert(t, secondID, nil), tqFinalStamp(t, secondID, h.now()), tqSQLStep{kind: "commit"})
	var recipients []uuid.UUID
	h.sendFinalCall = func(application models.GeneralApplication, _, _, _ string) error {
		recipients = append(recipients, application.Id)
		if application.Id == firstID {
			return errors.New("fake first-recipient failure")
		}
		return nil
	}
	sent, failed, err := h.SendPendingFinalCalls()
	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, []string{firstID.String()}, failed)
	require.Equal(t, []uuid.UUID{firstID, secondID}, recipients)
}

func TestTeamQuestionsFinalCallSkipsOutsideWindowAndInDevelopment(t *testing.T) {
	for _, tc := range []struct {
		name        string
		now         time.Time
		development bool
	}{
		{name: "before final-call window", now: teamQuestionsFinalCallStart.Add(-time.Second)},
		{name: "at closure", now: teamQuestionsSubmissionCutoff},
		{name: "development does not consume final call", now: teamQuestionsFinalCallStart, development: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTQFinalHandler(t)
			h.now = func() time.Time { return tc.now }
			h.cfg.DevelopmentMode = tc.development
			sent, failed, err := h.SendPendingFinalCalls()
			require.NoError(t, err)
			require.Zero(t, sent)
			require.Empty(t, failed)
			delivered, err := h.issueAndSendFinalCall(uuid.New())
			require.NoError(t, err)
			require.False(t, delivered)
		})
	}
}
