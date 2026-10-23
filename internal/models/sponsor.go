package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sponsor is a company that gives the society services or resources in
// exchange for visibility on the public sponsors page. Admins add, reorder
// and remove sponsors through /admin/sponsors.
type Sponsor struct {
	gorm.Model
	Id         uuid.UUID `gorm:"uniqueIndex" json:"id"`
	Name       string    `gorm:"not null" json:"name"`
	WebsiteUrl string    `json:"websiteUrl"`
	Logo       uuid.UUID `json:"logo"` // reference to a blob_data object
	// SortOrder controls placement on the page: lower comes first, ties fall
	// back to the order sponsors were added.
	SortOrder int `gorm:"not null;default:0" json:"sortOrder"`
}
