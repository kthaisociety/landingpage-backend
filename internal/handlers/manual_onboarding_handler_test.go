package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// generateTestJWTKey returns a freshly generated RSA private key, PEM
// encoded. Used as both JwtSigningKey and JwtValidatingKey in tests —
// ParseAndVerify (internal/utils/jwt.go) accepts a private key PEM and
// derives the public key from it, so one generated key covers both. This
// keeps these tests independent of a real .env file (and its real signing
// keys), which CI never provides — previously these tests silently skipped
// in CI entirely.
func generateTestJWTKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

// TestManualOnboardingHandler covers admin-auth gating, validation, and the
// happy path. No Postgres needed — this handler never touches the
// database. OnboardingServiceURL is left unset so the fire-and-forget
// notify call is a logged no-op, not a real network call.
func TestManualOnboardingHandler(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{JwtSigningKey: jwtKey, JwtValidatingKey: jwtKey}

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
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{
		JwtSigningKey:           jwtKey,
		JwtValidatingKey:        jwtKey,
		OnboardingServiceSecret: "test-onboarding-service-secret",
	}

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

func TestOnboardingRecordActions(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{
		JwtSigningKey:           jwtKey,
		JwtValidatingKey:        jwtKey,
		OnboardingServiceSecret: "test-onboarding-service-secret",
	}

	adminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	postAction := func(t *testing.T, engine *gin.Engine, path string, body map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
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

	for _, action := range []string{"cancel", "restart"} {
		t.Run(action, func(t *testing.T) {
			t.Run("proxies to onboarding-service with the shared secret", func(t *testing.T) {
				var gotPath, gotSecret string
				var gotBody map[string]uint
				fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					gotSecret = r.Header.Get("X-Service-Secret")
					require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"id":5,"state":"` + action + `ed"}`))
				}))
				t.Cleanup(fakeOnboardingService.Close)
				cfg.OnboardingServiceURL = fakeOnboardingService.URL

				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, adminCookie(t))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, "/internal/onboarding/"+action, gotPath)
				require.Equal(t, "test-onboarding-service-secret", gotSecret)
				require.Equal(t, uint(5), gotBody["id"])
			})

			t.Run("missing id is rejected", func(t *testing.T) {
				cfg.OnboardingServiceURL = "http://example.invalid"
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{}, adminCookie(t))
				require.Equal(t, http.StatusBadRequest, rec.Code)
			})

			t.Run("unconfigured onboarding service fails loudly, not silently", func(t *testing.T) {
				cfg.OnboardingServiceURL = ""
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, adminCookie(t))
				require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			})

			t.Run("non-admin is rejected", func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, nil)
				require.Equal(t, http.StatusUnauthorized, rec.Code)
			})
		})
	}
}
