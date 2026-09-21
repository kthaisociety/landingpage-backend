package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestDeleteUserProtectsAccountsHoldingBoardRoles is a regression for a
// Greptile finding on the offboarding PR, generalized for the exactly-one/
// transfer-only board-role model (see Profile.BoardRole's doc comment):
// DeleteUser (the plain "Delete user" admin action, unrelated to member
// offboarding) deleted a profile outright with no awareness of whether it
// held a board role — deleting the sole holder of one of the eight
// exactly-one roles (Head of IT among them) would leave nobody able to
// transfer it away through the app, leaving that role stuck until someone
// manually fixed the database. Board Advisor is the deliberate exception:
// any number of advisors, including zero, is valid, so deleting one is
// never blocked. Needs a real Postgres connection (the check locks and
// reads the target's own Profile row), so it follows the same "skip if no
// .env" convention as TestOffboardingHandler.
func TestDeleteUserProtectsAccountsHoldingBoardRoles(t *testing.T) {
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

	headOfIT := mustCreateHeadOfIT(t, db, cfg, "delete-user-head-of-it@example.com")
	treasurer := mustCreateBoardRoleHolder(t, db, cfg, "delete-user-treasurer@example.com", models.BoardRoleTreasurer)
	advisor := mustCreateBoardRoleHolder(t, db, cfg, "delete-user-advisor@example.com", models.BoardRoleBoardAdvisor)
	requester := mustCreateAdmin(t, db, cfg, "delete-user-requester@example.com")
	t.Cleanup(func() {
		for _, email := range []string{headOfIT.email, treasurer.email, advisor.email, requester.email} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAdminHandler(db, cfg).Register(engine.Group("/api/v1"))

	del := func(t *testing.T, email string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+email, nil)
		req.AddCookie(requester.cookie)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	// Board Advisor, the multi-holder exception, is never blocked — even
	// as the only advisor, deleting them threatens no invariant.
	rec := del(t, advisor.email)
	require.Equal(t, http.StatusOK, rec.Code, "deleting a board advisor is always allowed")

	var advisorStillExists int64
	db.Model(&models.User{}).Where("email = ?", advisor.email).Count(&advisorStillExists)
	require.Zero(t, advisorStillExists, "the deleted user should actually be gone")

	// Any of the eight exactly-one roles blocks deletion — proven here for
	// both Head of IT and a non-IT role, since the check must not be
	// special-cased to just one.
	for _, holder := range []testAdmin{headOfIT, treasurer} {
		rec := del(t, holder.email)
		require.Equal(t, http.StatusConflict, rec.Code, "deleting %s must be refused", holder.email)

		var stillExists int64
		db.Model(&models.User{}).Where("email = ?", holder.email).Count(&stillExists)
		require.EqualValues(t, 1, stillExists, "the refused delete must not have taken effect")
	}

	var headOfITRoleIntact string
	require.NoError(t, db.Model(&models.Profile{}).Select("board_role").
		Where("email = ?", headOfIT.email).Scan(&headOfITRoleIntact).Error)
	require.Equal(t, models.BoardRoleHeadOfIT, headOfITRoleIntact, "the head of IT's profile must be untouched")
}

// TestListAllUsersIncludesDeactivatedWithTimestamp guards the deliberate
// choice behind ListAllUsers: it must keep returning a deactivated user
// (not silently drop them — the frontend still needs to find one, e.g. to
// finish permanently deleting them later) but must expose DeactivatedAt so
// the frontend can tell active and deactivated members apart itself.
func TestListAllUsersIncludesDeactivatedWithTimestamp(t *testing.T) {
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

	active := mustCreateAdmin(t, db, cfg, "list-users-active@example.com")
	deactivated := mustCreateAdmin(t, db, cfg, "list-users-deactivated@example.com")
	require.NoError(t, db.Model(&models.User{}).Where("email = ?", deactivated.email).
		Update("deactivated_at", time.Now()).Error)
	t.Cleanup(func() {
		for _, email := range []string{active.email, deactivated.email} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAdminHandler(db, cfg).Register(engine.Group("/api/v1"))

	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	req.AddCookie(active.cookie)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var rows []AdminUserRow
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))

	byEmail := map[string]AdminUserRow{}
	for _, row := range rows {
		byEmail[row.Email] = row
	}

	activeRow, ok := byEmail[active.email]
	require.True(t, ok, "an active user must still be listed")
	require.Nil(t, activeRow.DeactivatedAt)

	deactivatedRow, ok := byEmail[deactivated.email]
	require.True(t, ok, "a deactivated user must still be listed, not silently dropped")
	require.NotNil(t, deactivatedRow.DeactivatedAt, "the frontend needs this to tell them apart")
}
