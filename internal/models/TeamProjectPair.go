package models

import (
	"github.com/google/uuid"
)

type TeamProjectPair struct {
	TeamId    uuid.UUID `gorm:"primaryKey" json:"team_id"`
	ProjectId uuid.UUID `gorm:"primaryKey" json:"project_id"`
}
