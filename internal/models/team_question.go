package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TeamQuestion is a single team-specific follow-up question, managed by that
// team's head from the admin UI rather than hardcoded. SortOrder controls
// display order within a team; new questions are appended to the end.
type TeamQuestion struct {
	gorm.Model
	Id   uuid.UUID `gorm:"uniqueIndex" json:"id"`
	Team string    `gorm:"not null;index" json:"team"`
	Text string    `gorm:"type:text;not null" json:"text"`
	// No gorm "default:" tag: the application always sets this explicitly on
	// create, and GORM silently substitutes a column default whenever a bool
	// field is left at its Go zero value (false) — which would make an
	// explicit "required: false" from the admin UI get saved as true.
	Required  bool `gorm:"not null" json:"required"`
	SortOrder int  `gorm:"not null;default:0" json:"-"`
}
