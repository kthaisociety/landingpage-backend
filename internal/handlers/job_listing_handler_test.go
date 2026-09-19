package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestJobListingSummaryQueryOrdersNewestFirst(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=localhost user=test dbname=test sslmode=disable"), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	query := jobListingSummaryQuery(db).Scan(&[]SmallJobListing{})
	sql := strings.Join(strings.Fields(query.Statement.SQL.String()), " ")
	want := "ORDER BY job_listings.created_at DESC, job_listings.id DESC"
	if !strings.Contains(sql, want) {
		t.Fatalf("query ordering = %q, want it to contain %q", sql, want)
	}
}

// TestTrackApplyClick needs a real Postgres connection, so it follows the same
// "skip if no .env" convention as the other handler tests in this package.
func TestTrackApplyClick(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&models.JobListing{}))

	job := models.JobListing{
		Id:        uuid.New(),
		Name:      "Test Job",
		Location:  "Remote",
		JobType:   "Full-time",
		CompanyId: uuid.New(),
		StartDate: time.Now(),
		EndDate:   time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.Create(&job).Error)
	t.Cleanup(func() {
		db.Unscoped().Delete(&job)
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := NewJobListingHandler(db, cfg)
	// Registered directly, bypassing ClickRateLimit (which requires a live Redis
	// connection): this test exercises the handler's own logic in isolation.
	// ClickRateLimit itself is covered by TestClickRateLimit below.
	engine.POST("/joblistings/click", handler.TrackApplyClick)

	post := func(t *testing.T, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	t.Run("increments the counter atomically", func(t *testing.T) {
		rec := post(t, "/joblistings/click?id="+job.Id.String())
		require.Equal(t, http.StatusNoContent, rec.Code)

		var got models.JobListing
		require.NoError(t, db.First(&got, "id = ?", job.Id).Error)
		require.EqualValues(t, 1, got.ApplyClickCount)

		rec = post(t, "/joblistings/click?id="+job.Id.String())
		require.Equal(t, http.StatusNoContent, rec.Code)

		require.NoError(t, db.First(&got, "id = ?", job.Id).Error)
		require.EqualValues(t, 2, got.ApplyClickCount)
	})

	t.Run("missing id", func(t *testing.T) {
		rec := post(t, "/joblistings/click")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed id", func(t *testing.T) {
		rec := post(t, "/joblistings/click?id=not-a-uuid")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown listing", func(t *testing.T) {
		rec := post(t, "/joblistings/click?id="+uuid.New().String())
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestClickRateLimit needs a real Redis connection, so it follows the same
// "skip if no .env" convention as the Postgres-backed tests in this package.
func TestClickRateLimit(t *testing.T) {
	envFile := "../../.env"
	if _, err := os.Stat(envFile); err != nil {
		t.Skip("skipping: no .env file present (this test needs local Redis)")
	}
	require.NoError(t, godotenv.Load(envFile))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)

	limiter, err := middleware.NewRedisRateLimiter(cfg, 3, time.Minute)
	if err != nil {
		t.Skipf("skipping: could not connect to Redis: %v", err)
	}

	// Unique key per run so this test doesn't interfere with itself or other runs
	// sharing the same Redis instance; it self-expires via the limiter's own window.
	key := fmt.Sprintf("test_click_rate_limit:%s", uuid.New())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(ctx, key)
		require.NoError(t, err)
		require.Truef(t, allowed, "request %d should be allowed within the limit", i+1)
	}

	allowed, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	require.False(t, allowed, "request beyond the limit should be rejected")
}
