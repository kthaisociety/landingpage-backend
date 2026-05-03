package models

import (
	"github.com/google/uuid"
	// "github.com/lib/pq"
	"gorm.io/gorm"
)

type ProjectStatus string

// const (
// 	ProjectStatusPlanning  ProjectStatus = "planning"
// 	ProjectStatusActive    ProjectStatus = "active"
// 	ProjectStatusCompleted ProjectStatus = "completed"
// )

const (
	ProjectStatusIdea        ProjectStatus = "Idea"
	ProjectStatusPrototype   ProjectStatus = "Prototype"
	ProjectStatusDevelopment ProjectStatus = "In development"
	ProjectStatusBeta        ProjectStatus = "Public beta"
	ProjectStatusLive        ProjectStatus = "Live"
)

type Project struct {
	gorm.Model
	ProjectId   uuid.UUID `gorm:"uniqueIndex;default:gen_random_uuid()" json:"project_id"`
	ProjectName string    `gorm:"not null" json:"title"`
	// Description string         `gorm:"type:text" json:"description"`
	// Skills      pq.StringArray `gorm:"type:text[]" json:"skills"`
	// Status      ProjectStatus  `gorm:"type:varchar(20);default:'planning'" json:"status"`
	OneLineDescription string        `gorm:"not null" json:"one_line_description"`
	Categories         string        `gorm:"not null" json:"categories"`
	TechStack          string        `gorm:"not null" json:"tech_stack"`
	ProblemImpact      string        `gorm:"type:text;not null" json:"problem_impact"`
	KeyFeatures        string        `gorm:"type:text;not null" json:"key_features"` // JSON stringified array of strings with <SEP> as delimiter
	Status             ProjectStatus `gorm:"type:varchar(50);not null" json:"status"`
	Screenshots        string        `gorm:"type:text" json:"screenshots"` // JSON stringified array of object
	RepoUrl            string        `json:"repo_url"`
	Affiliations       string        `json:"affiliations"`
	Timeline           string        `gorm:"type:text" json:"timeline"` // JSON stringified object
	MaintenancePlan    string        `gorm:"type:text" json:"maintenance_plan"`
	Contact            string        `json:"contact"`
}
