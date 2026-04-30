package models

import (
	"time"

	"gorm.io/gorm"
)

// viv hot take - we don't need this
// update: Each member may have different role in different project
type TeamMember struct {
	ID                   uint           `gorm:"primarykey" json:"id"`
	UserID               uint           `gorm:"not null" json:"user_id"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"index" json:"-"`
	TeamMemberRole       string         `gorm:"type:text" json:"role"`
	TeamMemberDepartment string         `gorm:"type:text" json:"team"`
}
