package handlers

import (
	"errors"

	"backend/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// errCannotDeleteLastHeadOfIT signals deleteUserAndProfile's lock-and-count
// check found the target is the only remaining Head of IT — a 409, not a
// 500, mirroring RevokeHeadOfIT's own refusal for the same invariant.
var errCannotDeleteLastHeadOfIT = errors.New("cannot delete the only remaining head of IT")

// deleteUserAndProfile deletes targetUserID's Profile and User rows,
// refusing with errCannotDeleteLastHeadOfIT if doing so would remove the
// last remaining Head of IT — since is_head_of_it=false with GrantHeadOfIT
// requiring already being one would leave nobody able to grant it back
// through the app. Locks every current head row for the duration of the
// check so this can't race a concurrent revoke/delete of the same last
// head. Shared by AdminHandler.DeleteUser (the plain "remove this login")
// and OffboardingHandler.Delete (which also has to fully remove the local
// record once the real Google Workspace + Mattermost accounts are gone —
// otherwise the member keeps showing up in the admin user list).
func deleteUserAndProfile(db *gorm.DB, targetUserID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var heads []models.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_head_of_it = ?", true).Find(&heads).Error; err != nil {
			return err
		}
		isLastHead := len(heads) == 1 && heads[0].UserId == targetUserID
		if isLastHead {
			return errCannotDeleteLastHeadOfIT
		}

		// Delete profile first (no FK cascade defined at DB level).
		if err := tx.Unscoped().Where("user_id = ?", targetUserID).Delete(&models.Profile{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("id = ?", targetUserID).Delete(&models.User{}).Error
	})
}
