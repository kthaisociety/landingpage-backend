package handlers

import (
	"database/sql/driver"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/stretchr/testify/require"
)

// TestEffectiveDeadlinesDefaultWhenUnconfigured locks in the "byte-for-byte
// identical when unconfigured" guarantee: a fresh handler (zero-value cache,
// same as before these override fields existed) must resolve to exactly the
// hardcoded defaults.
func TestEffectiveDeadlinesDefaultWhenUnconfigured(t *testing.T) {
	h := &TeamQuestionsHandler{}
	require.True(t, defaultTeamQuestionsFinalCallStart.Equal(h.effectiveFinalCallStart()))
	require.True(t, defaultTeamQuestionsSubmissionCutoff.Equal(h.effectiveSubmissionCutoff()))
}

// TestApplyDeadlineCacheOverridesEffectiveValues covers both directions: a
// settings row with overrides set makes them take effect, and a settings row
// without them (e.g. after clearing) reverts to the defaults.
func TestApplyDeadlineCacheOverridesEffectiveValues(t *testing.T) {
	h := &TeamQuestionsHandler{}
	start := defaultTeamQuestionsFinalCallStart.Add(-24 * time.Hour)
	cutoff := defaultTeamQuestionsSubmissionCutoff.Add(-24 * time.Hour)

	h.applyDeadlineCache(models.TeamQuestionsSettings{FinalCallStart: &start, SubmissionCutoff: &cutoff})
	require.True(t, start.Equal(h.effectiveFinalCallStart()))
	require.True(t, cutoff.Equal(h.effectiveSubmissionCutoff()))

	h.applyDeadlineCache(models.TeamQuestionsSettings{})
	require.True(t, defaultTeamQuestionsFinalCallStart.Equal(h.effectiveFinalCallStart()), "clearing the override must fall back to the default, not keep the stale cached value")
	require.True(t, defaultTeamQuestionsSubmissionCutoff.Equal(h.effectiveSubmissionCutoff()))
}

// TestEffectiveTeamQuestionsHelpersReadASettingsValueDirectly covers the pure
// helpers used by admin endpoints that already hold a freshly loaded
// settings row and want the true current DB state rather than the handler's
// (possibly one-tick-stale) cache.
func TestEffectiveTeamQuestionsHelpersReadASettingsValueDirectly(t *testing.T) {
	require.True(t, defaultTeamQuestionsFinalCallStart.Equal(effectiveTeamQuestionsFinalCallStart(models.TeamQuestionsSettings{})))
	require.True(t, defaultTeamQuestionsSubmissionCutoff.Equal(effectiveTeamQuestionsSubmissionCutoff(models.TeamQuestionsSettings{})))

	start := defaultTeamQuestionsFinalCallStart.Add(-24 * time.Hour)
	require.True(t, start.Equal(effectiveTeamQuestionsFinalCallStart(models.TeamQuestionsSettings{FinalCallStart: &start})))
}

// TestGetSettingsRefreshesDeadlineCache proves getSettings() — already
// called from most of the paths that need a current deadline — keeps the
// cache warm as a side effect, using a scripted settings row with overrides
// set.
func TestGetSettingsRefreshesDeadlineCache(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})

	start := defaultTeamQuestionsFinalCallStart.Add(-24 * time.Hour)
	cutoff := defaultTeamQuestionsSubmissionCutoff.Add(-24 * time.Hour)
	script.add(tqSQLStep{
		kind:     "query",
		contains: []string{`FROM "team_questions_settings"`},
		columns:  []string{"final_call_start", "submission_cutoff"},
		rows:     [][]driver.Value{{start, cutoff}},
	})

	_, err := h.getSettings()
	require.NoError(t, err)
	require.True(t, start.Equal(h.effectiveFinalCallStart()))
	require.True(t, cutoff.Equal(h.effectiveSubmissionCutoff()))
}

// TestGetSettingsWithNoRowKeepsDefaults confirms the "no settings row saved
// yet" path (gorm.ErrRecordNotFound, tolerated by getSettings()) leaves the
// cache at the defaults rather than caching some zero/garbage value.
func TestGetSettingsWithNoRowKeepsDefaults(t *testing.T) {
	db, script := newTeamQuestionsSQL(t)
	h := NewTeamQuestionsHandler(db, &config.Config{FrontendURL: "https://example.com"})

	script.add(tqSQLStep{
		kind:     "query",
		contains: []string{`FROM "team_questions_settings"`},
		columns:  []string{"id"},
		rows:     nil,
	})

	_, err := h.getSettings()
	require.NoError(t, err)
	require.True(t, defaultTeamQuestionsFinalCallStart.Equal(h.effectiveFinalCallStart()))
	require.True(t, defaultTeamQuestionsSubmissionCutoff.Equal(h.effectiveSubmissionCutoff()))
}
