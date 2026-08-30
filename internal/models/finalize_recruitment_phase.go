package models

import (
	"time"

	"gorm.io/gorm"
)

// FinalizeRecruitmentPhase is a singleton row tracking the recruitment "day
// of reckoning" phase. While open, any admin may accept or reject an
// interviewed applicant (accepting records the admin's own team). Opening
// requires an IT admin; closing requires specifically the head of IT, and is
// the point at which the list of new members is treated as solid. Any admin
// may read this row; only the handler enforces who may open/close it.
type FinalizeRecruitmentPhase struct {
	gorm.Model
	OpenedByEmail string     `gorm:"type:text;not null;default:''" json:"opened_by_email"`
	OpenedAt      *time.Time `json:"opened_at"`
	ClosedByEmail string     `gorm:"type:text;not null;default:''" json:"closed_by_email"`
	ClosedAt      *time.Time `json:"closed_at"`
}

// IsOpen reports whether the phase is currently open: opened, and not yet
// closed since. Re-opening after a close (see AdminOpenFinalizePhase)
// overwrites ClosedAt, so this stays a simple two-field check rather than a
// separate status enum.
func (p FinalizeRecruitmentPhase) IsOpen() bool {
	return p.OpenedAt != nil && p.ClosedAt == nil
}
