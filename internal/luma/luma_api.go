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
	APIKey string
	// MembersTierID is the Luma tier new @kthais.com members are added to
	// — see AddMemberToTier, called both by onboarding-service's
	// provisioning step and this backend's own /admin/luma/add-member.
	MembersTierID string
}

// LumaAPIError is the error response from the Luma API.
type LumaAPIError struct {
	Status int
	Body   string
}

func (err *LumaAPIError) Error() string {
	return fmt.Sprintf("luma API error: status %d: %s", err.Status, err.Body)
}

// InitLumaApi creates a LumaAPI client. Mirrors mailchimp.InitMailchimpApi's
// error-if-missing contract so main.go can log-and-continue the same way it
// does for Mailchimp/SES. MembersTierID isn't required here — a missing
// tier ID only breaks AddMemberToTier's one call, checked there, rather
// than disabling the client (and its shared API key) entirely.
func InitLumaApi(cfg *config.Config) (*LumaAPI, error) {
	apiKey := cfg.Luma.APIKey
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("luma API key is missing")
	}

	return &LumaAPI{
		APIKey:        apiKey,
		MembersTierID: cfg.Luma.MembersTierID,
	}, nil
}

type addMemberRequest struct {
	Email            string `json:"email"`
	MembershipTierID string `json:"membership_tier_id"`
}

type addMemberResponse struct {
	MembershipID string `json:"membership_id"`
	Status       string `json:"status"`
}

// AddMemberToTier adds email to tierID. Only requires an API key — safe to
// call on a nil receiver (returns an error rather than panicking), since
// main.go leaves the client nil when LUMA_API_KEY isn't set.
func (api *LumaAPI) AddMemberToTier(email, tierID string) error {
	if api == nil || strings.TrimSpace(api.APIKey) == "" {
		return fmt.Errorf("luma is not configured")
	}
	if strings.TrimSpace(tierID) == "" {
		return fmt.Errorf("luma tier id is missing")
	}

	request := addMemberRequest{Email: email, MembershipTierID: tierID}
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
