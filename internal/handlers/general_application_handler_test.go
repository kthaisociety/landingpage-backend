package handlers

import (
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
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
		Interests:            []string{"Startups & Venture Creation", "Finance & Investment"},
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
				input.Interests = []string{"Finance & Investment", "Finance & Investment"}
			},
			wantErr: "once",
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
