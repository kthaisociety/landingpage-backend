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
		for _, email := range []string{"not-an-email", "a@.", "a@b.", "a@.b"} {
			body := map[string]string{
				"first_name": "Board", "last_name": "Member",
				"email": email, "assigned_team": "IT",
			}
			rec := create(t, body, adminCookie(t))
			require.Equal(t, http.StatusBadRequest, rec.Code, "email %q should be rejected", email)
		}
	})

	t.Run("valid request from an admin succeeds", func(t *testing.T) {
		rec := create(t, validBody, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestIsValidEmail(t *testing.T) {
	valid := []string{"a@b.c", "board.member@example.com", "a@sub.example.com"}
	invalid := []string{"", "not-an-email", "a@", "@b.c", "a@.", "a@b.", "a@.b", "a b@c.d"}

	for _, email := range valid {
		require.True(t, isValidEmail(email), "expected %q to be valid", email)
	}
	for _, email := range invalid {
		require.False(t, isValidEmail(email), "expected %q to be invalid", email)
	}
}

func TestManualOnboardingListRecords(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (needs real JWT signing keys)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	cfg.OnboardingServiceSecret = "test-onboarding-service-secret"

	adminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	listRecords := func(t *testing.T, engine *gin.Engine, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/admin/onboarding/records", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("proxies onboarding-service's response through unchanged", func(t *testing.T) {
		fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/internal/onboarding/records", r.URL.Path)
			require.Equal(t, "test-onboarding-service-secret", r.Header.Get("X-Service-Secret"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1,"first_name":"Ada","state":"notified"}]`))
		}))
		t.Cleanup(fakeOnboardingService.Close)
		cfg.OnboardingServiceURL = fakeOnboardingService.URL

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

		rec := listRecords(t, engine, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `[{"id":1,"first_name":"Ada","state":"notified"}]`, rec.Body.String())
	})

	t.Run("returns an empty array when onboarding-service isn't configured", func(t *testing.T) {
		cfg.OnboardingServiceURL = ""

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

		rec := listRecords(t, engine, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `[]`, rec.Body.String())
	})

	t.Run("non-admin is rejected", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

		rec := listRecords(t, engine, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
