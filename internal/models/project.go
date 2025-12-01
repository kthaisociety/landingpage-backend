package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type ProjectStatus string

const (
	ProjectStatusPlanning  ProjectStatus = "planning"
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusCompleted ProjectStatus = "completed"
)

type Project struct {
	ProjectID   uuid.UUID      `gorm:"type:uuid;primaryKey" json:"project_id"`
	ProjectName string         `gorm:"not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	Skills      pq.StringArray `gorm:"type:text[]" json:"skills"`
	Status      ProjectStatus  `gorm:"type:varchar(20);default:'Active'" json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}
