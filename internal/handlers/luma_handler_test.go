package handlers

import (
	"encoding/json"
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

// TestLumaHandler covers admin-auth gating and validation. No real Luma API
// key is configured, so the "add-member" call itself is never expected to
// succeed here — see internal/luma's AddMemberToTier for the actual Luma
// HTTP call.
func TestLumaHandler(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{JwtSigningKey: jwtKey, JwtValidatingKey: jwtKey}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewLumaHandler(cfg, nil).Register(api)

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

	addMember := func(t *testing.T, body map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/admin/luma/add-member", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	validBody := map[string]string{"email": "grace.hopper@kthais.com"}

	t.Run("no cookie is rejected", func(t *testing.T) {
		rec := addMember(t, validBody, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-admin is rejected", func(t *testing.T) {
		rec := addMember(t, validBody, nonAdminCookie(t))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-kthais.com email is rejected", func(t *testing.T) {
		rec := addMember(t, map[string]string{"email": "grace.hopper@example.com"}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing email is rejected", func(t *testing.T) {
		rec := addMember(t, map[string]string{}, adminCookie(t))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("admin with valid email reaches the luma call, which fails since luma isn't configured here", func(t *testing.T) {
		rec := addMember(t, validBody, adminCookie(t))
		require.Equal(t, http.StatusBadGateway, rec.Code)
	})
}
