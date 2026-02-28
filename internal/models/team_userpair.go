package models

import (
	"github.com/google/uuid"
)

type TeamUserPair struct {
	TeamId uuid.UUID `gorm:"primaryKey" json:"team_id"`
	UserId uuid.UUID `gorm:"primaryKey" json:"user_id"`
}
