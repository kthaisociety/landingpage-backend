package models

import (
	"time"

	"github.com/google/uuid"
)

type TeamProjectPair struct {
	TeamID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"team_id"`
	ProjectID uuid.UUID `gorm:"type:uuid;primaryKey" json:"project_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
