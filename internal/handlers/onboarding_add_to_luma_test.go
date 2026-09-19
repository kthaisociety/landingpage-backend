package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/luma"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOnboardingHandlerAddToLuma covers POST /internal/onboarding/add-to-luma
// end to end against a fake Luma server — auth gating, validation, and the
// real HTTP contract (URL, x-luma-api-key header, JSON body). No DB needed:
// this route never touches it, unlike SendEmail/RecordAccount (see
// TestOnboardingHandler, which needs real Postgres and is skipped without
// it).
func TestOnboardingHandlerAddToLuma(t *testing.T) {
	cfg := &config.Config{OnboardingServiceSecret: "test-onboarding-service-secret"}
	cfg.Luma.MembersTierID = "tier-123"

	var gotAPIKey string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-luma-api-key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"membership_id":"mem-1","status":"approved"}`))
	}))
	t.Cleanup(server.Close)

	lumaApi := &luma.LumaAPI{
		APIKey:        "test-luma-key",
		MembersTierID: cfg.Luma.MembersTierID,
		BaseURL:       server.URL,
		HTTPClient:    server.Client(),
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewOnboardingHandler(nil, cfg, lumaApi).Register(api)

	post := func(t *testing.T, body map[string]string, secret string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/internal/onboarding/add-to-luma", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("X-Service-Secret", secret)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	validBody := map[string]string{"email": "grace.hopper@kthais.com"}

	t.Run("missing shared secret is rejected", func(t *testing.T) {
		rec := post(t, validBody, "")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("wrong shared secret is rejected", func(t *testing.T) {
		rec := post(t, validBody, "wrong-secret")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-kthais.com email is rejected", func(t *testing.T) {
		rec := post(t, map[string]string{"email": "grace.hopper@example.com"}, cfg.OnboardingServiceSecret)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid request succeeds against the real HTTP contract", func(t *testing.T) {
		rec := post(t, validBody, cfg.OnboardingServiceSecret)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "test-luma-key", gotAPIKey)
		require.Equal(t, "grace.hopper@kthais.com", gotBody["email"])
		require.Equal(t, "tier-123", gotBody["membership_tier_id"])
	})
}
