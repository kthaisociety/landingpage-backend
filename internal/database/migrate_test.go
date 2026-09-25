package database

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openMigrationTestDB connects to Postgres using MIGRATION_TEST_DSN (how CI runs
// this) or a local .env (how the sibling handler tests run), and skips when
// neither is available. It returns the DSN alongside the connection.
//
// An explicit MIGRATION_TEST_DSN means someone asked for these assertions to
// run, so an unreachable server fails the test instead of skipping: skipping
// there would let CI report success without ever checking the schema, which is
// the regression this file exists to catch.
func openMigrationTestDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("MIGRATION_TEST_DSN"))
	explicit := dsn != ""
	if !explicit {
		envFile := "../../.env"
		if _, err := os.Stat(envFile); err != nil {
			t.Skip("skipping: set MIGRATION_TEST_DSN or provide a .env file (this test needs Postgres)")
		}
		require.NoError(t, godotenv.Load(envFile))

		cfg, err := config.LoadConfig()
		require.NoError(t, err)
		dsn = fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
			cfg.Database.Host, cfg.Database.User, cfg.Database.Password,
			cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		if explicit {
			require.NoErrorf(t, err,
				"MIGRATION_TEST_DSN is set, so Postgres must be reachable; refusing to skip and report success")
		}
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	return db, dsn
}

// TestMigrateKeepsRegistrationUserForeignKey guards the startup migration path.
//
// Registration.UserID and User.UserId (the uuid column "user_id") normalise to
// the same lookup name, so the Registration.User association is ambiguous. gorm
// >= 1.31 guesses has-one first and, without the explicit belongsTo tag, hangs
// the foreign key off users.user_id as a bigint instead of registrations.user_id
// — which makes AutoMigrate rewrite a populated UUID column and abort, taking
// startup down with it.
//
// Dropping the tag, or a future gorm changing its inference again, has to fail
// here rather than at boot on a deployed environment.
func TestMigrateKeepsRegistrationUserForeignKey(t *testing.T) {
	base, dsn := openMigrationTestDB(t)

	// Migrate into a throwaway schema so the test never touches whatever else
	// lives in the target database.
	schemaName := "migtest_" + uuid.New().String()[:8]
	require.NoError(t, base.Exec("CREATE SCHEMA "+schemaName).Error)
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schemaName + " CASCADE") })

	db, err := gorm.Open(
		postgres.Open(dsn+" search_path="+schemaName),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)

	t.Run("fresh schema", func(t *testing.T) {
		require.NoError(t, Migrate(db), "AutoMigrate must succeed against an empty schema")
		assertRegistrationUserWiring(t, db)
	})

	t.Run("populated schema", func(t *testing.T) {
		// A UUID in users.user_id is what turns the wrong relationship into a
		// hard failure: gorm tries to retype the populated column to bigint.
		require.NoError(t, db.Create(&models.User{
			UserId:   uuid.New(),
			Email:    fmt.Sprintf("migration-test-%s@example.com", uuid.New().String()[:8]),
			Provider: "magic-link",
		}).Error)

		require.NoError(t, Migrate(db), "AutoMigrate must stay idempotent against a populated schema")
		assertRegistrationUserWiring(t, db)
	})
}

func assertRegistrationUserWiring(t *testing.T, db *gorm.DB) {
	t.Helper()

	var userIDType string
	require.NoError(t, db.Raw(`
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'user_id'
	`).Scan(&userIDType).Error)
	require.Equal(t, "text", userIDType,
		"users.user_id must stay a text-backed UUID, not be retyped as a bigint foreign key")

	var registrationFK string
	require.NoError(t, db.Raw(`
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE contype = 'f' AND conrelid = (current_schema() || '.registrations')::regclass
		  AND conname = 'fk_registrations_user'
	`).Scan(&registrationFK).Error)
	require.Contains(t, registrationFK, "FOREIGN KEY (user_id)",
		"registrations.user_id must own the foreign key to users")
	require.Contains(t, registrationFK, "REFERENCES users(id)",
		"registrations.user_id must reference users.id")

	var inverted int64
	require.NoError(t, db.Raw(`
		SELECT count(*) FROM pg_constraint
		WHERE contype = 'f' AND conrelid = (current_schema() || '.users')::regclass
		  AND confrelid = (current_schema() || '.registrations')::regclass
	`).Scan(&inverted).Error)
	require.Zero(t, inverted,
		"users must not carry a foreign key back to registrations (relationship inverted)")
}
