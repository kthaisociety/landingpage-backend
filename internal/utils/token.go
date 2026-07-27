package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateToken produces a random, single-use access token. raw is the
// URL-safe value that goes into a link (e.g. an emailed one-time link);
// hash is what should be persisted instead of raw, so a database read alone
// never yields a live, usable token. Callers hash whatever token they
// receive back with HashToken and compare against the stored hash.
func GenerateToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("failed to generate token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken deterministically hashes a raw token for storage/lookup.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
