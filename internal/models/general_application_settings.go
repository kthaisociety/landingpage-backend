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
	// RecruitmentOpensAt is the instant the public site starts showing Apply
	// CTAs (nav/hero — see IsRecruitmentOpen). nil means "no lower bound" —
	// recruitment is considered open as soon as before SubmissionDeadline,
	// matching how the site behaved before this field existed — same
	// nullable-override convention as TeamQuestionsSettings.FinalCallStart.
	// Set to a future date to hide the CTAs until then.
	RecruitmentOpensAt *time.Time `json:"recruitment_opens_at"`
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

// IsRecruitmentOpen reports whether the public site should show Apply CTAs
// right now: on or after RecruitmentOpensAt (if set), and on or before
// SubmissionDeadline. Exported so both the handler and its tests compute
// this exactly one way.
func (s GeneralApplicationSettings) IsRecruitmentOpen(now time.Time) bool {
	if s.RecruitmentOpensAt != nil && now.Before(*s.RecruitmentOpensAt) {
		return false
	}
	return !now.After(s.SubmissionDeadline)
}
