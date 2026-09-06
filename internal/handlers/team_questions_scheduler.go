package handlers

import (
	"log"
	"time"
	_ "time/tzdata" // Keep Stockholm deadlines correct even without system zoneinfo.
)

// teamQuestionsInviteTZ anchors the daily send to Swedish local time, so the
// windows below stay put for applicants regardless of server timezone and
// correctly shift across Sweden's DST transitions.
var teamQuestionsInviteTZ = mustLoadLocation("Europe/Stockholm")

// defaultTeamQuestionsFinalCallStart and defaultTeamQuestionsSubmissionCutoff
// are the fallback instants used whenever TeamQuestionsSettings hasn't
// configured an override (see TeamQuestionsHandler.effectiveFinalCallStart /
// effectiveSubmissionCutoff) — matching the defaultTeamQuestions*Template
// naming convention used for the other admin-overridable defaults.
var defaultTeamQuestionsFinalCallStart = time.Date(2026, time.September, 7, 10, 0, 0, 0, teamQuestionsInviteTZ)
var defaultTeamQuestionsSubmissionCutoff = time.Date(2026, time.September, 9, 0, 0, 0, 0, teamQuestionsInviteTZ)

func teamQuestionsClosed(now, cutoff time.Time) bool {
	return !now.Before(cutoff)
}

func teamQuestionsFinalCallWindow(now, start, cutoff time.Time) bool {
	return !now.Before(start) && !teamQuestionsClosed(now, cutoff)
}

// teamQuestionsRunHours are the local hours the scheduler fires at, every
// day. Two, both within normal working hours, rather than one: an in-process
// scheduler has no memory of what it missed across a restart, so if a deploy
// happens to kill the old process just before a run and the new one doesn't
// finish booting until just after, that day's run slips to the next window
// instead of being lost for a full 24h. Ordinary emails wait for these hours
// rather than sending immediately on boot. During the short final-call
// window, startup also catches up, including a restart after the last run
// on September 8. Existing tokens and sent markers prevent later scheduled
// runs from selecting applications that have already received that email.
var teamQuestionsRunHours = []int{10, 16}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("failed to load required timezone " + name + ": " + err.Error())
	}
	return loc
}

// StartDailyTeamQuestionsScheduler sends ordinary invites and 7-day reminders
// before the final-call window, then only final calls until submissions close.
// Runs stay at teamQuestionsRunHours in Europe/Stockholm. Recomputing the next
// local run time on every iteration keeps DST transitions from moving it.
func (h *TeamQuestionsHandler) StartDailyTeamQuestionsScheduler() {
	// Warm the deadline-override cache synchronously before the loop below
	// ever reads it — a boot-time settings-load failure just means this
	// process runs on the hardcoded defaults until its next successful
	// getSettings() call, never a crash.
	if _, err := h.getSettings(); err != nil {
		log.Printf("failed to load team questions settings at startup — using default deadlines: %v", err)
	}
	go func() {
		if teamQuestionsFinalCallWindow(h.now(), h.effectiveFinalCallStart(), h.effectiveSubmissionCutoff()) {
			h.runTeamQuestionsScheduledSend()
		}
		for {
			next := nextTeamQuestionsRun(h.now().In(teamQuestionsInviteTZ))
			if teamQuestionsClosed(next, h.effectiveSubmissionCutoff()) {
				return
			}
			time.Sleep(time.Until(next))
			h.runTeamQuestionsScheduledSend()
		}
	}()
}

func (h *TeamQuestionsHandler) runTeamQuestionsScheduledSend() {
	now := h.now()
	if teamQuestionsClosed(now, h.effectiveSubmissionCutoff()) {
		return
	}
	if teamQuestionsFinalCallWindow(now, h.effectiveFinalCallStart(), h.effectiveSubmissionCutoff()) {
		sent, failed, err := h.SendPendingFinalCalls()
		if err != nil {
			log.Printf("team questions final call send failed: %v", err)
		} else if sent > 0 || len(failed) > 0 {
			log.Printf("team questions final call send: sent %d, failed %d", sent, len(failed))
		}
		return
	}

	sent, failed, err := h.SendPendingInvites()
	if err != nil {
		log.Printf("team questions invite send failed: %v", err)
	} else if sent > 0 || len(failed) > 0 {
		log.Printf("team questions invite send: sent %d, failed %d", sent, len(failed))
	}

	remindersSent, remindersFailed, err := h.SendPendingReminders()
	if err != nil {
		log.Printf("team questions reminder send failed: %v", err)
	} else if remindersSent > 0 || len(remindersFailed) > 0 {
		log.Printf("team questions reminder send: sent %d, failed %d", remindersSent, len(remindersFailed))
	}
}

// nextTeamQuestionsRun returns the nearest upcoming time (today or tomorrow)
// among teamQuestionsRunHours, strictly after now.
func nextTeamQuestionsRun(now time.Time) time.Time {
	var best time.Time
	for _, hour := range teamQuestionsRunHours {
		candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		if best.IsZero() || candidate.Before(best) {
			best = candidate
		}
	}
	return best
}
