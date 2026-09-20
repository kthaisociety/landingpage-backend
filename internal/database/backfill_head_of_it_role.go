package database

import (
	"log"

	"backend/internal/models"

	"gorm.io/gorm"
)

// BackfillHeadOfITRole migrates the legacy Profile.IsHeadOfIT bool column
// (superseded by Profile.BoardRole) to board_role="head_of_it" for whoever
// held it. Runs unconditionally on every API boot, right after AutoMigrate,
// the same way BackfillProfileSlugs does: it's the only way to reach
// production without a manual one-off step, and without it the real
// production Head of IT holder ends up with an empty BoardRole after
// deploy — unrecoverable via self-service transfer, since that requires
// already holding the role. The information_schema guard makes this a
// permanent no-op once the legacy column is eventually dropped manually —
// no coordination needed, matching this codebase's convention of never
// dropping columns via AutoMigrate (see Profile.Slug's doc comment).
func BackfillHeadOfITRole(db *gorm.DB) {
	var exists bool
	if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'profiles' AND column_name = 'is_head_of_it')`).Scan(&exists).Error; err != nil {
		log.Printf("[head of IT backfill] failed to check for legacy column: %v", err)
		return
	}
	if !exists {
		return
	}
	result := db.Exec(`UPDATE profiles SET board_role = ? WHERE is_head_of_it = true AND board_role = ''`, models.BoardRoleHeadOfIT)
	if result.Error != nil {
		log.Printf("[head of IT backfill] failed to migrate legacy is_head_of_it holder(s): %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		log.Printf("[head of IT backfill] migrated %d legacy is_head_of_it holder(s) to board_role=%q", result.RowsAffected, models.BoardRoleHeadOfIT)
	}
}
