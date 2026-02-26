package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	UserId    uuid.UUID `gorm:"uniqueIndex" json:"user_id"`
	Email     string    `gorm:"uniqueIndex;not null" json:"email"`
	Provider  string    `gorm:"not null;default:'magic-link'" json:"provider"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Roles     pq.StringArray `json:"roles" gorm:"type:text[]"`
}
