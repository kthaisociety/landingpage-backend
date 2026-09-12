package database

import (
	"log"

	"backend/internal/models"

	"gorm.io/gorm"
)

// BackfillRejectionEmailSentAt stamps RejectionEmailSentAt on every
// application that was already rejected before that column existed — without
// this, AdminSendRejectionsBulk's "not yet emailed" query would treat every
// historical rejection as unsent and re-email everyone who was already told
// months ago. Falls back to CreatedAt when FinalizedAt is also unset (should
// only happen for data seeded/imported before FinalizedAt existed). Runs
// unconditionally on every API boot, right after AutoMigrate, the same way
// BackfillProfileSlugs does: safe to run repeatedly, since once every
// rejected application has a value this is a single query that finds
// nothing.
func BackfillRejectionEmailSentAt(db *gorm.DB) {
	var applications []models.GeneralApplication
	if err := db.Where("status = ? AND rejection_email_sent_at IS NULL", models.GeneralApplicationStatusRejected).
		Find(&applications).Error; err != nil {
		log.Printf("[rejection email backfill] failed to query applications: %v", err)
		return
	}
	if len(applications) == 0 {
		return
	}

	backfilled := 0
	for _, application := range applications {
		sentAt := application.FinalizedAt
		if sentAt == nil {
			sentAt = &application.CreatedAt
		}
		if err := db.Model(&models.GeneralApplication{}).Where("id = ?", application.Id).
			Update("rejection_email_sent_at", sentAt).Error; err != nil {
			log.Printf("[rejection email backfill] failed to set rejection_email_sent_at for application %s: %v", application.Id, err)
			continue
		}
		backfilled++
	}

	if backfilled > 0 {
		log.Printf("[rejection email backfill] backfilled rejection_email_sent_at for %d application(s)", backfilled)
	}
}
