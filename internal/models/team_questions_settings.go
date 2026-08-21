package models

import "gorm.io/gorm"

// TeamQuestionsSettings is a singleton row holding the shared Team Questions
// invite email template. Any admin can read it; only IT admins may write it
// (enforced in the handler, not here) since it isn't scoped to one admin the
// way Profile.InterviewEmailTemplate is.
type TeamQuestionsSettings struct {
	gorm.Model
	EmailTemplate string `gorm:"type:text;not null;default:''" json:"email_template"`
	// EmailSubject may contain {{first_name}} and {{teams}} placeholders — see
	// RenderTeamQuestionsInvite in internal/email/email.go.
	EmailSubject string `gorm:"type:text;not null;default:''" json:"email_subject"`
	// ReminderEmailTemplate is sent once, automatically, to applicants who
	// haven't submitted Team Questions 14 days after their invite — see
	// teamQuestionsReminderDelay in team_questions_handler.go.
	ReminderEmailTemplate string `gorm:"type:text;not null;default:''" json:"reminder_email_template"`
	// ReminderEmailSubject may contain {{first_name}} and {{teams}} placeholders.
	ReminderEmailSubject string `gorm:"type:text;not null;default:''" json:"reminder_email_subject"`
	UpdatedByEmail       string `gorm:"type:text;not null;default:''" json:"updated_by_email"`
}
