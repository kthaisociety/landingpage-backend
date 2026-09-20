package handlers

import (
	"errors"

	"backend/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// errCannotDeleteAccountHoldingBoardRole signals deleteUserAndProfile found
// the target currently holds one of the eight exactly-one board roles
// (everything except BoardRoleBoardAdvisor) — a 409, not a 500. Deleting
// them would leave that role with no holder, and since the only way any of
// those eight ever changes hands is a transfer initiated by its current
// holder (see Profile.BoardRole's doc comment), there would be nobody left
// who could ever transfer it again through the app.
var errCannotDeleteAccountHoldingBoardRole = errors.New("cannot delete an account holding a board role")

// deleteUserAndProfile deletes targetUserID's Profile and User rows,
// refusing with errCannotDeleteAccountHoldingBoardRole if the target
// currently holds one of the eight exactly-one board roles. Locks the
// target's own Profile row for the duration of the check so this can't race
// a concurrent transfer of the same role away from this same account.
// Board Advisor is deliberately excluded from this check — any number of
// advisors, including zero, is a valid state, so deleting one (even the
// last one) threatens no invariant. Shared by AdminHandler.DeleteUser (the
// plain "remove this login") and OffboardingHandler.Delete (which also has
// to fully remove the local record once the real Google Workspace +
// Mattermost accounts are gone — otherwise the member keeps showing up in
// the admin user list).
func deleteUserAndProfile(db *gorm.DB, targetUserID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var profile models.Profile
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ?", targetUserID).First(&profile).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && profile.BoardRole != "" && profile.BoardRole != models.BoardRoleBoardAdvisor {
			return errCannotDeleteAccountHoldingBoardRole
		}

		// Delete profile first (no FK cascade defined at DB level).
		if err := tx.Unscoped().Where("user_id = ?", targetUserID).Delete(&models.Profile{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("id = ?", targetUserID).Delete(&models.User{}).Error
	})
}
