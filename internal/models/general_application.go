package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type GeneralApplicationStatus string

const (
	GeneralApplicationStatusPending      GeneralApplicationStatus = "pending"
	GeneralApplicationStatusAvailable    GeneralApplicationStatus = "available"
	GeneralApplicationStatusInterviewing GeneralApplicationStatus = "interviewing"
	GeneralApplicationStatusIneligible   GeneralApplicationStatus = "ineligible"
	GeneralApplicationStatusWithdrawn    GeneralApplicationStatus = "withdrawn"
	// GeneralApplicationStatusAccepted and GeneralApplicationStatusRejected are
	// terminal recruitment decisions. They are deliberately absent from
	// allowedApplicationStatuses in general_application_handler.go — the only
	// way to reach them is AdminFinalizeDecision, which requires the finalize
	// recruitment phase (see FinalizeRecruitmentPhase) to be open. Never add
	// them to that map; that would let the ordinary AdminUpdateStatus endpoint
	// set them with no phase gate.
	GeneralApplicationStatusAccepted GeneralApplicationStatus = "accepted"
	GeneralApplicationStatusRejected GeneralApplicationStatus = "rejected"
)

type GeneralApplication struct {
	gorm.Model
	Id                          uuid.UUID                `gorm:"uniqueIndex" json:"id"`
	ApplicationYear             int                      `gorm:"not null;uniqueIndex:idx_general_application_year_email" json:"application_year"`
	FirstName                   string                   `gorm:"not null" json:"first_name"`
	LastName                    string                   `gorm:"not null" json:"last_name"`
	Email                       string                   `gorm:"not null" json:"email"`
	EmailNormalized             string                   `gorm:"not null;uniqueIndex:idx_general_application_year_email" json:"-"`
	Gender                      string                   `gorm:"not null;default:'Prefer not to say'" json:"gender"`
	University                  string                   `gorm:"not null;default:''" json:"university"`
	Programme                   string                   `gorm:"not null" json:"programme"`
	GraduationYear              int                      `gorm:"not null" json:"graduation_year"`
	LinkedinURL                 string                   `gorm:"not null" json:"linkedin_url"`
	AdditionalLinks             pq.StringArray           `gorm:"type:text[]" json:"additional_links"`
	ResumeBlobID                uuid.UUID                `gorm:"not null" json:"-"`
	ResumeData                  []byte                   `gorm:"type:bytea" json:"-"`
	ResumeFileName              string                   `gorm:"not null" json:"resume_file_name"`
	ResumeContentType           string                   `gorm:"not null" json:"resume_content_type"`
	Teams                       pq.StringArray           `gorm:"type:text[];not null" json:"teams"`
	TeamPreferencesRanked       bool                     `gorm:"not null;default:false" json:"team_preferences_ranked"`
	TeamInterestReason          string                   `gorm:"type:text;not null" json:"team_interest_reason"`
	Interests                   pq.StringArray           `gorm:"type:text[];not null;default:'{}'" json:"interests"`
	Availability                string                   `gorm:"not null" json:"availability"`
	Contribution                string                   `gorm:"type:text;not null" json:"contribution"`
	DataRetentionConsent        bool                     `gorm:"not null;default:false" json:"data_retention_consent"`
	Status                      GeneralApplicationStatus `gorm:"not null;default:'available'" json:"status"`
	InterviewingByUserID        *uuid.UUID               `gorm:"type:uuid" json:"interviewing_by_user_id"`
	InterviewingByEmail         string                   `gorm:"default:''" json:"interviewing_by_email"`
	InterviewedBy               pq.StringArray           `gorm:"type:text[];not null;default:'{}'" json:"interviewed_by"`
	InterviewInviteSentAt       *time.Time               `json:"interview_invite_sent_at"`
	TeamQuestionsInviteSentAt   *time.Time               `json:"team_questions_invite_sent_at"`
	TeamQuestionsReminderSentAt *time.Time               `json:"team_questions_reminder_sent_at"`
	// Only the final-call sender updates this marker, explicitly after delivery.
	// Excluding it from ordinary updates prevents stale full-record saves from clearing it.
	TeamQuestionsFinalCallSentAt *time.Time `gorm:"<-:create" json:"team_questions_final_call_sent_at"`
	FastTracked                  bool       `gorm:"not null;default:false" json:"fast_tracked"`
	FastTrackedByEmail           string     `gorm:"default:''" json:"fast_tracked_by_email"`
	FastTrackedAt                *time.Time `json:"fast_tracked_at"`
	FastTrackReason              string     `gorm:"type:text;default:''" json:"fast_track_reason"`
	AssignedTeam                 string     `gorm:"default:''" json:"assigned_team"`
	FinalizedByEmail             string     `gorm:"default:''" json:"finalized_by_email"`
	FinalizedAt                  *time.Time `json:"finalized_at"`
	// RejectionEmailSentAt is claimed (see claimRejectionEmail) just before
	// the rejection email is actually sent — by AdminFinalizeDecision for an
	// individually rejected applicant, or by the end-of-cycle bulk sweep
	// (see AdminSendRejectionsBulk) for applicants who never went through an
	// individual decision at all (never interviewed, or marked ineligible).
	// Tracked separately from FinalizedAt/Status so both paths, and any
	// concurrent overlap between them, can never send the same applicant two
	// rejection emails.
	RejectionEmailSentAt *time.Time `json:"rejection_email_sent_at"`
	// OnboardingNotifiedAt is stamped once notifyOnboardingService actually
	// succeeds for an accepted applicant — this is their only welcome notice
	// now that AdminFinalizeDecision no longer sends its own acceptance
	// email directly, so a still-nil value on an accepted application is the
	// visible sign that they were never actually notified and
	// AdminRetryOnboardingNotify needs to be run for them.
	OnboardingNotifiedAt *time.Time `json:"onboarding_notified_at"`
	// KthaisEmail records the @kthais.com address onboarding-service created
	// for this applicant once account provisioning finished — pure
	// bookkeeping so admins can see, from the application itself, that
	// onboarding is complete and what the resulting account is. It does not
	// create or link to a User/Profile row: the member gets those the normal
	// way, by logging in with this address (see AuthHandler.GoogleCallback).
	KthaisEmail string     `gorm:"default:''" json:"kthais_email"`
	OnboardedAt *time.Time `json:"onboarded_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
