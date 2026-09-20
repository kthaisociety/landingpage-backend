package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// mustCreateNonAdminMember creates a signed-in member with a Profile but no
// "admin" role — used to prove a head-of-IT transfer target must already
// be an admin.
func mustCreateNonAdminMember(t *testing.T, db *gorm.DB, email string) testAdmin {
	t.Helper()
	userID := uuid.New()
	require.NoError(t, db.Create(&models.User{
		UserId:   userID,
		Email:    email,
		Provider: "test",
		Roles:    pq.StringArray{"user", "member"},
	}).Error)
	require.NoError(t, db.Create(&models.Profile{
		UserUUID:  userID,
		UserId:    mustFindUserPK(t, db, email),
		Email:     email,
		FirstName: "Non",
		LastName:  "Admin",
	}).Error)
	return testAdmin{email: email, userID: userID}
}

// vacateBoardRoleHolder lets a test freely assign one of the eight
// exactly-one board roles to its own fixture despite
// idx_profiles_board_role_single_holder: a shared dev database may already
// have a real holder for role (e.g. seed_dev.go's devMembers), so this
// vacates that holder for the duration of the calling test and restores it
// via t.Cleanup, once the calling test's own fixtures using role are gone
// (register this call, and therefore this restore, before any t.Cleanup
// that deletes those fixtures — t.Cleanup is LIFO). No-op for
// board_advisor, which has no single holder to protect. Updates go through
// the already-loaded struct rather than a hand-built "id = ?": Profile
// embeds gorm.Model (a uint ID) but also declares its own uuid.UUID Id as
// the actual primary key column, so .ID would bind the wrong
// (always-zero) field.
func vacateBoardRoleHolder(t *testing.T, db *gorm.DB, role string) {
	t.Helper()
	if role == models.BoardRoleBoardAdvisor {
		return
	}
	var previousHolder models.Profile
	if err := db.Where("board_role = ?", role).First(&previousHolder).Error; err == nil {
		require.NoError(t, db.Model(&previousHolder).Update("board_role", "").Error)
		t.Cleanup(func(holder models.Profile, role string) func() {
			return func() {
				db.Model(&holder).Update("board_role", role)
			}
		}(previousHolder, role))
	}
}

// mustCreateBoardRoleHolder creates an admin and sets Profile.BoardRole
// directly in the database — the same one-off bootstrap every first holder
// of an exactly-one role needs (see Profile.BoardRole's doc comment); every
// change after that goes through TransferBoardRole. See
// vacateBoardRoleHolder for how this avoids colliding with a real holder
// already present in a shared dev database.
func mustCreateBoardRoleHolder(t *testing.T, db *gorm.DB, cfg *config.Config, email, role string) testAdmin {
	t.Helper()
	admin := mustCreateAdmin(t, db, cfg, email)
	vacateBoardRoleHolder(t, db, role)
	require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", email).Update("board_role", role).Error)
	return admin
}

// TestBoardRoleHandler needs a real Postgres connection (every check reads
// Profile), so it follows the same "skip if no .env" convention as
// TestOffboardingHandler.
func TestBoardRoleHandler(t *testing.T) {
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

	treasurer := mustCreateBoardRoleHolder(t, db, cfg, "board-role-treasurer@example.com", models.BoardRoleTreasurer)
	headOfIT := mustCreateBoardRoleHolder(t, db, cfg, "board-role-head-of-it@example.com", models.BoardRoleHeadOfIT)
	plainAdmin := mustCreateAdmin(t, db, cfg, "board-role-plain-admin@example.com")
	otherPlainAdmin := mustCreateAdmin(t, db, cfg, "board-role-other-plain-admin@example.com")
	allEmails := []string{treasurer.email, headOfIT.email, plainAdmin.email, otherPlainAdmin.email}
	t.Cleanup(func() {
		for _, email := range allEmails {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewBoardRoleHandler(db, cfg).Register(engine.Group("/api/v1"))

	post := func(t *testing.T, path string, body map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", path, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}
	get := func(t *testing.T, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}
	boardRoleOf := func(t *testing.T, email string) string {
		t.Helper()
		var profile models.Profile
		require.NoError(t, db.Where("email = ?", email).First(&profile).Error)
		return profile.BoardRole
	}

	t.Run("list board role holders", func(t *testing.T) {
		// Chairperson may already be held in a shared dev database (e.g. by
		// seed_dev.go's devMembers) — vacate it for the duration of this
		// assertion and restore whoever held it afterward, so the test
		// doesn't depend on ambient state it doesn't own. Updates go
		// through the already-loaded struct (same pattern as
		// BackfillMemberTeams — see its comment), not a hand-built
		// "id = ?": Profile embeds gorm.Model (a uint ID) but also declares
		// its own uuid.UUID Id as the actual primary key column, so .ID
		// would bind the wrong (always-zero) field.
		var previousChairperson models.Profile
		hadChairperson := db.Where("board_role = ?", models.BoardRoleChairperson).First(&previousChairperson).Error == nil
		if hadChairperson {
			require.NoError(t, db.Model(&previousChairperson).Update("board_role", "").Error)
			t.Cleanup(func(holder models.Profile) func() {
				return func() {
					db.Model(&holder).Update("board_role", models.BoardRoleChairperson)
				}
			}(previousChairperson))
		}

		rec := get(t, "/api/v1/admin/board-role", plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, treasurer.email, body[models.BoardRoleTreasurer])
		require.Equal(t, headOfIT.email, body[models.BoardRoleHeadOfIT])
		require.Nil(t, body[models.BoardRoleChairperson], "an unheld exactly-one role is null, not absent")
		require.IsType(t, []any{}, body[models.BoardRoleBoardAdvisor], "board_advisor is always an array")
	})

	t.Run("transfer", func(t *testing.T) {
		body := map[string]any{"role": models.BoardRoleTreasurer, "to_email": plainAdmin.email}

		rec := post(t, "/api/v1/admin/board-role/transfer", body, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "no cookie at all")

		rec = post(t, "/api/v1/admin/board-role/transfer", body, otherPlainAdmin.cookie)
		require.Equal(t, http.StatusForbidden, rec.Code, "requester does not currently hold this role")

		rec = post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleBoardAdvisor, "to_email": plainAdmin.email}, treasurer.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "board advisor has no single holder to transfer")

		rec = post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleTreasurer, "to_email": "nobody@example.com"}, treasurer.cookie)
		require.Equal(t, http.StatusNotFound, rec.Code, "recipient must have a profile")

		rec = post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleTreasurer, "to_email": headOfIT.email}, treasurer.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "recipient already holds a different board role")

		rec = post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleHeadOfIT, "to_email": plainAdmin.email}, headOfIT.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "recipient is already an admin, so this must succeed")
		require.Equal(t, models.BoardRoleHeadOfIT, boardRoleOf(t, plainAdmin.email))
		require.Equal(t, "", boardRoleOf(t, headOfIT.email), "the sender's role is cleared")
		// Hand it back so later subtests see the fixture in its original state.
		rec = post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleHeadOfIT, "to_email": headOfIT.email}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		rec = post(t, "/api/v1/admin/board-role/transfer", body, treasurer.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "the actual successful transfer")
		require.Equal(t, models.BoardRoleTreasurer, boardRoleOf(t, plainAdmin.email))
		require.Equal(t, "", boardRoleOf(t, treasurer.email))
		// Hand it back for the concurrent-transfer subtest below.
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", plainAdmin.email).Update("board_role", "").Error)
		require.NoError(t, db.Model(&models.Profile{}).Where("email = ?", treasurer.email).Update("board_role", models.BoardRoleTreasurer).Error)
	})

	// A head-of-IT recipient must already be an admin — same constraint the
	// old GrantHeadOfIT's resolveAdminTarget enforced, carried over since
	// granting IT privilege to a non-admin who couldn't reach the gated
	// endpoints anyway never made sense.
	t.Run("head of IT recipient must already be an admin", func(t *testing.T) {
		nonAdminEmail := "board-role-non-admin@example.com"
		mustCreateNonAdminMember(t, db, nonAdminEmail)
		t.Cleanup(func() {
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.User{})
		})

		rec := post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleHeadOfIT, "to_email": nonAdminEmail}, headOfIT.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, models.BoardRoleHeadOfIT, boardRoleOf(t, headOfIT.email), "the refused transfer must not have taken effect")
	})

	// Every /admin/board-role/* route requires the caller to already be an
	// admin, so transferring any of the other seven exactly-one roles to a
	// non-admin would create an administrative dead end — that recipient
	// could never call the transfer endpoint themselves to hand the role
	// onward. This applies the same admin-eligibility check the previous
	// subtest exercises for head_of_it to a non-head_of_it role too.
	t.Run("non-head-of-IT recipient must also already be an admin", func(t *testing.T) {
		nonAdminEmail := "board-role-non-admin-treasurer@example.com"
		mustCreateNonAdminMember(t, db, nonAdminEmail)
		t.Cleanup(func() {
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", nonAdminEmail).Unscoped().Delete(&models.User{})
		})

		rec := post(t, "/api/v1/admin/board-role/transfer",
			map[string]any{"role": models.BoardRoleTreasurer, "to_email": nonAdminEmail}, treasurer.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, models.BoardRoleTreasurer, boardRoleOf(t, treasurer.email), "the refused transfer must not have taken effect")
	})

	// Two concurrent transfers of the same role, to two different
	// recipients: the SELECT ... FOR UPDATE lock on the requester's own
	// profile row should serialize them, so exactly one succeeds and the
	// loser sees its own role already gone (403), never a 500.
	t.Run("concurrent transfers of the same role can't both succeed", func(t *testing.T) {
		targets := []string{plainAdmin.email, otherPlainAdmin.email}
		codes := make([]int, len(targets))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, target := range targets {
			wg.Add(1)
			go func(i int, target string) {
				defer wg.Done()
				<-start
				rec := post(t, "/api/v1/admin/board-role/transfer",
					map[string]any{"role": models.BoardRoleTreasurer, "to_email": target}, treasurer.cookie)
				codes[i] = rec.Code
			}(i, target)
		}
		close(start)
		wg.Wait()

		successes := 0
		for _, code := range codes {
			if code == http.StatusOK {
				successes++
			} else {
				require.Equal(t, http.StatusForbidden, code, "the loser must see its own role already gone, not a 500")
			}
		}
		require.Equal(t, 1, successes, "exactly one of the two concurrent transfers should win")

		var holderCount int64
		require.NoError(t, db.Model(&models.Profile{}).
			Where("email IN ? AND board_role = ?", targets, models.BoardRoleTreasurer).
			Count(&holderCount).Error)
		require.Equal(t, int64(1), holderCount, "exactly one recipient should hold treasurer after the race")

		// Clean up for anything after this subtest.
		db.Model(&models.Profile{}).Where("board_role = ?", models.BoardRoleTreasurer).Update("board_role", "")
	})

	t.Run("board advisor add/remove", func(t *testing.T) {
		rec := post(t, "/api/v1/admin/board-role/board-advisor/add", map[string]any{"email": plainAdmin.email}, otherPlainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "any admin can add a board advisor")
		require.Equal(t, models.BoardRoleBoardAdvisor, boardRoleOf(t, plainAdmin.email))

		rec = post(t, "/api/v1/admin/board-role/board-advisor/add", map[string]any{"email": headOfIT.email}, plainAdmin.cookie)
		require.Equal(t, http.StatusBadRequest, rec.Code, "adding someone who already holds a different board role is rejected")

		rec = post(t, "/api/v1/admin/board-role/board-advisor/add", map[string]any{"email": otherPlainAdmin.email}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code, "a second, simultaneous board advisor is allowed")

		listRec := get(t, "/api/v1/admin/board-role", plainAdmin.cookie)
		require.Equal(t, http.StatusOK, listRec.Code)
		var body struct {
			BoardAdvisor []string `json:"board_advisor"`
		}
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &body))
		// Subset, not exact match: board_advisor is global, shared state (see
		// its doc comment) — a shared dev database may already have other
		// advisors (e.g. seed_dev.go's devMembers) that this test doesn't own.
		require.Subset(t, body.BoardAdvisor, []string{plainAdmin.email, otherPlainAdmin.email})

		rec = post(t, "/api/v1/admin/board-role/board-advisor/remove", map[string]any{"email": plainAdmin.email}, otherPlainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "", boardRoleOf(t, plainAdmin.email))

		// Removing the last remaining advisor is allowed too — any number,
		// including zero, is a valid state.
		rec = post(t, "/api/v1/admin/board-role/board-advisor/remove", map[string]any{"email": otherPlainAdmin.email}, plainAdmin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "", boardRoleOf(t, otherPlainAdmin.email))
	})
}

// TestBackfillMemberTeams covers the three cases that matter for the
// one-time-ish bulk fix: a member with an empty Team and a matching
// accepted application gets backfilled, one with no matching application is
// left alone, and one whose Team is already set is never overwritten even
// when a (differently-teamed) accepted application also exists for them —
// this only ever fills in blanks.
func TestBackfillMemberTeams(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.Profile{}, &models.GeneralApplication{}))

	admin := mustCreateAdmin(t, db, cfg, "backfill-admin@example.com")

	suffix := uuid.New().String()[:8]
	matchedEmail := fmt.Sprintf("backfill-matched-%s@kthais.local", suffix)
	unmatchedEmail := fmt.Sprintf("backfill-unmatched-%s@kthais.local", suffix)
	alreadySetEmail := fmt.Sprintf("backfill-already-set-%s@kthais.local", suffix)
	boardRoleEmail := fmt.Sprintf("backfill-board-role-%s@kthais.local", suffix)

	mustCreateMemberProfile := func(t *testing.T, email, team, boardRole string) {
		t.Helper()
		userID := uuid.New()
		require.NoError(t, db.Create(&models.User{
			UserId: userID, Email: email, Provider: "test", Roles: pq.StringArray{"user", "member"},
		}).Error)
		require.NoError(t, db.Create(&models.Profile{
			UserUUID: userID, UserId: mustFindUserPK(t, db, email),
			Email: email, FirstName: "Backfill", LastName: "Test", Team: team, BoardRole: boardRole,
		}).Error)
	}
	mustCreateAcceptedApplication := func(t *testing.T, kthaisEmail, team string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		app := models.GeneralApplication{
			Id:                id,
			ApplicationYear:   2026,
			FirstName:         "Backfill",
			LastName:          "Test",
			Email:             fmt.Sprintf("backfill-personal-%s@example.com", id.String()[:8]),
			EmailNormalized:   fmt.Sprintf("backfill-personal-%s@example.com", id.String()[:8]),
			Programme:         "Computer Science",
			GraduationYear:    2027,
			LinkedinURL:       "https://linkedin.com/in/backfill-test",
			ResumeFileName:    "resume.pdf",
			ResumeContentType: "application/pdf",
			Teams:             []string{team},
			Availability:      "4-6 hours",
			Contribution:      "Contribution text.",
			Status:            models.GeneralApplicationStatusAccepted,
			KthaisEmail:       kthaisEmail,
			AssignedTeam:      team,
		}
		require.NoError(t, db.Create(&app).Error)
		return id
	}

	mustCreateMemberProfile(t, matchedEmail, "", "")
	mustCreateAcceptedApplication(t, matchedEmail, "Growth")

	mustCreateMemberProfile(t, unmatchedEmail, "", "")

	mustCreateMemberProfile(t, alreadySetEmail, "Research", "")
	mustCreateAcceptedApplication(t, alreadySetEmail, "Business")

	// A board-role holder with no team and a matching accepted application
	// — must still be left as "" (Unassigned): board roles aren't scoped to
	// any recruitment team, so this isn't a gap to fill.
	vacateBoardRoleHolder(t, db, models.BoardRoleTreasurer)
	mustCreateMemberProfile(t, boardRoleEmail, "", models.BoardRoleTreasurer)
	mustCreateAcceptedApplication(t, boardRoleEmail, "Development")

	t.Cleanup(func() {
		for _, email := range []string{admin.email, matchedEmail, unmatchedEmail, alreadySetEmail, boardRoleEmail} {
			db.Where("email = ?", email).Unscoped().Delete(&models.Profile{})
			db.Where("email = ?", email).Unscoped().Delete(&models.User{})
		}
		db.Where("kthais_email IN ?", []string{matchedEmail, alreadySetEmail, boardRoleEmail}).Unscoped().Delete(&models.GeneralApplication{})
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewBoardRoleHandler(db, cfg).Register(engine.Group("/api/v1"))

	req := httptest.NewRequest("POST", "/api/v1/admin/team/backfill", nil)
	req.AddCookie(admin.cookie)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// A fresh struct per lookup — reusing one across queries would carry its
	// already-populated Id into the next query's WHERE clause via GORM's
	// struct-condition merging, silently over-scoping it.
	var matchedProfile, unmatchedProfile, alreadySetProfile, boardRoleProfile models.Profile
	require.NoError(t, db.Where("email = ?", matchedEmail).First(&matchedProfile).Error)
	require.Equal(t, "Growth", matchedProfile.Team, "backfilled from the matching accepted application")

	require.NoError(t, db.Where("email = ?", unmatchedEmail).First(&unmatchedProfile).Error)
	require.Equal(t, "", unmatchedProfile.Team, "no matching application — left as Unassigned")

	require.NoError(t, db.Where("email = ?", alreadySetEmail).First(&alreadySetProfile).Error)
	require.Equal(t, "Research", alreadySetProfile.Team, "already had a team — never overwritten")

	require.NoError(t, db.Where("email = ?", boardRoleEmail).First(&boardRoleProfile).Error)
	require.Equal(t, "", boardRoleProfile.Team, "board-role holders are skipped, even with a matching application")
}
