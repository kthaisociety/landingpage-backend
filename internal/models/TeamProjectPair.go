package models

import (
	"github.com/google/uuid"
)

type TeamProjectPair struct {
	TeamID    uuid.UUID `gorm:"type:uuid;primaryKey;foreignKey:TeamID" json:"team_id"`
	ProjectID uuid.UUID `gorm:"type:uuid;primaryKey;foreignKey:ProjectID" json:"project_id"`
}
