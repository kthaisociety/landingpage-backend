package handlers

import (
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func validGeneralApplicationInput() generalApplicationInput {
	return generalApplicationInput{
		FirstName:            "Ada",
		LastName:             "Lovelace",
		Email:                "ada@example.com",
		Gender:               "Female",
		University:           "KTH Royal Institute of Technology",
		Programme:            "Computer Science",
		GraduationYear:       2027,
		LinkedinURL:          "https://www.linkedin.com/in/adalovelace",
		AdditionalLinks:      []string{"https://github.com/ada"},
		Teams:                []string{"Development", "Research", "Business", "Growth", "IT"},
		Interests:            []string{"Startups & Venture Creation", "Machine Learning"},
		Availability:         "6-8 hours",
		Contribution:         "I can contribute by building products, writing clearly, and helping organize technical work.",
		DataRetentionConsent: true,
	}
}

func TestValidateGeneralApplicationInput(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*generalApplicationInput)
		wantErr string
	}{
		{
			name: "valid input",
		},
		{
			name: "missing first name",
			mutate: func(input *generalApplicationInput) {
				input.FirstName = ""
			},
			wantErr: "first name",
		},
		{
			name: "invalid email",
			mutate: func(input *generalApplicationInput) {
				input.Email = "not-an-email"
			},
			wantErr: "email",
		},
		{
			name: "invalid gender",
			mutate: func(input *generalApplicationInput) {
				input.Gender = ""
			},
			wantErr: "gender",
		},
		{
			name: "missing programme",
			mutate: func(input *generalApplicationInput) {
				input.Programme = ""
			},
			wantErr: "programme",
		},
		{
			name: "invalid graduation year too late",
			mutate: func(input *generalApplicationInput) {
				input.GraduationYear = 2200
			},
			wantErr: "graduation year",
		},
		{
			name: "invalid graduation year too early",
			mutate: func(input *generalApplicationInput) {
				input.GraduationYear = 2025
			},
			wantErr: "graduation year",
		},
		{
			name: "domain-only LinkedIn URL",
			mutate: func(input *generalApplicationInput) {
				input.LinkedinURL = "linkedin.com/in/adalovelace"
			},
		},
		{
			name: "invalid LinkedIn URL",
			mutate: func(input *generalApplicationInput) {
				input.LinkedinURL = "https://example.com/ada"
			},
			wantErr: "LinkedIn",
		},
		{
			name: "too many additional links",
			mutate: func(input *generalApplicationInput) {
				input.AdditionalLinks = []string{
					"https://example.com/1",
					"https://example.com/2",
					"https://example.com/3",
					"https://example.com/4",
					"https://example.com/5",
					"https://example.com/6",
				}
			},
			wantErr: "5",
		},
		{
			name: "invalid additional link",
			mutate: func(input *generalApplicationInput) {
				input.AdditionalLinks = []string{"not-a-url"}
			},
			wantErr: "valid URLs",
		},
		{
			name: "domain-only additional link",
			mutate: func(input *generalApplicationInput) {
				input.AdditionalLinks = []string{"ludvigbergstrom.com", "github.com/ada"}
			},
		},
		{
			name: "missing ranked teams",
			mutate: func(input *generalApplicationInput) {
				input.Teams = nil
			},
			wantErr: "at least one team",
		},
		{
			name: "one ranked team",
			mutate: func(input *generalApplicationInput) {
				input.Teams = []string{"Development"}
			},
		},
		{
			name: "two ranked teams",
			mutate: func(input *generalApplicationInput) {
				input.Teams = []string{"Development", "Research"}
			},
		},
		{
			name: "too many teams",
			mutate: func(input *generalApplicationInput) {
				input.Teams = []string{"Development", "Research", "Business", "Growth", "IT", "Development"}
			},
			wantErr: "at most five teams",
		},
		{
			name: "invalid team",
			mutate: func(input *generalApplicationInput) {
				input.Teams = []string{"Development", "Research", "Business", "Growth", "Board"}
			},
			wantErr: "team",
		},
		{
			name: "duplicate team",
			mutate: func(input *generalApplicationInput) {
				input.Teams = []string{"Development", "Research", "Business", "Growth", "Growth"}
			},
			wantErr: "once",
		},
		{
			name: "missing interests",
			mutate: func(input *generalApplicationInput) {
				input.Interests = nil
			},
			wantErr: "at least one area of interest",
		},
		{
			name: "invalid interest",
			mutate: func(input *generalApplicationInput) {
				input.Interests = []string{"Crypto & Web3"}
			},
			wantErr: "invalid interest",
		},
		{
			name: "duplicate interest",
			mutate: func(input *generalApplicationInput) {
				input.Interests = []string{"Machine Learning", "Machine Learning"}
			},
			wantErr: "once",
		},
		{
			name: "too many interests",
			mutate: func(input *generalApplicationInput) {
				input.Interests = []string{
					"Machine Learning",
					"Robotics & Autonomous Systems",
					"Computer Vision & Graphics",
					"Natural Language Processing",
					"Data Science & Big Data Infrastructure",
					"Embedded Systems & Edge AI",
					"Cybersecurity & AI Safety Engineering",
					"AI Research & Theoretical ML",
					"Bioinformatics & Computational Biology",
					"Quantitative Finance & Investment",
					"Venture Capital & Private Equity",
					"Startups & Venture Creation",
					"Machine Learning",
				}
			},
			wantErr: "at most 12 areas of interest",
		},
		{
			name: "invalid availability",
			mutate: func(input *generalApplicationInput) {
				input.Availability = "1-3 hours"
			},
			wantErr: "availability",
		},
		{
			name: "short contribution",
			mutate: func(input *generalApplicationInput) {
				input.Contribution = "too short"
			},
			wantErr: "contribution",
		},
		{
			name: "missing data retention consent",
			mutate: func(input *generalApplicationInput) {
				input.DataRetentionConsent = false
			},
			wantErr: "consent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validGeneralApplicationInput()
			if tt.mutate != nil {
				tt.mutate(&input)
			}

			err := validateGeneralApplicationInput(input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateGeneralApplicationInput() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateGeneralApplicationInput() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateResumeFile(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		size        int64
		contentType string
		wantErr     string
	}{
		{
			name:        "valid pdf",
			filename:    "resume.pdf",
			size:        1024,
			contentType: "application/pdf",
		},
		{
			name:        "valid docx",
			filename:    "resume.docx",
			size:        1024,
			contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		{
			name:        "invalid extension",
			filename:    "resume.png",
			size:        1024,
			contentType: "image/png",
			wantErr:     "PDF",
		},
		{
			name:        "invalid content type",
			filename:    "resume.pdf",
			size:        1024,
			contentType: "text/plain",
			wantErr:     "PDF",
		},
		{
			name:        "oversized",
			filename:    "resume.pdf",
			size:        generalApplicationMaxResume + 1,
			contentType: "application/pdf",
			wantErr:     "10 MiB",
		},
		{
			name:        "empty file",
			filename:    "resume.pdf",
			size:        0,
			contentType: "application/pdf",
			wantErr:     "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := textproto.MIMEHeader{}
			if tt.contentType != "" {
				header.Set("Content-Type", tt.contentType)
			}
			_, err := validateResumeFile(&multipart.FileHeader{
				Filename: tt.filename,
				Size:     tt.size,
				Header:   header,
			})

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateResumeFile() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateResumeFile() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	got := normalizeEmail("  ADA@Example.COM ")
	if got != "ada@example.com" {
		t.Fatalf("normalizeEmail() = %q, want %q", got, "ada@example.com")
	}
}

// TestAdminUpdateSettingsPreservesRecruitmentOpensAtWhenOmitted covers a
// real bug: since Go's JSON decoding can't tell "the client omitted this
// field" apart from "the client explicitly sent null" once bound into a
// *time.Time, a caller that doesn't know about recruitment_opens_at (an
// older client, or one only updating the deadline) would otherwise silently
// wipe a previously configured future opening date and reopen recruitment
// immediately. AdminUpdateSettings must only ever clear it on an explicit
// null, never on omission.
//
// Standalone (not part of TestApplicationAndInterviewLifecycle) so it isn't
// affected by that test's own unrelated failure — same "skip if no .env"
// convention, since this also needs a real Postgres connection.
func TestAdminUpdateSettingsPreservesRecruitmentOpensAtWhenOmitted(t *testing.T) {
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
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.Profile{}, &models.GeneralApplicationSettings{}))
	require.NoError(t, db.Unscoped().Where("1 = 1").Delete(&models.GeneralApplicationSettings{}).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewGeneralApplicationHandler(db, cfg, nil).Register(api)

	admin := mustCreateAdmin(t, db, cfg, "recruitment-settings-presence@example.com")
	t.Cleanup(func() {
		db.Unscoped().Where("1 = 1").Delete(&models.GeneralApplicationSettings{})
		db.Where("email = ?", admin.email).Unscoped().Delete(&models.Profile{})
		db.Where("email = ?", admin.email).Unscoped().Delete(&models.User{})
	})

	deadline := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	opensAt := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)

	rec := doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings", map[string]any{
		"recruitment_opens_at": opensAt,
		"submission_deadline":  deadline,
	}, admin.cookie)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		RecruitmentOpensAt *time.Time `json:"recruitment_opens_at"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.RecruitmentOpensAt)
	require.True(t, opensAt.Equal(*body.RecruitmentOpensAt))

	t.Run("a request omitting the field entirely leaves it untouched", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings", map[string]any{
			"submission_deadline": deadline,
		}, admin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			RecruitmentOpensAt *time.Time `json:"recruitment_opens_at"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.NotNil(t, body.RecruitmentOpensAt, "omitting the field must not clear a previously saved opening date")
		require.True(t, opensAt.Equal(*body.RecruitmentOpensAt))
	})

	t.Run("an explicit null still clears it", func(t *testing.T) {
		rec := doJSONRequest(t, engine, "PUT", "/api/v1/applications/admin/settings", map[string]any{
			"recruitment_opens_at": nil,
			"submission_deadline":  deadline,
		}, admin.cookie)
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			RecruitmentOpensAt *time.Time `json:"recruitment_opens_at"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Nil(t, body.RecruitmentOpensAt, "an explicit null must still clear it")
	})
}
