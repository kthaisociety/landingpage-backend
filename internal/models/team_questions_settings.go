package models

import "gorm.io/gorm"

// TeamQuestionsSettings is a singleton row holding the shared Team Questions
// invite email template. Any admin can read it; only IT admins may write it
// (enforced in the handler, not here) since it isn't scoped to one admin the
// way Profile.InterviewEmailTemplate is.
type TeamQuestionsSettings struct {
	gorm.Model
	EmailTemplate  string `gorm:"type:text;not null;default:''" json:"email_template"`
	UpdatedByEmail string `gorm:"type:text;not null;default:''" json:"updated_by_email"`
}
