package models

import (
	"time"

	"gorm.io/gorm"
)

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
	// haven't submitted Team Questions 7 days after their invite — see
	// teamQuestionsReminderDelay in team_questions_handler.go.
	ReminderEmailTemplate string `gorm:"type:text;not null;default:''" json:"reminder_email_template"`
	// ReminderEmailSubject may contain {{first_name}} and {{teams}} placeholders.
	ReminderEmailSubject string `gorm:"type:text;not null;default:''" json:"reminder_email_subject"`
	// FinalCallStart and SubmissionCutoff override the hardcoded
	// defaultTeamQuestionsFinalCallStart / defaultTeamQuestionsSubmissionCutoff
	// (see team_questions_scheduler.go) when non-nil. nil means "not
	// configured" — every reader must fall back to the default, so a database
	// with no row (or a row that never set these) behaves exactly as it did
	// before these fields existed.
	FinalCallStart   *time.Time `json:"final_call_start_override"`
	SubmissionCutoff *time.Time `json:"submission_cutoff_override"`
	UpdatedByEmail   string     `gorm:"type:text;not null;default:''" json:"updated_by_email"`
}
