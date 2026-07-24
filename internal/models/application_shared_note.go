package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ApplicationSharedNote stores a single note on an applicant that all admins can see and edit.
// One row per application; the last admin to save overwrites the note.
type ApplicationSharedNote struct {
	gorm.Model
	ApplicationID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex" json:"application_id"`
	Note            string    `gorm:"type:text;not null;default:''" json:"note"`
	LastEditedBy    uuid.UUID `gorm:"type:uuid;not null" json:"last_edited_by"`
	LastEditedEmail string    `gorm:"type:text;not null;default:''" json:"last_edited_email"`
}
