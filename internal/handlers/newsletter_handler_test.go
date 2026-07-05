package handlers

import (
	"strings"
	"testing"
)

func validNewsletterSubscribeBody() newsletterSubscribeBody {
	return newsletterSubscribeBody{
		FirstName:            "Ada",
		LastName:             "Lovelace",
		Email:                "ada@example.com",
		Gender:               "Female",
		University:           "KTH Royal Institute of Technology",
		Programme:            "Computer Science",
		GraduationYear:       2027,
		Interests:            []string{"Startups & Venture Creation", "Finance & Investment"},
		DataRetentionConsent: true,
	}
}

func TestValidateNewsletterSubscribeBody(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*newsletterSubscribeBody)
		wantErr string
	}{
		{
			name: "valid input",
		},
		{
			name: "missing first name",
			mutate: func(body *newsletterSubscribeBody) {
				body.FirstName = ""
			},
			wantErr: "first name",
		},
		{
			name: "missing last name",
			mutate: func(body *newsletterSubscribeBody) {
				body.LastName = ""
			},
			wantErr: "last name",
		},
		{
			name: "invalid email",
			mutate: func(body *newsletterSubscribeBody) {
				body.Email = "not-an-email"
			},
			wantErr: "email",
		},
		{
			name: "invalid gender",
			mutate: func(body *newsletterSubscribeBody) {
				body.Gender = ""
			},
			wantErr: "gender",
		},
		{
			name: "missing university",
			mutate: func(body *newsletterSubscribeBody) {
				body.University = ""
			},
			wantErr: "university",
		},
		{
			name: "missing programme",
			mutate: func(body *newsletterSubscribeBody) {
				body.Programme = ""
			},
			wantErr: "programme",
		},
		{
			name: "invalid graduation year too early",
			mutate: func(body *newsletterSubscribeBody) {
				body.GraduationYear = 2025
			},
			wantErr: "graduation year",
		},
		{
			name: "missing interests",
			mutate: func(body *newsletterSubscribeBody) {
				body.Interests = nil
			},
			wantErr: "at least one area of interest",
		},
		{
			name: "invalid interest",
			mutate: func(body *newsletterSubscribeBody) {
				body.Interests = []string{"Crypto & Web3"}
			},
			wantErr: "invalid interest",
		},
		{
			name: "duplicate interest",
			mutate: func(body *newsletterSubscribeBody) {
				body.Interests = []string{"Finance & Investment", "Finance & Investment"}
			},
			wantErr: "once",
		},
		{
			name: "missing data retention consent",
			mutate: func(body *newsletterSubscribeBody) {
				body.DataRetentionConsent = false
			},
			wantErr: "consent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := validNewsletterSubscribeBody()
			if tt.mutate != nil {
				tt.mutate(&body)
			}

			err := validateNewsletterSubscribeBody(body)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateNewsletterSubscribeBody() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateNewsletterSubscribeBody() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
