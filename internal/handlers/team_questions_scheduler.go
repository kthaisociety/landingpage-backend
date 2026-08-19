package handlers

import (
	"log"
	"time"
)

// teamQuestionsInviteTZ anchors the daily send to Swedish local time, so it
// stays at 10:00 for applicants regardless of server timezone and correctly
// shifts across Sweden's DST transitions.
var teamQuestionsInviteTZ = mustLoadLocation("Europe/Stockholm")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("failed to load timezone %s, falling back to UTC: %v", name, err)
		return time.UTC
	}
	return loc
}

// StartDailyInviteScheduler runs SendPendingInvites once a day at 10:00
// Europe/Stockholm, for as long as the process is running. It recomputes the
// next 10:00 on every iteration rather than sleeping a fixed 24h, so DST
// transitions don't drift the send time. Safe to start on every boot and
// safe if a run ever overlaps a manual admin send: pendingUninvitedApplications
// only ever picks up applications that have never been issued a token, so a
// duplicate or re-triggered run just sends nothing extra.
func (h *TeamQuestionsHandler) StartDailyInviteScheduler() {
	go func() {
		for {
			time.Sleep(time.Until(nextTeamQuestionsRun(time.Now().In(teamQuestionsInviteTZ))))

			sent, failed, err := h.SendPendingInvites()
			if err != nil {
				log.Printf("daily team questions invite send failed: %v", err)
				continue
			}
			if sent == 0 && len(failed) == 0 {
				log.Printf("daily team questions invite send: no pending applicants to invite")
				continue
			}
			log.Printf("daily team questions invite send: sent %d, failed %d", sent, len(failed))
		}
	}()
}

func nextTeamQuestionsRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
