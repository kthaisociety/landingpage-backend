package config

import "testing"

func TestLoadConfigUsesMailchimpAudienceIDFallback(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("MAILCHIMP_API_KEY", "key-us20")
	t.Setenv("MAILCHIMP_AUDIENCE_ID", "audience-id")
	t.Setenv("MAILCHIMP_LIST_ID", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Mailchimp.ListID != "audience-id" {
		t.Fatalf("Mailchimp.ListID = %q, want %q", cfg.Mailchimp.ListID, "audience-id")
	}
	if cfg.Mailchimp.User != "kthais" {
		t.Fatalf("Mailchimp.User = %q, want default %q", cfg.Mailchimp.User, "kthais")
	}
}
