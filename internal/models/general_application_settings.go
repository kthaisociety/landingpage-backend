package models

import (
	"time"

	"gorm.io/gorm"
)

// GeneralApplicationSettings is a singleton row holding admin-configurable
// settings for the public application flow — the submission deadline, and
// the copy shown on the closed page once it passes. Any admin may read and
// write it (enforced in the handler, not here).
type GeneralApplicationSettings struct {
	gorm.Model
	// SubmissionDeadline is the instant applications close for good. The
	// zero value means "not yet configured" — the handler falls back to a
	// hardcoded default in that case (see defaultSubmissionDeadline).
	SubmissionDeadline time.Time `json:"submission_deadline"`
	// ClosedHeading and ClosedMessage are the /apply closed screen's copy.
	// An empty value means "not yet configured" — same default-fallback
	// convention as SubmissionDeadline (see defaultClosedHeading/Message).
	ClosedHeading  string `gorm:"type:text;not null;default:''" json:"closed_heading"`
	ClosedMessage  string `gorm:"type:text;not null;default:''" json:"closed_message"`
	UpdatedByEmail string `gorm:"type:text;not null;default:''" json:"updated_by_email"`
}
