package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TeamQuestionsToken is a single-use, hashed access token for the public
// Team Questions follow-up form. Only TokenHash is persisted — the raw token
// is only ever embedded in the emailed link, never stored. Multiple rows may
// exist per ApplicationID (each resend issues a new one); lookups only
// consider the most recent unused, unexpired row.
type TeamQuestionsToken struct {
	gorm.Model
	ApplicationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"application_id"`
	TokenHash     string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	ExpiresAt     time.Time  `gorm:"not null" json:"expires_at"`
	UsedAt        *time.Time `json:"used_at"`
}
