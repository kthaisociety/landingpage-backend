package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	NewsletterSourceForm             = "newsletter_form"
	NewsletterSourceApplicationOptIn = "application_opt_in"
)

type NewsletterSubscription struct {
	gorm.Model
	Id                   uuid.UUID      `gorm:"uniqueIndex" json:"id"`
	FirstName            string         `gorm:"not null" json:"first_name"`
	LastName             string         `gorm:"not null" json:"last_name"`
	Email                string         `gorm:"not null" json:"email"`
	EmailNormalized      string         `gorm:"not null;uniqueIndex" json:"-"`
	Gender               string         `gorm:"not null;default:'Prefer not to say'" json:"gender"`
	University           string         `gorm:"not null;default:''" json:"university"`
	Programme            string         `gorm:"not null;default:''" json:"programme"`
	GraduationYear       int            `gorm:"not null;default:0" json:"graduation_year"`
	Interests            pq.StringArray `gorm:"type:text[];not null;default:'{}'" json:"interests"`
	DataRetentionConsent bool           `gorm:"not null;default:false" json:"data_retention_consent"`
	Source               string         `gorm:"not null;default:'newsletter_form'" json:"source"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}
