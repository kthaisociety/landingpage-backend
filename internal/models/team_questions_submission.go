package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// TeamQuestionsSubmission stores a single applicant's answers to the team-specific
// follow-up form. One row per application; Answers is JSON-encoded as
// map[team]map[questionID]answer.
type TeamQuestionsSubmission struct {
	gorm.Model
	ApplicationID  uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex" json:"application_id"`
	Answers        string         `gorm:"type:text;not null;default:''" json:"answers"`
	WithdrawnTeams pq.StringArray `gorm:"type:text[];not null;default:'{}'" json:"withdrawn_teams"`
	SubmittedAt    time.Time      `gorm:"not null" json:"submitted_at"`
}
