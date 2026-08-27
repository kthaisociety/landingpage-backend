package database

import (
	"log"

	"backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BackfillSharedNoteEntries copies each application's old single shared-note
// blob (models.ApplicationSharedNote) into the new per-entry table
// (models.ApplicationSharedNoteEntry) as one legacy entry, attributed to
// whoever last edited it. Safe to run on every boot: an application is
// skipped once it has any entry, whether from this backfill or a real save,
// so it never duplicates and never overwrites entries admins have since
// added.
func BackfillSharedNoteEntries(db *gorm.DB) {
	var legacyNotes []models.ApplicationSharedNote
	if err := db.Where("note <> ?", "").Find(&legacyNotes).Error; err != nil {
		log.Printf("[shared note entries backfill] failed to query legacy notes: %v", err)
		return
	}

	for _, legacy := range legacyNotes {
		var count int64
		if err := db.Model(&models.ApplicationSharedNoteEntry{}).
			Where("application_id = ?", legacy.ApplicationID).
			Count(&count).Error; err != nil {
			log.Printf("[shared note entries backfill] failed to check existing entries for application %s: %v", legacy.ApplicationID, err)
			continue
		}
		if count > 0 {
			continue
		}

		entry := models.ApplicationSharedNoteEntry{
			Id:            uuid.New(),
			ApplicationID: legacy.ApplicationID,
			AuthorID:      legacy.LastEditedBy,
			AuthorEmail:   legacy.LastEditedEmail,
			Text:          legacy.Note,
		}
		entry.CreatedAt = legacy.UpdatedAt
		if err := db.Create(&entry).Error; err != nil {
			log.Printf("[shared note entries backfill] failed to create entry for application %s: %v", legacy.ApplicationID, err)
		}
	}
}
