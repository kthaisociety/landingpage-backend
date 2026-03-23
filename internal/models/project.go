package models

import (
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
	gorm.Model
	ProjectId   uuid.UUID      `gorm:"uniqueIndex" json:"project_id"`
	ProjectName string         `gorm:"not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	Skills      pq.StringArray `gorm:"type:text[]" json:"skills"`
	Status      ProjectStatus  `gorm:"type:varchar(20);default:'planning'" json:"status"`
}
