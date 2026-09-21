package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	NewManualOnboardingHandler(nil, cfg).Register(api)

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
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := listRecords(t, engine, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `[{"id":1,"first_name":"Ada","state":"notified"}]`, rec.Body.String())
	})

	t.Run("returns an empty array when onboarding-service isn't configured", func(t *testing.T) {
		cfg.OnboardingServiceURL = ""

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := listRecords(t, engine, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `[]`, rec.Body.String())
	})

	t.Run("non-admin is rejected", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

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

	actions := []struct {
		route        string
		internalPath string
	}{
		{"cancel", "/internal/onboarding/cancel"},
		{"restart", "/internal/onboarding/restart"},
		{"retry", "/internal/onboarding/retry-provisioning"},
		{"delete-record", "/internal/onboarding/delete-record"},
	}
	for _, tc := range actions {
		action := tc.route
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
				NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, adminCookie(t))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, tc.internalPath, gotPath)
				require.Equal(t, "test-onboarding-service-secret", gotSecret)
				require.Equal(t, uint(5), gotBody["id"])
			})

			t.Run("missing id is rejected", func(t *testing.T) {
				cfg.OnboardingServiceURL = "http://example.invalid"
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{}, adminCookie(t))
				require.Equal(t, http.StatusBadRequest, rec.Code)
			})

			t.Run("unconfigured onboarding service fails loudly, not silently", func(t *testing.T) {
				cfg.OnboardingServiceURL = ""
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, adminCookie(t))
				require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			})

			t.Run("non-admin is rejected", func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				engine := gin.New()
				NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

				rec := postAction(t, engine, "/api/v1/admin/onboarding/"+action, map[string]any{"id": 5}, nil)
				require.Equal(t, http.StatusUnauthorized, rec.Code)
			})
		})
	}
}

func TestOnboardingEmailSettings(t *testing.T) {
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

	do := func(t *testing.T, engine *gin.Engine, method, path string, body map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var reader *strings.Reader
		if body != nil {
			payload, err := json.Marshal(body)
			require.NoError(t, err)
			reader = strings.NewReader(string(payload))
		} else {
			reader = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("GET proxies straight through to onboarding-service", func(t *testing.T) {
		var gotPath, gotSecret string
		fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotSecret = r.Header.Get("X-Service-Secret")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"intro_text":"Welcome!"}`))
		}))
		t.Cleanup(fakeOnboardingService.Close)
		cfg.OnboardingServiceURL = fakeOnboardingService.URL

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "GET", "/api/v1/admin/onboarding/email-settings", nil, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "/internal/onboarding/email-settings", gotPath)
		require.Equal(t, "test-onboarding-service-secret", gotSecret)
		require.Contains(t, rec.Body.String(), "Welcome!")
	})

	t.Run("GET fails loudly when unconfigured", func(t *testing.T) {
		cfg.OnboardingServiceURL = ""
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "GET", "/api/v1/admin/onboarding/email-settings", nil, adminCookie(t))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("GET requires admin auth", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "GET", "/api/v1/admin/onboarding/email-settings", nil, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("PUT attaches the admin's own identity, not anything client-supplied", func(t *testing.T) {
		var gotBody map[string]string
		fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"start_intro_text":"` + gotBody["start_intro_text"] + `"}`))
		}))
		t.Cleanup(fakeOnboardingService.Close)
		cfg.OnboardingServiceURL = fakeOnboardingService.URL

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "PUT", "/api/v1/admin/onboarding/email-settings",
			map[string]any{
				"start_intro_text":      "So excited to have you!",
				"confirm_intro_text":    "Almost there!",
				"account_intro_text":    "Welcome aboard!",
				"mattermost_intro_text": "Say hi!",
			}, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "So excited to have you!", gotBody["start_intro_text"])
		require.Equal(t, "Almost there!", gotBody["confirm_intro_text"])
		require.Equal(t, "Welcome aboard!", gotBody["account_intro_text"])
		require.Equal(t, "Say hi!", gotBody["mattermost_intro_text"])
		require.Equal(t, "admin@kthais.com", gotBody["updated_by_email"])
	})

	t.Run("preview rejects an unknown kind", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "POST", "/api/v1/admin/onboarding/email-settings/preview",
			map[string]any{"kind": "bogus", "intro_text": "whatever"}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("preview rejects a malformed-but-syntactically-valid upstream response", func(t *testing.T) {
		fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`null`))
		}))
		t.Cleanup(fakeOnboardingService.Close)
		cfg.OnboardingServiceURL = fakeOnboardingService.URL

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		rec := do(t, engine, "POST", "/api/v1/admin/onboarding/email-settings/preview",
			map[string]any{"kind": "start", "intro_text": "whatever"}, adminCookie(t))
		require.Equal(t, http.StatusBadGateway, rec.Code, "a bare null body must never render as a blank-but-successful preview")
	})

	// fakeEmailSettingsPreviewServer mirrors what onboarding-service's own
	// EmailSettingsHandler.Preview actually returns per kind (subject/body
	// plus, for account/mattermost, a fixed real button; start/confirm get
	// only a button_text, since their real button_url is a per-record
	// portal token that doesn't exist for a preview) — see that handler's
	// own doc comment for the reasoning this mirrors.
	fakeEmailSettingsPreviewServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/internal/onboarding/email-settings/preview", r.URL.Path)
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

			var resp map[string]string
			switch body["kind"] {
			case "start":
				resp = map[string]string{"subject": "Welcome to KTH AI Society", "body": "Hi Alex,\n\nSo glad you're here!", "button_text": "Start onboarding"}
			case "confirm":
				resp = map[string]string{"subject": "Confirm your KTH email", "body": "Hi Alex,\n\nPlease confirm.", "button_text": "Continue to confirm"}
			case "account":
				resp = map[string]string{"subject": "Your KTH AI Society account", "body": "Hi Alex,\n\nWelcome aboard!", "button_url": "https://accounts.google.com/", "button_text": "Sign in with Google"}
			case "mattermost":
				resp = map[string]string{"subject": "Getting started with Mattermost", "body": "Hi Alex,\n\nSay hi!", "button_url": "https://chat.aisociety.se", "button_text": "Open Mattermost"}
			}
			payload, err := json.Marshal(resp)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		t.Cleanup(server.Close)
		return server
	}

	preview := func(t *testing.T, engine *gin.Engine, kind string) (subject, html string) {
		t.Helper()
		rec := do(t, engine, "POST", "/api/v1/admin/onboarding/email-settings/preview",
			map[string]any{"kind": kind, "intro_text": "whatever"}, adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			Subject string `json:"subject"`
			HTML    string `json:"html"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp.Subject, resp.HTML
	}

	t.Run("start and confirm previews get a local placeholder button URL, real button text", func(t *testing.T) {
		cfg.OnboardingServiceURL = fakeEmailSettingsPreviewServer(t).URL
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		startSubject, startHTML := preview(t, engine, "start")
		require.Equal(t, "Welcome to KTH AI Society", startSubject)
		require.Contains(t, startHTML, "<!DOCTYPE html>")
		require.Contains(t, startHTML, "Start onboarding")
		require.Contains(t, startHTML, previewSampleStartButtonURL)

		_, confirmHTML := preview(t, engine, "confirm")
		require.Contains(t, confirmHTML, "Continue to confirm")
		require.Contains(t, confirmHTML, previewSampleConfirmButtonURL)
	})

	t.Run("account and mattermost previews use onboarding-service's own real button", func(t *testing.T) {
		cfg.OnboardingServiceURL = fakeEmailSettingsPreviewServer(t).URL
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		_, accountHTML := preview(t, engine, "account")
		require.Contains(t, accountHTML, "Sign in with Google")
		require.Contains(t, accountHTML, "https://accounts.google.com/")
		require.NotContains(t, accountHTML, "Start onboarding")

		_, mattermostHTML := preview(t, engine, "mattermost")
		require.Contains(t, mattermostHTML, "Open Mattermost")
		require.Contains(t, mattermostHTML, "https://chat.aisociety.se")
	})

	t.Run("start and confirm previews fall back to a local button label against an older onboarding-service that omits button_text", func(t *testing.T) {
		fakeOnboardingService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"subject":"Welcome to KTH AI Society","body":"Hi Alex,\n\nSo glad you're here!"}`))
		}))
		t.Cleanup(fakeOnboardingService.Close)
		cfg.OnboardingServiceURL = fakeOnboardingService.URL

		gin.SetMode(gin.TestMode)
		engine := gin.New()
		NewManualOnboardingHandler(nil, cfg).Register(engine.Group("/api/v1"))

		_, startHTML := preview(t, engine, "start")
		require.Contains(t, startHTML, "Start onboarding")
		require.NotContains(t, startHTML, "Contact us")
	})
}

// TestOnboardingContractTemplate covers the admin upload/metadata endpoints
// backing onboarding-service's contract email — the other end of this
// (OnboardingHandler.GetContractTemplate, which onboarding-service actually
// fetches from) lives in this same handler package but is exercised
// implicitly through these same rows. Needs a real Postgres connection,
// same "skip if no .env" convention as
// TestAdminUpdateSettingsPreservesRecruitmentOpensAtWhenOmitted.
func TestOnboardingContractTemplate(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	// Keeps this test independent of real R2 credentials — see
	// shouldStoreResumeInDatabase's own reasoning in
	// general_application_handler.go.
	cfg.DevelopmentMode = true
	cfg.JwtSigningKey = generateTestJWTKey(t)
	cfg.JwtValidatingKey = cfg.JwtSigningKey

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&models.OnboardingContractTemplate{}, &models.BlobData{}))
	require.NoError(t, db.Unscoped().Where("1 = 1").Delete(&models.OnboardingContractTemplate{}).Error)
	t.Cleanup(func() {
		db.Unscoped().Where("1 = 1").Delete(&models.OnboardingContractTemplate{})
	})

	adminCookie := func(t *testing.T) *http.Cookie {
		t.Helper()
		token, err := utils.WriteJWT("admin@kthais.com", []string{"user", "member", "admin"}, uuid.New(), cfg.JwtSigningKey, 60)
		require.NoError(t, err)
		return &http.Cookie{Name: "jwt", Value: token}
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewManualOnboardingHandler(db, cfg).Register(engine.Group("/api/v1"))

	getMeta := func(t *testing.T) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/admin/onboarding/contract-template", nil)
		req.AddCookie(adminCookie(t))
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	upload := func(t *testing.T, filename, content string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, err := writer.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		req := httptest.NewRequest("POST", "/api/v1/admin/onboarding/contract-template", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("no file uploaded yet", func(t *testing.T) {
		rec := getMeta(t)
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"uploaded":false}`, rec.Body.String())
	})

	t.Run("upload requires admin auth", func(t *testing.T) {
		rec := upload(t, "contract.pdf", "%PDF-1.4 fake", nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("uploading stores the file and metadata reflects it", func(t *testing.T) {
		rec := upload(t, "contract-2026.pdf", "%PDF-1.4 fake contract bytes", adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, true, body["uploaded"])
		require.Equal(t, "contract-2026.pdf", body["file_name"])
		require.Equal(t, "admin@kthais.com", body["updated_by_email"])

		metaRec := getMeta(t)
		require.Contains(t, metaRec.Body.String(), "contract-2026.pdf")
	})

	t.Run("re-uploading replaces the stored file, not adds a second one", func(t *testing.T) {
		rec := upload(t, "contract-v2.pdf", "%PDF-1.4 a newer version", adminCookie(t))
		require.Equal(t, http.StatusOK, rec.Code)

		var count int64
		require.NoError(t, db.Model(&models.OnboardingContractTemplate{}).Count(&count).Error)
		require.Equal(t, int64(1), count, "the singleton row is updated in place, not duplicated")

		metaRec := getMeta(t)
		require.Contains(t, metaRec.Body.String(), "contract-v2.pdf")
		require.NotContains(t, metaRec.Body.String(), "contract-2026.pdf")
	})
}
