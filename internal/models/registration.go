package models

import (
	"time"

	"gorm.io/gorm"
)

type RegistrationStatus string

const (
	RegistrationStatusPending  RegistrationStatus = "pending"
	RegistrationStatusApproved RegistrationStatus = "approved"
	RegistrationStatusRejected RegistrationStatus = "rejected"
)

// belongsTo below is load-bearing: User.UserId (column "user_id") collides with
// the UserID foreign key, and gorm >= 1.31 otherwise guesses has-one and migrates
// the FK onto users.user_id instead of registrations.user_id.
type Registration struct {
	ID                  uint               `gorm:"primarykey" json:"id"`
	EventID             uint               `gorm:"not null" json:"event_id"`
	Event               Event              `gorm:"foreignKey:EventID" json:"event"`
	UserID              uint               `gorm:"not null" json:"user_id"`
	User                User               `gorm:"belongsTo:User;foreignKey:UserID" json:"user"`
	Status              RegistrationStatus `gorm:"not null" json:"status"`
	Attended            bool               `gorm:"not null" json:"attended"`
	DietaryRestrictions string             `json:"dietary_restrictions"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	DeletedAt           gorm.DeletedAt     `gorm:"index" json:"-"`
}
