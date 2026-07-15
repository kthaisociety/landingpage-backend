package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AdminInterviewNote stores a single admin's private notes on an applicant.
// The composite unique index ensures one note row per (application, admin) pair.
type AdminInterviewNote struct {
	gorm.Model
	ApplicationID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_interview_note_app_admin" json:"application_id"`
	AdminUserID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_interview_note_app_admin" json:"admin_user_id"`
	Note          string    `gorm:"type:text;not null;default:''" json:"note"`
}
