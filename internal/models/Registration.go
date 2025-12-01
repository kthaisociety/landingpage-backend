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

type Registration struct {
	ID                  uint               `gorm:"primarykey" json:"id"`
	EventID             uint               `gorm:"not null" json:"event_id"`
	Event               Event              `gorm:"foreignKey:EventID" json:"event"`
	UserID              uint               `gorm:"not null" json:"user_id"`
	User                User               `gorm:"foreignKey:UserID" json:"user"`
	Status              RegistrationStatus `gorm:"not null" json:"status"`
	Attended            bool               `gorm:"not null" json:"attended"`
	DietaryRestrictions string             `json:"dietary_restrictions"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	DeletedAt           gorm.DeletedAt     `gorm:"index" json:"-"`
}
