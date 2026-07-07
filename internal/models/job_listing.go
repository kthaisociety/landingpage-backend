package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type JobListing struct {
	gorm.Model
	Id          uuid.UUID `gorm:"uniqueIndex;default:gen_random_uuid()" json:"id"`
	Name        string    `gorm:"not null" json:"title"`
	Description string    `gorm:"type:text;not null" json:"description"`
	Salary      string    `json:"salary"`
	Location    string    `gorm:"not null" json:"location"`
	JobType     string    `gorm:"not null" json:"jobType"`
	CompanyId   uuid.UUID `gorm:"not null" json:"company"`
	StartDate   time.Time `gorm:"not null" json:"startdate"`
	EndDate     time.Time `gorm:"not null" json:"enddate"`
	AppUrl      string    `json:"appurl"`
	ContactInfo string    `json:"contact"`

	ApplyClickCount int64 `gorm:"not null;default:0" json:"applyClickCount"`
}
