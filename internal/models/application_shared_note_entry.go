package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ApplicationSharedNoteEntry is one admin's comment on an applicant, visible
// to all admins. Entries are appended chronologically rather than merged
// into a single blob, so each one keeps its own author — admins may edit
// only their own entries (enforced in the handler, not here).
type ApplicationSharedNoteEntry struct {
	gorm.Model
	// Id, CreatedAt, and UpdatedAt shadow the untagged fields gorm.Model
	// promotes — those serialize as "ID"/"CreatedAt"/"UpdatedAt" with no way
	// to retag a promoted field, which would silently break the frontend's
	// snake_case JSON contract (see GeneralApplication.Id for the same fix).
	Id            uuid.UUID `gorm:"uniqueIndex" json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ApplicationID uuid.UUID `gorm:"type:uuid;not null;index" json:"application_id"`
	AuthorID      uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	AuthorEmail   string    `gorm:"type:text;not null;default:''" json:"author_email"`
	Text          string    `gorm:"type:text;not null;default:''" json:"text"`
}
