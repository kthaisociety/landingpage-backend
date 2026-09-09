package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGeneralApplicationSettingsIsRecruitmentOpen(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	t.Run("unconfigured opens-at has no lower bound", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			SubmissionDeadline: now.Add(24 * time.Hour),
		}
		require.True(t, settings.IsRecruitmentOpen(now), "a never-set RecruitmentOpensAt must not gate recruitment closed")
	})

	t.Run("before opens-at is closed", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			RecruitmentOpensAt: ptr(now.Add(time.Hour)),
			SubmissionDeadline: now.Add(24 * time.Hour),
		}
		require.False(t, settings.IsRecruitmentOpen(now))
	})

	t.Run("exactly at opens-at is open", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			RecruitmentOpensAt: ptr(now),
			SubmissionDeadline: now.Add(24 * time.Hour),
		}
		require.True(t, settings.IsRecruitmentOpen(now))
	})

	t.Run("between opens-at and deadline is open", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			RecruitmentOpensAt: ptr(now.Add(-time.Hour)),
			SubmissionDeadline: now.Add(time.Hour),
		}
		require.True(t, settings.IsRecruitmentOpen(now))
	})

	t.Run("after the deadline is closed, even if opens-at already passed", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			RecruitmentOpensAt: ptr(now.Add(-48 * time.Hour)),
			SubmissionDeadline: now.Add(-time.Hour),
		}
		require.False(t, settings.IsRecruitmentOpen(now))
	})

	t.Run("exactly at the deadline is still open", func(t *testing.T) {
		settings := GeneralApplicationSettings{
			RecruitmentOpensAt: ptr(now.Add(-time.Hour)),
			SubmissionDeadline: now,
		}
		require.True(t, settings.IsRecruitmentOpen(now))
	})
}
