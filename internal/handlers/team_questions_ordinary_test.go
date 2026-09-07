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

// tqOrdinaryClaim is the in-transaction stamp that claims the send right for
// one applicant — set right after the token is created, still under the row
// lock, so a concurrent caller for the same applicant either blocks behind
// this transaction or (if it started first) has already set this column
// before we ever get here.
func tqOrdinaryClaim(column string) tqSQLStep {
	return tqSQLStep{kind: "exec", affected: 1, contains: []string{"UPDATE general_applications SET " + column}}
}

// tqOrdinaryClaimRevert undoes tqOrdinaryClaim after a real failure (a stale
// token or a failed send), restoring whatever the column held before this
// call (nil for a first-time send) so the applicant remains eligible for a
// future retry instead of being stuck permanently marked as sent.
func tqOrdinaryClaimRevert(t *testing.T, column string) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "exec", affected: 1, contains: []string{"UPDATE general_applications SET " + column},
		check: func(query string, _ []driver.NamedValue) {
			require.Contains(t, query, "AND "+column, "the revert must be matched to our own claim, not unconditional")
		},
	}
}

// tqOrdinaryAlreadyClaimedRow mirrors tqFinalRow but with the relevant
// "already sent" column populated, for testing the in-transaction guard
// against a concurrent duplicate claim.
func tqOrdinaryAlreadyClaimedRow(t *testing.T, id uuid.UUID, sentAtColumn string, sentAt time.Time) tqSQLStep {
	t.Helper()
	return tqSQLStep{
		kind: "query", contains: []string{`FROM "general_applications"`, "FOR UPDATE"},
		columns: []string{"id", "application_year", "status", sentAtColumn},
		rows:    [][]driver.Value{{id.String(), int64(generalApplicationYear), string(models.GeneralApplicationStatusPending), sentAt}},
		check: func(_ string, args []driver.NamedValue) {
			require.NotEmpty(t, args)
			require.Equal(t, id.String(), args[0].Value)
		},
	}
}

// TestIssueAndSendOrdinaryReleasesLockBeforeSendAndRetriesOnFailure locks in
// the fix for holding the application row lock across the SES round-trip: the
// token must be committed (and the lock released) before the network call.
// A failed send must remove the now-unusable token and revert the claim so
// the next run treats the application as never-invited again instead of
// skipping it forever.
func TestIssueAndSendOrdinaryReleasesLockBeforeSendAndRetriesOnFailure(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
	h.now = func() time.Time { return defaultTeamQuestionsFinalCallStart.Add(-time.Hour) }

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

	// First attempt: the token is committed, the claim is set, the send
	// fails, and both the unused token and the claim are reverted — no lock
	// is held during either post-commit step.
	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row),
		tqOrdinaryTokenInsert(t, id, &insertedHash), tqOrdinaryClaim("team_questions_invite_sent_at"), tqSQLStep{kind: "commit"})
	script.add(tqNotStaleSteps(t, id)...)
	script.add(tqOrdinaryTokenDelete())
	script.add(tqOrdinaryClaimRevert(t, "team_questions_invite_sent_at"))
	script.add(tqDeliveryEvent(t, id, models.TeamQuestionsDeliveryOutcomeFailed))
	err := h.issueAndSendOrdinary(application, "body", "subject", false, false)
	require.ErrorIs(t, err, sendFailure)
	require.Equal(t, 1, calls)

	// Second attempt: a fresh token is issued and committed along with a
	// fresh claim, and only then does the (successful) send happen — with
	// nothing left to write afterward, since the claim already covers it.
	firstHash := insertedHash
	script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row),
		tqOrdinaryTokenInsert(t, id, &insertedHash), tqOrdinaryClaim("team_questions_invite_sent_at"), tqSQLStep{kind: "commit"})
	script.add(tqNotStaleSteps(t, id)...)
	script.add(tqDeliveryEvent(t, id, models.TeamQuestionsDeliveryOutcomeSent))
	err = h.issueAndSendOrdinary(application, "body", "subject", false, false)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.NotEqual(t, firstHash, insertedHash, "a retry must issue a fresh token")
}

// TestIssueAndSendOrdinarySkipsWhenAlreadyClaimed covers the new guard this
// session added after a production incident: an automatic scheduler run and
// a second overlapping process (a rolling deploy briefly running two
// instances, or a concurrent admin bulk-send) both saw the same applicant as
// pending and both tried to send. The second caller must see the claim
// already set — inside the same row-locked transaction, so it never races
// past it — and stop before ever creating a token or touching SES.
func TestIssueAndSendOrdinarySkipsWhenAlreadyClaimed(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reminder     bool
		sentAtColumn string
	}{
		{name: "invite already claimed", reminder: false, sentAtColumn: "team_questions_invite_sent_at"},
		{name: "reminder already claimed", reminder: true, sentAtColumn: "team_questions_reminder_sent_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, script := newTeamQuestionsSQL(t)
			h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
			h.now = func() time.Time { return defaultTeamQuestionsFinalCallStart.Add(-time.Hour) }
			h.sendInvite = func(models.GeneralApplication, string, string, string) error {
				t.Fatal("unexpected send: an already-claimed applicant must never be dispatched")
				return nil
			}
			h.sendReminder = func(models.GeneralApplication, string, string, string) error {
				t.Fatal("unexpected send: an already-claimed applicant must never be dispatched")
				return nil
			}

			id := uuid.New()
			application := models.GeneralApplication{Id: id, Status: models.GeneralApplicationStatusPending}
			claimedByOther := defaultTeamQuestionsFinalCallStart.Add(-2 * time.Hour)

			script.add(tqSQLStep{kind: "begin"},
				tqOrdinaryAlreadyClaimedRow(t, id, tc.sentAtColumn, claimedByOther),
				tqSQLStep{kind: "rollback"})
			script.add(tqDeliveryEvent(t, id, models.TeamQuestionsDeliveryOutcomeSuperseded))

			err := h.issueAndSendOrdinary(application, "body", "subject", tc.reminder, true)
			require.ErrorIs(t, err, errTeamQuestionsTokenSuperseded)
		})
	}
}

// TestIssueAndSendOrdinaryManualResendIgnoresExistingClaim is the regression
// test for a mistake caught while building the guard above: AdminResend
// (automatic=false) is *supposed* to run against an applicant who already
// has an invite_sent_at — that's the entire point of a resend, reissuing a
// fresh link after the fact. Only the automatic path treats an existing
// stamp as disqualifying. A failed resend must also restore the applicant's
// real prior send time, not wipe it to null — a manual resend's claim
// overwrites that timestamp the same way a successful one would, so on
// failure it has to be handed back exactly, not reset to "never sent".
func TestIssueAndSendOrdinaryManualResendIgnoresExistingClaim(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
	h.now = func() time.Time { return defaultTeamQuestionsFinalCallStart.Add(-time.Hour) }

	id := uuid.New()
	application := models.GeneralApplication{Id: id, Status: models.GeneralApplicationStatusPending}
	priorSend := defaultTeamQuestionsFinalCallStart.Add(-48 * time.Hour)
	row := tqOrdinaryAlreadyClaimedRow(t, id, "team_questions_invite_sent_at", priorSend)

	sendFailure := errors.New("fake delivery failure")
	h.sendInvite = func(current models.GeneralApplication, _, _, _ string) error {
		require.Equal(t, id, current.Id)
		return sendFailure
	}

	script.add(tqSQLStep{kind: "begin"}, row,
		tqOrdinaryTokenInsert(t, id, nil), tqOrdinaryClaim("team_questions_invite_sent_at"), tqSQLStep{kind: "commit"})
	script.add(tqNotStaleSteps(t, id)...)
	script.add(tqOrdinaryTokenDelete())
	revert := tqOrdinaryClaimRevert(t, "team_questions_invite_sent_at")
	revert.check = func(query string, args []driver.NamedValue) {
		require.Contains(t, query, "AND team_questions_invite_sent_at")
		require.Len(t, args, 3)
		restored, ok := args[0].Value.(time.Time)
		require.True(t, ok)
		require.True(t, restored.Equal(priorSend), "a failed resend must restore the real prior send time, not null it out")
	}
	script.add(revert)
	script.add(tqDeliveryEvent(t, id, models.TeamQuestionsDeliveryOutcomeFailed))

	err := h.issueAndSendOrdinary(application, "body", "subject", false, false)
	require.ErrorIs(t, err, sendFailure)
	require.NotErrorIs(t, err, errTeamQuestionsTokenSuperseded, "a manual resend must not be blocked by its own prior send")
}

// TestIssueAndSendOrdinarySkipsWhenSuperseded covers the pre-dispatch
// staleness check: once the token's transaction commits and releases the
// row lock, a concurrent resend/reminder/final-call (or the application
// leaving pending some other way) can make that token's link dead on
// arrival. The stale token — and its claim — must be discarded and the send
// skipped, never dispatched.
func TestIssueAndSendOrdinarySkipsWhenSuperseded(t *testing.T) {
	for _, tc := range []struct {
		name       string
		newerCount int64
		status     models.GeneralApplicationStatus
	}{
		{name: "a newer token now exists", newerCount: 1, status: models.GeneralApplicationStatusPending},
		{name: "the application left pending", newerCount: 0, status: models.GeneralApplicationStatusAvailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, script := newTeamQuestionsSQL(t)
			h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})
			h.now = func() time.Time { return defaultTeamQuestionsFinalCallStart.Add(-time.Hour) }
			h.sendInvite = func(models.GeneralApplication, string, string, string) error {
				t.Fatal("unexpected send: a superseded/stale token must never be dispatched")
				return nil
			}

			id := uuid.New()
			application := models.GeneralApplication{Id: id, Status: models.GeneralApplicationStatusPending}
			row := tqFinalRow(id, generalApplicationYear, models.GeneralApplicationStatusPending, nil)

			script.add(tqSQLStep{kind: "begin"}, tqFinalLockedApplication(t, id, row),
				tqOrdinaryTokenInsert(t, id, nil), tqOrdinaryClaim("team_questions_invite_sent_at"), tqSQLStep{kind: "commit"})
			script.add(tqSQLStep{kind: "query", contains: []string{`FROM "team_questions_tokens"`, "count(*)"}, columns: []string{"count"}, rows: [][]driver.Value{{tc.newerCount}}})
			if tc.newerCount == 0 {
				script.add(tqSQLStep{kind: "query", contains: []string{`SELECT "status" FROM "general_applications"`}, columns: []string{"status"}, rows: [][]driver.Value{{string(tc.status)}}})
			}
			script.add(tqOrdinaryTokenDelete())
			script.add(tqOrdinaryClaimRevert(t, "team_questions_invite_sent_at"))
			script.add(tqDeliveryEvent(t, id, models.TeamQuestionsDeliveryOutcomeSuperseded))

			err := h.issueAndSendOrdinary(application, "body", "subject", false, false)
			require.ErrorIs(t, err, errTeamQuestionsTokenSuperseded)
		})
	}
}
