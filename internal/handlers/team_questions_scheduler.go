package handlers

import (
	"log"
	"time"
)

// teamQuestionsInviteTZ anchors the daily send to Swedish local time, so the
// windows below stay put for applicants regardless of server timezone and
// correctly shift across Sweden's DST transitions.
var teamQuestionsInviteTZ = mustLoadLocation("Europe/Stockholm")

// teamQuestionsRunHours are the local hours the scheduler fires at, every
// day. Two, both within normal working hours, rather than one: an in-process
// scheduler has no memory of what it missed across a restart, so if a deploy
// happens to kill the old process just before a run and the new one doesn't
// finish booting until just after, that day's run slips to the next window
// instead of being lost for a full 24h. Deliberately not "run once
// immediately on boot" instead — that would fire at whatever odd hour a
// deploy happens to land on (2am included), rather than a time someone
// picked. Both SendPendingInvites and SendPendingReminders only ever act on
// applications that have never been sent that particular email, so a
// same-day double-run near one of these windows is always a no-op, never a
// duplicate send.
var teamQuestionsRunHours = []int{10, 16}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("failed to load timezone %s, falling back to UTC: %v", name, err)
		return time.UTC
	}
	return loc
}

// StartDailyTeamQuestionsScheduler sends pending Team Questions invites and
// 7-day reminders at each hour in teamQuestionsRunHours, Europe/Stockholm,
// for as long as the process is running. It recomputes the next run time on
// every iteration rather than sleeping a fixed interval, so DST transitions
// don't drift the send time.
func (h *TeamQuestionsHandler) StartDailyTeamQuestionsScheduler() {
	go func() {
		for {
			time.Sleep(time.Until(nextTeamQuestionsRun(time.Now().In(teamQuestionsInviteTZ))))
			h.runTeamQuestionsScheduledSend()
		}
	}()
}

func (h *TeamQuestionsHandler) runTeamQuestionsScheduledSend() {
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
