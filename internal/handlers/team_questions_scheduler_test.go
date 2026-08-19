package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextTeamQuestionsRun(t *testing.T) {
	loc := teamQuestionsInviteTZ

	t.Run("before 10:00 runs later today", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 9, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 10, 10, 0, 0, 0, loc), next)
	})

	t.Run("after 10:00 rolls to tomorrow", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 10, 0, 1, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 11, 10, 0, 0, 0, loc), next)
	})

	t.Run("exactly 10:00 rolls to tomorrow, not an immediate re-fire", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 10, 0, 0, 0, loc)
		next := nextTeamQuestionsRun(now)
		require.Equal(t, time.Date(2026, 3, 11, 10, 0, 0, 0, loc), next)
	})

	// Sweden springs forward the last Sunday of March (2026-03-29, 02:00 -> 03:00)
	// and falls back the last Sunday of October (2026-10-25, 03:00 -> 02:00).
	// 10:00 sits well outside either transition window, so the run should land
	// at a plain, unambiguous 10:00 local time on both sides of each change.
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
		before := time.Date(2026, 3, 28, 12, 0, 0, 0, loc)
		after := time.Date(2026, 3, 30, 12, 0, 0, 0, loc)
		_, beforeOffset := before.Zone()
		_, afterOffset := after.Zone()
		require.NotEqual(t, beforeOffset, afterOffset, "test fixture assumes the DST change actually happened between these two dates")

		next := nextTeamQuestionsRun(after)
		require.Equal(t, 10, next.Hour())
	})
}
