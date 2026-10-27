package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"slices"
	"strings"
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

func TestLoadConfigOAuthRateLimitRequests(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")

	cases := []struct {
		name string
		env  *string
		want int
	}{
		{"unset", nil, defaultOAuthRateLimitRequests},
		{"valid", ptr("12"), 12},
		{"not a number", ptr("lots"), defaultOAuthRateLimitRequests},
		{"zero", ptr("0"), defaultOAuthRateLimitRequests},
		{"negative", ptr("-3"), defaultOAuthRateLimitRequests},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == nil {
				unsetEnvForTest(t, "OAUTH_RATE_LIMIT_REQUESTS")
			} else {
				t.Setenv("OAUTH_RATE_LIMIT_REQUESTS", *tc.env)
			}

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.OAuth.RateLimitRequests != tc.want {
				t.Fatalf("OAuth.RateLimitRequests = %d, want %d", cfg.OAuth.RateLimitRequests, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestParseDevRoleOverrides(t *testing.T) {
	got, err := parseDevRoleOverrides(" Sam@KTHAIS.com = user, member,Admin ; b@x.com=user; ")
	if err != nil {
		t.Fatalf("parseDevRoleOverrides() error = %v", err)
	}
	if want := []string{"user", "member", "admin"}; !slices.Equal(got["sam@kthais.com"], want) {
		t.Fatalf("sam roles = %v, want %v", got["sam@kthais.com"], want)
	}
	if want := []string{"user"}; !slices.Equal(got["b@x.com"], want) {
		t.Fatalf("b roles = %v, want %v", got["b@x.com"], want)
	}

	for _, bad := range []string{"a@x.com=superuser", "a@x.com", "a@x.com=", "=admin", ";;;", " ; "} {
		if _, err := parseDevRoleOverrides(bad); err == nil {
			t.Errorf("parseDevRoleOverrides(%q) = nil error, want error", bad)
		}
	}
}

func TestLoadConfigRejectsDevRoleOverridesOutsideDevelopmentMode(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("DEVELOPMENT_MODE", "false")

	for _, raw := range []string{"a@x.com=admin", ";;;"} {
		t.Setenv("DEV_ROLE_OVERRIDES", raw)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("LoadConfig() with DEV_ROLE_OVERRIDES=%q = nil error, want error outside development mode", raw)
		}
	}
}

func TestLoadConfigDevelopmentMode(t *testing.T) {
	tests := []struct {
		name  string
		value *string
		want  bool
	}{
		{"unset", nil, false},
		{"empty", ptr(""), false},
		{"false", ptr("false"), false},
		{"non-boolean", ptr("yes"), false},
		{"GIN_MODE-style value", ptr("debug"), false},
		{"true", ptr("true"), true},
		{"mixed-case true", ptr("TRUE"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOOGLE_CLIENT_ID", "client-id")
			t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
			unsetEnvForTest(t, "DEV_ROLE_OVERRIDES")
			if tt.value == nil {
				unsetEnvForTest(t, "DEVELOPMENT_MODE")
			} else {
				t.Setenv("DEVELOPMENT_MODE", *tt.value)
			}

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.DevelopmentMode != tt.want {
				t.Fatalf("DevelopmentMode = %v, want %v", cfg.DevelopmentMode, tt.want)
			}
		})
	}
}

// testRSAKeyPEMs returns a fresh PEM-encoded RSA private/public key pair.
func testRSAKeyPEMs(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
}

func TestLoadConfigJWTKeyNames(t *testing.T) {
	priv, pub := testRSAKeyPEMs(t)

	setup := func(t *testing.T) {
		t.Setenv("GOOGLE_CLIENT_ID", "client-id")
		t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
		for _, key := range []string{"JWT_PRIVATE_KEY", "JWT_PUBLIC_KEY", "JWTSigningKey", "JWTValidatingKey"} {
			unsetEnvForTest(t, key)
		}
	}

	t.Run("new names", func(t *testing.T) {
		setup(t)
		t.Setenv("JWT_PRIVATE_KEY", priv)
		t.Setenv("JWT_PUBLIC_KEY", pub)
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if err := ValidateJWTKeys(cfg); err != nil {
			t.Fatalf("ValidateJWTKeys() error = %v", err)
		}
	})

	t.Run("one-line keys with literal \\n", func(t *testing.T) {
		setup(t)
		t.Setenv("JWT_PRIVATE_KEY", strings.ReplaceAll(priv, "\n", `\n`))
		t.Setenv("JWT_PUBLIC_KEY", strings.ReplaceAll(pub, "\n", `\n`))
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if err := ValidateJWTKeys(cfg); err != nil {
			t.Fatalf("ValidateJWTKeys() error = %v", err)
		}
	})

	t.Run("old names are no longer read", func(t *testing.T) {
		setup(t)
		t.Setenv("JWTSigningKey", priv)
		t.Setenv("JWTValidatingKey", pub)
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.JwtSigningKey != "" || cfg.JwtValidatingKey != "" {
			t.Fatal("LoadConfig() read the old key names")
		}
		if err := ValidateJWTKeys(cfg); err == nil {
			t.Fatal("ValidateJWTKeys() accepted a config with only the old key names set")
		}
	})
}

func TestValidateJWTKeys(t *testing.T) {
	priv, pub := testRSAKeyPEMs(t)
	_, otherPub := testRSAKeyPEMs(t)

	tests := []struct {
		name        string
		signing     string
		validating  string
		wantErrText string
	}{
		{"valid pair", priv, pub, ""},
		{"missing private key", "", pub, "JWT_PRIVATE_KEY is not set"},
		{"missing public key", priv, "", "JWT_PUBLIC_KEY is not set"},
		{"old placeholder default", "test123456", pub, "not a PEM-encoded RSA private key"},
		{"public key in the private slot", pub, pub, "not a PEM-encoded RSA private key"},
		{"private key in the public slot", priv, priv, "not a PEM-encoded RSA public key"},
		{"mismatched pair", priv, otherPub, "does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJWTKeys(&Config{JwtSigningKey: tt.signing, JwtValidatingKey: tt.validating})
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("ValidateJWTKeys() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("ValidateJWTKeys() error = %v, want it to contain %q", err, tt.wantErrText)
			}
		})
	}
}

func TestDSN(t *testing.T) {
	parts := func() *Config {
		c := &Config{}
		c.Database.Host, c.Database.Port, c.Database.User = "db", "5432", "u"
		c.Database.Password, c.Database.DBName, c.Database.SSLMode = "p", "kthais", "disable"
		return c
	}

	tests := []struct {
		name    string
		url     string
		sslmode string
		want    string
		wantErr bool
	}{
		{name: "DB_* parts without DATABASE_URL", want: "host=db user=u password=p dbname=kthais port=5432 sslmode=disable"},
		{name: "pipeline URL gets DB_SSLMODE", url: "postgresql://landingpage_backend:secret@pg-abc:5432/landingpage_backend", sslmode: "disable",
			want: "postgresql://landingpage_backend:secret@pg-abc:5432/landingpage_backend?sslmode=disable"},
		{name: "URL's own sslmode wins", url: "postgres://u:p@h:5432/d?sslmode=require", sslmode: "disable",
			want: "postgres://u:p@h:5432/d?sslmode=require"},
		{name: "no DB_SSLMODE leaves the URL alone", url: "postgres://u:p@h:5432/d", sslmode: "",
			want: "postgres://u:p@h:5432/d"},
		{name: "not a postgres URL", url: "host=h user=u", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := parts()
			if tt.url != "" {
				c.Database.URL, c.Database.SSLMode = tt.url, tt.sslmode
			}
			got, err := c.DSN()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DSN() = %q, want an error", got)
				}
				if strings.Contains(err.Error(), tt.url) {
					t.Fatalf("DSN() error %q echoes DATABASE_URL", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DSN() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestLoadConfigJWTCookieName(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")

	unsetEnvForTest(t, "JWT_COOKIE_NAME")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := cfg.JWTCookieName(); got != "jwt" {
		t.Fatalf("JWTCookieName() = %q by default, want jwt", got)
	}

	t.Setenv("JWT_COOKIE_NAME", "jwt_staging")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := cfg.JWTCookieName(); got != "jwt_staging" {
		t.Fatalf("JWTCookieName() = %q, want jwt_staging", got)
	}

	if got := (&Config{}).JWTCookieName(); got != "jwt" {
		t.Fatalf("JWTCookieName() on an empty Config = %q, want jwt", got)
	}
}
