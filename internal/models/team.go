package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Team struct {
	TeamID    uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"team_id"`
	TeamName  string         `gorm:"not null" json:"team_name"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
