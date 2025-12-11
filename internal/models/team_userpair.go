package models

import (
	"time"

	"github.com/google/uuid"
)

type TeamUserPair struct {
	TeamID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"team_id"`
	UserID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`
	JoinedAt time.Time `gorm:"autoCreateTime" json:"joined_at"`
}
