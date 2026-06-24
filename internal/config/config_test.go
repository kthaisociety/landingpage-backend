package config

import (
	"os"
	"testing"
)

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

func TestLoadConfigUsesSESDefaults(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	unsetEnvForTest(t, "SES_REGION")
	unsetEnvForTest(t, "SES_SENDER")
	unsetEnvForTest(t, "SES_REPLY_TO")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.SES.Region != defaultSESRegion {
		t.Fatalf("SES.Region = %q, want %q", cfg.SES.Region, defaultSESRegion)
	}
	if cfg.SES.Sender != defaultSESSender {
		t.Fatalf("SES.Sender = %q, want %q", cfg.SES.Sender, defaultSESSender)
	}
	if cfg.SES.ReplyTo != defaultSESSender {
		t.Fatalf("SES.ReplyTo = %q, want %q", cfg.SES.ReplyTo, defaultSESSender)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	previous, hadPrevious := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("failed to unset %s: %v", key, err)
	}

	t.Cleanup(func() {
		if hadPrevious {
			if err := os.Setenv(key, previous); err != nil {
				t.Fatalf("failed to restore %s: %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("failed to clean up %s: %v", key, err)
		}
	})
}
