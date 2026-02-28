package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Team struct {
	gorm.Model
	TeamId   uuid.UUID `gorm:"uniqueIndex" json:"team_id"`
	TeamName string    `gorm:"not null" json:"team_name"`
}
