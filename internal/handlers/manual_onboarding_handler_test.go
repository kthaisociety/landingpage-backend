package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

// TestManualOnboardingHandler covers admin-auth gating, validation, and the
// happy path — same skip-if-no-.env convention as TestOnboardingHandler
// (needs real RSA keys to sign/verify a JWT), but no Postgres is needed at
// all: this handler never touches the database. OnboardingServiceURL is
// left unset so the fire-and-forget notify call is a logged no-op, not a
// real network call.
func TestManualOnboardingHandler(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (needs real JWT signing keys)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.OnboardingServiceURL = ""

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewManualOnboardingHandler(cfg).Register(api)

	adminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	nonAdminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("member@kthais.com", []string{"user", "member"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	create := func(t *testing.T, body map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/admin/onboarding/manual", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	validBody := map[string]string{
		"first_name":    "Board",
		"last_name":     "Member",
		"email":         "board.member@example.com",
		"assigned_team": "IT",
	}

	t.Run("no cookie is rejected", func(t *testing.T) {
		rec := create(t, validBody, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-admin is rejected", func(t *testing.T) {
		rec := create(t, validBody, nonAdminCookie(t))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("missing fields are rejected", func(t *testing.T) {
		rec := create(t, map[string]string{"first_name": "Board"}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid team is rejected", func(t *testing.T) {
		body := map[string]string{
			"first_name": "Board", "last_name": "Member",
			"email": "board.member@example.com", "assigned_team": "Marketing",
		}
		rec := create(t, body, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("whitespace-only name is rejected", func(t *testing.T) {
		body := map[string]string{
			"first_name": "   ", "last_name": "Member",
			"email": "board.member@example.com", "assigned_team": "IT",
		}
		rec := create(t, body, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("over-80-character name is rejected", func(t *testing.T) {
		body := map[string]string{
			"first_name": strings.Repeat("a", 81), "last_name": "Member",
			"email": "board.member@example.com", "assigned_team": "IT",
		}
		rec := create(t, body, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid email is rejected", func(t *testing.T) {
		body := map[string]string{
			"first_name": "Board", "last_name": "Member",
			"email": "not-an-email", "assigned_team": "IT",
		}
		rec := create(t, body, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid request from an admin succeeds", func(t *testing.T) {
		rec := create(t, validBody, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}
