package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type GeneralApplicationStatus string

const (
	GeneralApplicationStatusPending  GeneralApplicationStatus = "pending"
	GeneralApplicationStatusReviewed GeneralApplicationStatus = "reviewed"
	GeneralApplicationStatusAccepted GeneralApplicationStatus = "accepted"
	GeneralApplicationStatusRejected GeneralApplicationStatus = "rejected"
)

type GeneralApplication struct {
	gorm.Model
	Id                    uuid.UUID                `gorm:"uniqueIndex" json:"id"`
	ApplicationYear       int                      `gorm:"not null;uniqueIndex:idx_general_application_year_email" json:"application_year"`
	FirstName             string                   `gorm:"not null" json:"first_name"`
	LastName              string                   `gorm:"not null" json:"last_name"`
	Email                 string                   `gorm:"not null" json:"email"`
	EmailNormalized       string                   `gorm:"not null;uniqueIndex:idx_general_application_year_email" json:"-"`
	Gender                string                   `gorm:"not null;default:'Prefer not to say'" json:"gender"`
	University            string                   `gorm:"not null;default:''" json:"university"`
	Programme             string                   `gorm:"not null" json:"programme"`
	GraduationYear        int                      `gorm:"not null" json:"graduation_year"`
	LinkedinURL           string                   `gorm:"not null" json:"linkedin_url"`
	AdditionalLinks       pq.StringArray           `gorm:"type:text[]" json:"additional_links"`
	ResumeBlobID          uuid.UUID                `gorm:"not null" json:"-"`
	ResumeData            []byte                   `gorm:"type:bytea" json:"-"`
	ResumeFileName        string                   `gorm:"not null" json:"resume_file_name"`
	ResumeContentType     string                   `gorm:"not null" json:"resume_content_type"`
	Teams                 pq.StringArray           `gorm:"type:text[];not null" json:"teams"`
	TeamPreferencesRanked bool                     `gorm:"not null;default:false" json:"team_preferences_ranked"`
	TeamInterestReason    string                   `gorm:"type:text;not null" json:"team_interest_reason"`
	Availability          string                   `gorm:"not null" json:"availability"`
	Contribution          string                   `gorm:"type:text;not null" json:"contribution"`
	DataRetentionConsent  bool                     `gorm:"not null;default:false" json:"data_retention_consent"`
	Status                GeneralApplicationStatus `gorm:"not null;default:'pending'" json:"status"`
	CreatedAt             time.Time                `json:"created_at"`
	UpdatedAt             time.Time                `json:"updated_at"`
}
