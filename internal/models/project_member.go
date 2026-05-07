package models

// ProjectMember is a simple join table linking users to projects they contributed to.
// It intentionally carries no role or department — that lives in TeamMember (profile team history).
type ProjectMember struct {
	ProjectID uint `gorm:"primaryKey;not null" json:"project_id"`
	UserID    uint `gorm:"primaryKey;not null" json:"user_id"`
}
