package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
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
	"gorm.io/gorm/logger"
)

var (
	testPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	testSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>`)
)

// memBlobStore is an in-memory utils.BlobStore so sponsor tests never
// touch real R2.
type memBlobStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemBlobStore() *memBlobStore {
	return &memBlobStore{objects: map[string][]byte{}}
}

func (s *memBlobStore) GetObject(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return data, nil
}

func (s *memBlobStore) PutObject(key string, obj []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = obj
	return nil
}

func (s *memBlobStore) DeleteObject(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *memBlobStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// failingBlobStore simulates storage being down: every call errors.
type failingBlobStore struct{}

var errStorageDown = errors.New("storage unavailable")

func (failingBlobStore) GetObject(string) ([]byte, error) { return nil, errStorageDown }
func (failingBlobStore) PutObject(string, []byte) error   { return errStorageDown }
func (failingBlobStore) DeleteObject(string) error        { return errStorageDown }

func newSponsorTestEngine(h *SponsorHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	h.Register(engine.Group("/api/v1"))
	return engine
}

func testJWTCookie(t *testing.T, cfg *config.Config, roles []string) *http.Cookie {
	t.Helper()
	token, err := utils.WriteJWT("sponsor-test@kthais.com", roles, uuid.New(), cfg.JwtSigningKey, 60)
	require.NoError(t, err)
	return &http.Cookie{Name: "jwt", Value: token}
}

// doSponsorUpload posts a multipart sponsor form. A nil logo omits the file
// part entirely.
func doSponsorUpload(t *testing.T, engine *gin.Engine, fields map[string]string, logo []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	if logo != nil {
		part, err := writer.CreateFormFile("logo", "logo.png")
		require.NoError(t, err)
		_, err = part.Write(logo)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/api/v1/admin/sponsors", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestDetectSponsorLogoType(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantExt string
		wantOK  bool
	}{
		{"png", testPNG, "png", true},
		{"jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), "jpg", true},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "webp", true},
		{"svg", testSVG, "svg", true},
		{"svg with xml declaration", append([]byte(`<?xml version="1.0"?>`+"\n"), testSVG...), "svg", true},
		{"html containing svg", []byte(`<html><body><svg></svg></body></html>`), "", false},
		{"plain text", []byte("definitely not a logo"), "", false},
		{"gif", []byte("GIF89a\x01\x00\x01\x00"), "", false},
		{"empty", []byte{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext, ok := detectSponsorLogoType(tc.data)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantExt, ext)
		})
	}
}

// TestSponsorHandlerRejectsBadRequests covers auth gating and input
// validation. Every case is refused before the handler touches the
// database or blob store, so no Postgres is needed.
func TestSponsorHandlerRejectsBadRequests(t *testing.T) {
	jwtKey := generateTestJWTKey(t)
	cfg := &config.Config{JwtSigningKey: jwtKey, JwtValidatingKey: jwtKey}
	h := NewSponsorHandler(nil, cfg)
	h.openBlobStore = func() (utils.BlobStore, error) {
		t.Fatal("blob store must not be opened for a rejected request")
		return nil, nil
	}
	engine := newSponsorTestEngine(h)
	admin := testJWTCookie(t, cfg, []string{"user", "member", "admin"})
	member := testJWTCookie(t, cfg, []string{"user", "member"})
	valid := map[string]string{"name": "Dokploy", "websiteUrl": "https://dokploy.com"}

	t.Run("anonymous cannot add", func(t *testing.T) {
		rec := doSponsorUpload(t, engine, valid, testPNG, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("non-admin cannot add", func(t *testing.T) {
		rec := doSponsorUpload(t, engine, valid, testPNG, member)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("anonymous cannot delete", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/admin/sponsors/"+uuid.NewString(), nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	invalid := []struct {
		name     string
		fields   map[string]string
		logo     []byte
		wantCode int
	}{
		{"missing name", map[string]string{"name": "  "}, testPNG, http.StatusBadRequest},
		{"name too long", map[string]string{"name": string(bytes.Repeat([]byte("a"), 101))}, testPNG, http.StatusBadRequest},
		{"non-http website", map[string]string{"name": "Dokploy", "websiteUrl": "javascript:alert(1)"}, testPNG, http.StatusBadRequest},
		{"non-numeric sort order", map[string]string{"name": "Dokploy", "sortOrder": "first"}, testPNG, http.StatusBadRequest},
		{"missing logo", valid, nil, http.StatusBadRequest},
		{"logo is not an image", valid, []byte("just some text"), http.StatusBadRequest},
		// Just over the logo limit: fits in the request cap, caught by readSponsorLogo.
		{"logo too large", valid, append(testPNG, make([]byte, maxSponsorLogoBytes)...), http.StatusRequestEntityTooLarge},
		// Far over: refused by the request cap before the form is fully read.
		{"upload far over the request cap", valid, append(testPNG, make([]byte, 3*maxSponsorUploadBytes)...), http.StatusRequestEntityTooLarge},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSponsorUpload(t, engine, tc.fields, tc.logo, admin)
			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
		})
	}

	t.Run("non-multipart add is rejected", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "POST", "/api/v1/admin/sponsors",
			map[string]string{"name": "Dokploy"}, admin)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})

	t.Run("anonymous cannot reorder", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PUT", "/api/v1/admin/sponsors/order",
			map[string]any{"ids": []string{uuid.NewString()}}, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	reorderInvalid := []struct {
		name string
		body any
	}{
		{"reorder without ids", map[string]any{"ids": "nope"}},
		{"reorder with malformed id", map[string]any{"ids": []string{"not-a-uuid"}}},
		{"reorder with duplicate id", func() any {
			id := uuid.NewString()
			return map[string]any{"ids": []string{id, id}}
		}()},
	}
	for _, tc := range reorderInvalid {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONRequest(t, engine, "PUT", "/api/v1/admin/sponsors/order", tc.body, admin)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	t.Run("malformed id on logo", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/sponsors/not-a-uuid/logo", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// TestSponsorHandlerLifecycle adds, lists, reorders, serves and removes
// sponsors against a real Postgres with an in-memory blob store. It follows
// the same "skip if no .env" convention as the other database-backed handler
// tests, but runs in a throwaway schema: Reorder rewrites every sponsor's
// position, which must never touch sponsors in a shared dev database.
func TestSponsorHandlerLifecycle(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Postgres)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	jwtKey := generateTestJWTKey(t)
	cfg.JwtSigningKey, cfg.JwtValidatingKey = jwtKey, jwtKey

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Skipf("skipping: could not connect to Postgres: %v", err)
	}
	schemaName := "sponsortest_" + uuid.NewString()[:8]
	require.NoError(t, base.Exec("CREATE SCHEMA "+schemaName).Error)
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schemaName + " CASCADE") })

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schemaName),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.BlobData{}, &models.Sponsor{}))

	store := newMemBlobStore()
	h := NewSponsorHandler(db, cfg)
	h.openBlobStore = func() (utils.BlobStore, error) { return store, nil }
	engine := newSponsorTestEngine(h)
	admin := testJWTCookie(t, cfg, []string{"user", "member", "admin"})

	create := func(t *testing.T, fields map[string]string, logo []byte) models.Sponsor {
		t.Helper()
		rec := doSponsorUpload(t, engine, fields, logo, admin)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var body struct{ Sponsor models.Sponsor }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.Sponsor
	}
	listNames := func(t *testing.T) []string {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/sponsors", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var sponsors []models.Sponsor
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sponsors))
		names := make([]string, len(sponsors))
		for i, s := range sponsors {
			names[i] = s.Name
		}
		return names
	}
	reorder := func(t *testing.T, ids ...uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		return doJSONRequest(t, engine, "PUT", "/api/v1/admin/sponsors/order",
			map[string]any{"ids": ids}, admin)
	}

	t.Run("empty list is an array, not null", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/sponsors", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, "[]", rec.Body.String())
	})

	alpha := create(t, map[string]string{"name": "Alpha", "websiteUrl": "https://alpha.example.com"}, testPNG)
	beta := create(t, map[string]string{"name": "Beta"}, testSVG)
	gamma := create(t, map[string]string{"name": "Gamma"}, testPNG)
	require.Equal(t, 3, store.len(), "each sponsor's logo should be stored")

	t.Run("new sponsors are added at the end", func(t *testing.T) {
		require.Equal(t, []int{0, 1, 2}, []int{alpha.SortOrder, beta.SortOrder, gamma.SortOrder})
		require.Equal(t, []string{"Alpha", "Beta", "Gamma"}, listNames(t))
	})

	t.Run("reorder saves the new order", func(t *testing.T) {
		rec := reorder(t, gamma.Id, alpha.Id, beta.Id)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, []string{"Gamma", "Alpha", "Beta"}, listNames(t))
	})

	t.Run("reorder from a stale list is a conflict and changes nothing", func(t *testing.T) {
		rec := reorder(t, beta.Id, alpha.Id) // missing gamma
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

		rec = reorder(t, beta.Id, alpha.Id, gamma.Id, uuid.New()) // unknown extra
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

		require.Equal(t, []string{"Gamma", "Alpha", "Beta"}, listNames(t))
	})

	t.Run("logo is served with its own content type", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/sponsors/"+beta.Id.String()+"/logo", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"))
		require.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox")
		require.Equal(t, testSVG, rec.Body.Bytes())
	})

	// breakStorage makes every storage call fail until the subtest ends.
	breakStorage := func(t *testing.T) {
		t.Helper()
		h.openBlobStore = func() (utils.BlobStore, error) { return failingBlobStore{}, nil }
		t.Cleanup(func() {
			h.openBlobStore = func() (utils.BlobStore, error) { return store, nil }
		})
	}

	t.Run("add fails cleanly when storage is down", func(t *testing.T) {
		breakStorage(t)
		rec := doSponsorUpload(t, engine, map[string]string{"name": "Delta"}, testPNG, admin)
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		require.Equal(t, []string{"Gamma", "Alpha", "Beta"}, listNames(t),
			"a sponsor whose logo couldn't be stored must not be saved")
	})

	t.Run("logo fails with 500, not an empty image, when storage is down", func(t *testing.T) {
		breakStorage(t)
		req := httptest.NewRequest("GET", "/api/v1/sponsors/"+beta.Id.String()+"/logo", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.NotEqual(t, "image/svg+xml", rec.Header().Get("Content-Type"))
	})

	t.Run("delete removes the sponsor and its logo", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/admin/sponsors/"+alpha.Id.String(), nil)
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		require.Equal(t, []string{"Gamma", "Beta"}, listNames(t))
		require.Equal(t, 2, store.len(), "the removed sponsor's logo should be gone from storage")

		req = httptest.NewRequest("GET", "/api/v1/sponsors/"+alpha.Id.String()+"/logo", nil)
		rec = httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}
