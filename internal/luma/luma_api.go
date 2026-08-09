package luma

import (
	"backend/internal/config"
	"backend/internal/models"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const baseURL = "https://public-api.luma.com/v1"

type LumaAPI struct {
	APIKey              string
	MembershipTierID    string
	FirstNameQuestionID string
	LastNameQuestionID  string
}

// LumaAPIError is the error response from the Luma API.
type LumaAPIError struct {
	Status int
	Body   string
}

func (err *LumaAPIError) Error() string {
	return fmt.Sprintf("luma API error: status %d: %s", err.Status, err.Body)
}

// InitLumaApi creates a LumaAPI client for registering newsletter subscribers
// as members via Luma. Mirrors mailchimp.InitMailchimpApi's error-if-missing
// contract so main.go can log-and-continue the same way it does for Mailchimp/SES.
func InitLumaApi(cfg *config.Config) (*LumaAPI, error) {
	apiKey := cfg.Luma.APIKey
	tierID := cfg.Luma.MembershipTierID
	firstNameQuestionID := cfg.Luma.FirstNameQuestionID
	lastNameQuestionID := cfg.Luma.LastNameQuestionID

	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("luma API key is missing")
	}
	if strings.TrimSpace(tierID) == "" {
		return nil, fmt.Errorf("luma membership tier id is missing")
	}
	if strings.TrimSpace(firstNameQuestionID) == "" {
		return nil, fmt.Errorf("luma first name question id is missing")
	}
	if strings.TrimSpace(lastNameQuestionID) == "" {
		return nil, fmt.Errorf("luma last name question id is missing")
	}

	return &LumaAPI{
		APIKey:              apiKey,
		MembershipTierID:    tierID,
		FirstNameQuestionID: firstNameQuestionID,
		LastNameQuestionID:  lastNameQuestionID,
	}, nil
}

func (api *LumaAPI) IsConfigured() bool {
	return api != nil &&
		strings.TrimSpace(api.APIKey) != "" &&
		strings.TrimSpace(api.MembershipTierID) != "" &&
		strings.TrimSpace(api.FirstNameQuestionID) != "" &&
		strings.TrimSpace(api.LastNameQuestionID) != ""
}

type registrationAnswer struct {
	QuestionID   string `json:"question_id"`
	QuestionType string `json:"question_type"`
	Value        string `json:"value"`
}

type addMemberRequest struct {
	Email               string               `json:"email"`
	MembershipTierID    string               `json:"membership_tier_id"`
	RegistrationAnswers []registrationAnswer `json:"registration_answers,omitempty"`
}

type addMemberResponse struct {
	MembershipID string `json:"membership_id"`
	Status       string `json:"status"`
}

// AddMember registers a newsletter subscriber with the configured Luma
// membership tier, including their first/last name as answers to the two
// configured registration questions.
//
// Luma's membership API has no first-class "name" field — a member created
// here shows as "Anonymous" in the Luma dashboard until someone edits it by
// hand or the person signs into their own Luma account with a name set;
// there's no API to set it directly (confirmed: Luma's own dashboard uses an
// internal, session-cookie-only admin endpoint for that, which rejects API
// keys). Sending the name as registration answers instead makes it visible
// on the member record — e.g. for a future script to copy from there into
// the actual name fields via that admin endpoint. The full structured data
// (name, gender, university, programme, graduation year, interests) always
// lives in our own NewsletterSubscription table regardless.
func (api *LumaAPI) AddMember(sub *models.NewsletterSubscription) error {
	if !api.IsConfigured() {
		return fmt.Errorf("luma is not configured")
	}

	request := addMemberRequest{
		Email:            sub.Email,
		MembershipTierID: api.MembershipTierID,
		RegistrationAnswers: []registrationAnswer{
			{
				QuestionID:   api.FirstNameQuestionID,
				QuestionType: "text",
				Value:        sub.FirstName,
			},
			{
				QuestionID:   api.LastNameQuestionID,
				QuestionType: "text",
				Value:        sub.LastName,
			},
		},
	}

	body, err := json.Marshal(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/memberships/members/add", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-luma-api-key", api.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &LumaAPIError{Status: resp.StatusCode, Body: string(data)}
	}

	if len(data) == 0 {
		return nil
	}

	var result addMemberResponse
	return json.Unmarshal(data, &result)
}
