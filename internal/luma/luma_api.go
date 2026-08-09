package luma

import (
	"backend/internal/config"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const baseURL = "https://public-api.luma.com/v1"

type LumaAPI struct {
	APIKey           string
	MembershipTierID string
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

	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("luma API key is missing")
	}
	if strings.TrimSpace(tierID) == "" {
		return nil, fmt.Errorf("luma membership tier id is missing")
	}

	return &LumaAPI{APIKey: apiKey, MembershipTierID: tierID}, nil
}

func (api *LumaAPI) IsConfigured() bool {
	return api != nil &&
		strings.TrimSpace(api.APIKey) != "" &&
		strings.TrimSpace(api.MembershipTierID) != ""
}

type addMemberRequest struct {
	Email            string `json:"email"`
	MembershipTierID string `json:"membership_tier_id"`
}

type addMemberResponse struct {
	MembershipID string `json:"membership_id"`
	Status       string `json:"status"`
}

// AddMember registers an email with the configured Luma membership tier.
//
// Only email is sent: Luma's registration_answers field requires question
// IDs configured per-tier in the Luma dashboard, which aren't set up here.
// The rest of the submitted form data (name, gender, university, programme,
// graduation year, interests) is stored in our own NewsletterSubscription
// table instead.
func (api *LumaAPI) AddMember(email string) error {
	if !api.IsConfigured() {
		return fmt.Errorf("luma is not configured")
	}

	body, err := json.Marshal(addMemberRequest{
		Email:            email,
		MembershipTierID: api.MembershipTierID,
	})
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
