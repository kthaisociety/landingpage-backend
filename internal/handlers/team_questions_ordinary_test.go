package handlers

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// tqOrdinaryTokenInsert mirrors tqFinalTokenInsert but doesn't pin an exact
// expiry: ordinary invites/reminders expire teamQuestionsTokenValidity from
// now rather than at a fixed cutoff.
func tqOrdinaryTokenInsert(t *testing.T, id uuid.UUID, insertedHash *string) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "query", contains: []string{`INSERT INTO "team_questions_tokens"`, "RETURNING"},
		columns: []string{"id"}, rows: [][]driver.Value{{int64(201)}},
		check: func(query string, args []driver.NamedValue) {
			start, end := strings.Index(query, "("), strings.Index(query, ")")
			require.Greater(t, end, start)
			columns := strings.Split(query[start+1:end], ",")
			require.Len(t, args, len(columns))
			values := make(map[string]driver.Value, len(columns))
			for i, column := range columns {
				values[strings.Trim(strings.TrimSpace(column), `"`)] = args[i].Value
			}
			require.Equal(t, id.String(), values["application_id"])
			_, ok := values["expires_at"].(time.Time)
			require.True(t, ok)
			hash, ok := values["token_hash"].(string)
			require.True(t, ok)
			require.NotEmpty(t, hash)
			if insertedHash != nil {
				*insertedHash = hash
			}
		},
	}
}

func tqOrdinaryTokenDelete() tqSQLStep {
	return tqSQLStep{kind: "exec", affected: 1, contains: []string{"DELETE FROM team_questions_tokens"}}
}

func tqOrdinaryStamp(column string) tqSQLStep {
	return tqSQLStep{kind: "exec", affected: 1, contains: []string{"UPDATE general_applications SET " + column}}
}

// TestIssueAndSendOrdinaryReleasesLockBeforeSendAndRetriesOnFailure locks in
// the fix for holding the application row lock across the SES round-trip: the
// token must be committed (and the lock released) before the network call,
// and a failed send must remove the now-unusable token so the next run
// treats the application as never-invited again instead of skipping it
// forever.
func TestIssueAndSendOrdinaryReleasesLockBeforeSendAndRetriesOnFailure(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
	h.now = func() time.Time { return teamQuestionsFinalCallStart.Add(-time.Hour) }

	id := uuid.New()
	application := models.GeneralApplication{Id: id, Status: models.GeneralApplicationStatusPending}
	row := tqFinalRow(id, generalApplicationYear, models.GeneralApplicationStatusPending, nil)

	sendFailure := errors.New("fake delivery failure")
	calls := 0
	var insertedHash string
	h.sendInvite = func(current models.GeneralApplication, _, _, _ string) error {
		calls++
		require.Equal(t, id, current.Id)
		if calls == 1 {
			return sendFailure
		}
		return nil
	}
	h.sendReminder = func(models.GeneralApplication, string, string, string) error {
		t.Fatal("unexpected reminder send")
		return nil
	}

	// First attempt: the token is committed, the send fails, and the unused
	// token is deleted afterward — no lock is held during either step.
	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row),
		tqOrdinaryTokenInsert(t, id, &insertedHash), tqSQLStep{kind: "commit"},
		tqOrdinaryTokenDelete())
	err := h.issueAndSendOrdinary(application, "body", "subject", false, false)
	require.ErrorIs(t, err, sendFailure)
	require.Equal(t, 1, calls)

	// Second attempt: a fresh token is issued, committed, and only then does
	// the (successful) send happen, followed by the stamp update.
	firstHash := insertedHash
	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row),
		tqOrdinaryTokenInsert(t, id, &insertedHash), tqSQLStep{kind: "commit"},
		tqOrdinaryStamp("team_questions_invite_sent_at"))
	err = h.issueAndSendOrdinary(application, "body", "subject", false, false)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.NotEqual(t, firstHash, insertedHash, "a retry must issue a fresh token")
}
