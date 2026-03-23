package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type JobListing struct {
	gorm.Model
	Id          uuid.UUID `gorm:"uniqueIndex" json:"id"`
	Name        string    `json:"title"`
	Description string    `json:"description"`
	Salary      string    `json:"salary"` // usually a range, or list of ints?
	Location    string    `json:"location"`
	JobType     string    `json:"jobType"` // full-time, part-time, internship, etc.
	CompanyId   uuid.UUID `json:"company"`
	StartDate   time.Time `json:"startdate"`
	EndDate     time.Time `json:"enddate"`
	AppUrl      string    `json:"appurl"`
	ContactInfo string    `json:"contact"`
}
