package models

import (
	"github.com/google/uuid"
)

type TeamUserPair struct {
	TeamID uuid.UUID `gorm:"primaryKey;foreignKey:TeamID" json:"teamID"`
	UserID uuid.UUID `gorm:"primaryKey;foreignKey:UserID" json:"userID"`
}
