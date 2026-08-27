package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ApplicationSharedNote is the legacy single-blob shared note, superseded by
// ApplicationSharedNoteEntry. Kept only so migrateLegacySharedNotes (in
// cmd/api/main.go) can carry old notes forward into the new per-entry table;
// nothing else reads or writes this anymore.
type ApplicationSharedNote struct {
	gorm.Model
	ApplicationID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex" json:"application_id"`
	Note            string    `gorm:"type:text;not null;default:''" json:"note"`
	LastEditedBy    uuid.UUID `gorm:"type:uuid;not null" json:"last_edited_by"`
	LastEditedEmail string    `gorm:"type:text;not null;default:''" json:"last_edited_email"`
}
