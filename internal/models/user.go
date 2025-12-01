package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	RoleUser   = "user"
	RoleMember = "member"
	RoleAdmin  = "admin"
)

type User struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Email     string         `gorm:"uniqueIndex;not null" json:"email"`
	Provider  string         `gorm:"not null;default:'magic-link'" json:"provider"`
	Roles     pq.StringArray `gorm:"type:text[];default:'{user}'" json:"roles"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
