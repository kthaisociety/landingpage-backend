package database

import (
	"fmt"
	"os"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestBackfillHeadOfITRole needs a real Postgres connection (it reads and
// writes Profile), so it follows the same "skip if no .env" convention as
// TestBoardRoleHandler in internal/handlers/board_role_handler_test.go.
func TestBackfillHeadOfITRole(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.Profile{}))

	// AutoMigrate never creates is_head_of_it under the current Profile
	// model, so add it directly here to simulate what a pre-migration
	// production database looks like, and drop it again afterward so this
	// test doesn't leave a permanent schema change behind.
	require.NoError(t, db.Exec(`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS is_head_of_it boolean DEFAULT false`).Error)
	t.Cleanup(func() {
		db.Exec(`ALTER TABLE profiles DROP COLUMN IF EXISTS is_head_of_it`)
	})

	legacyHolderEmail := "backfill-head-of-it-legacy@example.com"
	alreadySetEmail := "backfill-head-of-it-already-set@example.com"
	t.Cleanup(func() {
		emails := []string{legacyHolderEmail, alreadySetEmail}
		db.Where("email IN ?", emails).Unscoped().Delete(&models.Profile{})
		db.Where("email IN ?", emails).Unscoped().Delete(&models.User{})
	})

	mustCreateProfile := func(email, firstName string, boardRole string) {
		userID := uuid.New()
		require.NoError(t, db.Create(&models.User{
			UserId:   userID,
			Email:    email,
			Provider: "test",
			Roles:    pq.StringArray{"user", "member"},
		}).Error)
		var user models.User
		require.NoError(t, db.Where("email = ?", email).First(&user).Error)
		require.NoError(t, db.Create(&models.Profile{
			UserUUID:  userID,
			UserId:    user.ID,
			Email:     email,
			FirstName: firstName,
			LastName:  "Test",
			BoardRole: boardRole,
		}).Error)
		require.NoError(t, db.Exec(`UPDATE profiles SET is_head_of_it = true WHERE email = ?`, email).Error)
	}

	// A legacy holder with no board_role yet must be migrated.
	mustCreateProfile(legacyHolderEmail, "Legacy", "")
	// A profile that already has a board_role set must never be
	// overwritten, even if is_head_of_it is also (incorrectly) true for it.
	mustCreateProfile(alreadySetEmail, "Already", models.BoardRoleTreasurer)

	BackfillHeadOfITRole(db)

	var legacyHolder models.Profile
	require.NoError(t, db.Where("email = ?", legacyHolderEmail).First(&legacyHolder).Error)
	require.Equal(t, models.BoardRoleHeadOfIT, legacyHolder.BoardRole, "legacy is_head_of_it holder must be migrated to board_role")

	var alreadySet models.Profile
	require.NoError(t, db.Where("email = ?", alreadySetEmail).First(&alreadySet).Error)
	require.Equal(t, models.BoardRoleTreasurer, alreadySet.BoardRole, "a profile with an existing board_role must never be overwritten")
}
