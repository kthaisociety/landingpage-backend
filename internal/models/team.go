package models

import (
	"github.com/google/uuid"
)

type Team struct {
	TeamID   uuid.UUID `gorm:"primaryKey" json:"team_id"`
	TeamName string    `gorm:"not null;unique" json:"team_name"`
}
