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
	gorm.Model
	UserId    uuid.UUID      `gorm:"uniqueIndex" json:"user_id"`
	Email     string         `gorm:"uniqueIndex;not null" json:"email"`
	Provider  string         `gorm:"not null;default:'magic-link'" json:"provider"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Roles     pq.StringArray `json:"roles" gorm:"type:text[];default:'{user}'"`
	// DeactivatedAt is set when OffboardingHandler.Deactivate succeeds, and
	// is the only local signal that this account is currently suspended —
	// Deactivate deliberately leaves everything else about the local
	// record alone, since it's meant to be reversible from Google/
	// Mattermost's own admin consoles (see Deactivate's doc comment).
	// There's no in-app "reactivate" action to clear it: if an admin
	// reverses a deactivation from one of those consoles directly, this
	// stays stale until manually cleared or the account is re-added to
	// Luma individually via /admin/luma/add-member. That's a deliberate
	// tradeoff — a stale flag only causes a deactivated-then-reactivated
	// member to be skipped by a future LumaHandler.SyncAll, which is far
	// safer than SyncAll's alternative of silently re-adding every
	// currently-deactivated member to Luma on every run.
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
}
