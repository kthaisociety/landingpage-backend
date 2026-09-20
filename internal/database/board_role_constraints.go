package database

import (
	"log"

	"gorm.io/gorm"
)

// EnsureBoardRoleConstraints adds a DB-level backstop for the "exactly one
// holder" invariant that TransferBoardRole enforces at the application
// layer (see Profile.BoardRole's doc comment): without it, a direct/
// out-of-band write could give two profiles the same exactly-one role, and
// ListBoardRoleHolders would silently show only the last one it reads,
// hiding the conflict. board_advisor is excluded since any number of
// holders (including zero) is valid for it. Runs unconditionally on every
// API boot, right after AutoMigrate — CREATE INDEX IF NOT EXISTS makes it a
// no-op once applied. Deliberately not a CHECK constraint on the valid
// role-name set itself — no other enum-like string column in this codebase
// (Programme, GeneralApplicationStatus, etc.) has one; that stays an
// application-layer invariant, consistent with existing convention.
func EnsureBoardRoleConstraints(db *gorm.DB) {
	if err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_board_role_single_holder
		ON profiles (board_role)
		WHERE board_role <> '' AND board_role <> 'board_advisor'
	`).Error; err != nil {
		log.Printf("[board role constraints] failed to create single-holder unique index: %v", err)
	}
}
