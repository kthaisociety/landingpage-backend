package utils

import (
	"backend/internal/config"
	"bytes"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

// A put, get and delete against the bucket set by the S3_* values in .env.
// Skipped without a .env or S3_ENDPOINT, as in CI.
func TestBlobStoreRoundTrip(t *testing.T) {
	file := "../../.env"
	if _, err := os.Stat(file); err != nil {
		t.Skip("no .env; this test needs an S3 endpoint")
	}
	if err := godotenv.Load(file); err != nil {
		t.Fatalf("Could not load env file: %s", err)
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Error Loading Config: %s", err)
	}
	if cfg.S3.Endpoint == "" {
		t.Skip("S3_ENDPOINT is unset")
	}

	store, err := InitS3SDK(cfg)
	if err != nil {
		t.Fatalf("Failed to init store: %s", err)
	}

	key := "blob-test-" + t.Name()
	want := []byte("My test Object Here")
	if err := store.PutObject(key, want); err != nil {
		t.Fatalf("Error putting object: %s", err)
	}
	t.Cleanup(func() { _ = store.DeleteObject(key) })

	got, err := store.GetObject(key)
	if err != nil {
		t.Fatalf("Error getting object: %s", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("GetObject = %q, want %q", got, want)
	}
}
