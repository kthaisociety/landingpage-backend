package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextTeamQuestionsRun(t *testing.T) {
	loc := teamQuestionsInviteTZ

	t.Run("before 10:00 runs at 10:00 later today", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 9, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 10, 10, 0, 0, 0, loc), next)
	})

	t.Run("between the two windows runs at 16:00 later today", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 12, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 10, 16, 0, 0, 0, loc), next)
	})

	t.Run("after 16:00 rolls to 10:00 tomorrow", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 16, 0, 1, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 11, 10, 0, 0, 0, loc), next)
	})

	t.Run("exactly 10:00 rolls to 16:00 later today, not an immediate re-fire", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 10, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 10, 16, 0, 0, 0, loc), next)
	})

	t.Run("exactly 16:00 rolls to 10:00 tomorrow, not an immediate re-fire", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 16, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 11, 10, 0, 0, 0, loc), next)
	})

	// Sweden springs forward the last Sunday of March (2026-03-29, 02:00 -> 03:00)
	// and falls back the last Sunday of October (2026-10-25, 03:00 -> 02:00).
	// Both run hours sit well outside either transition window, so the run
	// should land at a plain, unambiguous local time on both sides of each change.
	t.Run("spring-forward day still lands on 10:00 local", func(t *testing.T) {
		now := time.Date(2026, 3, 29, 1, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, 10, next.Hour())
		require.Equal(t, 29, next.Day())
	})

	t.Run("fall-back day still lands on 10:00 local", func(t *testing.T) {
		now := time.Date(2026, 10, 25, 1, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, 10, next.Hour())
		require.Equal(t, 25, next.Day())
	})

	t.Run("day after spring-forward, the offset shifted but the run is still 10:00 local", func(t *testing.T) {
		before := time.Date(2026, 3, 28, 1, 0, 0, 0, loc)
		after := time.Date(2026, 3, 30, 1, 0, 0, 0, loc)
		_, beforeOffset := before.Zone()
		_, afterOffset := after.Zone()
		require.NotEqual(t, beforeOffset, afterOffset, "test fixture assumes the DST change actually happened between these two dates")

		next := nextTeamQuestionsRun(after)
		require.Equal(t, 10, next.Hour())
	})
}

func TestTeamQuestionsWindowBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		stockholm time.Time
		utc       time.Time
		finalCall bool
		closed    bool
	}{
		{
			name:      "September 7 at 09:59:59 keeps ordinary sends",
			stockholm: time.Date(2026, time.September, 7, 9, 59, 59, 0, teamQuestionsInviteTZ),
			utc:       time.Date(2026, time.September, 7, 7, 59, 59, 0, time.UTC),
		},
		{
			name:      "September 7 at 10:00 starts final calls",
			stockholm: time.Date(2026, time.September, 7, 10, 0, 0, 0, teamQuestionsInviteTZ),
			utc:       time.Date(2026, time.September, 7, 8, 0, 0, 0, time.UTC),
			finalCall: true,
		},
		{
			name:      "September 8 at 23:59:59 stays open",
			stockholm: time.Date(2026, time.September, 8, 23, 59, 59, 0, teamQuestionsInviteTZ),
			utc:       time.Date(2026, time.September, 8, 21, 59, 59, 0, time.UTC),
			finalCall: true,
		},
		{
			name:      "September 9 at 00:00 closes submissions and emails",
			stockholm: time.Date(2026, time.September, 9, 0, 0, 0, 0, teamQuestionsInviteTZ),
			utc:       time.Date(2026, time.September, 8, 22, 0, 0, 0, time.UTC),
			closed:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.stockholm.Equal(tc.utc), "Stockholm must be UTC+2 at these boundaries")
			for _, now := range []time.Time{tc.stockholm, tc.utc} {
				require.Equal(t, tc.finalCall, teamQuestionsFinalCallWindow(now, defaultTeamQuestionsFinalCallStart, defaultTeamQuestionsSubmissionCutoff), "at %s", now)
				require.Equal(t, tc.closed, teamQuestionsClosed(now, defaultTeamQuestionsSubmissionCutoff), "at %s", now)
			}
		})
	}
}

func TestTeamQuestionsSchedulerDoesNothingAfterClosure(t *testing.T) {
	for _, now := range []time.Time{
		defaultTeamQuestionsSubmissionCutoff,
		defaultTeamQuestionsSubmissionCutoff.Add(24 * time.Hour),
	} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			h := newTeamQuestionsDeadlineTestHandler(t, now)
			// A database access would panic because this handler has no DB.
			require.NotPanics(t, h.runTeamQuestionsScheduledSend)
		})
	}
}

func TestTeamQuestionsSchedulerSelectsEmailPhase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		now       time.Time
		finalCall bool
	}{
		{name: "ordinary before September 7 at 10", now: time.Date(2026, time.September, 7, 9, 59, 59, 0, teamQuestionsInviteTZ)},
		{name: "final calls at September 7 at 10", now: time.Date(2026, time.September, 7, 10, 0, 0, 0, teamQuestionsInviteTZ), finalCall: true},
		{name: "final calls through September 8", now: time.Date(2026, time.September, 8, 23, 59, 59, 0, teamQuestionsInviteTZ), finalCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, script := newTQFinalHandler(t)
			h.now = func() time.Time { return tc.now }
			if tc.finalCall {
				script.add(tqFinalCandidates(t))
			} else {
				// Empty recipient lists keep the old send path entirely offline.
				// Unexpected final-call selection or extra queries fail the script.
				script.add(
					tqSQLStep{kind: "query", contains: []string{"id NOT IN (SELECT application_id::text FROM team_questions_tokens)"}, columns: []string{"id"}},
					tqSQLStep{kind: "query", contains: []string{"team_questions_invite_sent_at <=", "team_questions_reminder_sent_at IS NULL"}, columns: []string{"id"}},
				)
			}
			h.runTeamQuestionsScheduledSend()
		})
	}
}
