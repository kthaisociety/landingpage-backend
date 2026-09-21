package luma

import (
	"backend/internal/config"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://public-api.luma.com/v1"

// lumaStatusNoMembership is Luma's own non-standard HTTP status for
// "update-status was called for a user with no existing membership" — see
// RemoveMember, the only caller that can hit this (there's no membership
// to decline for someone never added to any tier).
const lumaStatusNoMembership = 470

// defaultHTTPClient is shared across calls (for connection reuse) unless a
// LumaAPI overrides it. Bounded timeout so a hung Luma connection can't
// block a caller (e.g. AddToLuma, called synchronously from an HTTP
// handler) forever — net/http's zero-value client has no timeout at all.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

type LumaAPI struct {
	APIKey string
	// MembersTierID is the Luma tier new @kthais.com members are added to
	// — see AddMemberToTier, called both by onboarding-service's
	// provisioning step and this backend's own /admin/luma/add-member.
	MembersTierID string
	// BaseURL and HTTPClient override the defaults above — only ever set
	// in tests, to point requests at an httptest.Server instead of the
	// real Luma API.
	BaseURL    string
	HTTPClient *http.Client
}

func (api *LumaAPI) baseURL() string {
	if api.BaseURL != "" {
		return api.BaseURL
	}
	return defaultBaseURL
}

func (api *LumaAPI) httpClient() *http.Client {
	if api.HTTPClient != nil {
		return api.HTTPClient
	}
	return defaultHTTPClient
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

// updateMemberStatusRequest is RemoveMember's request body. user_id accepts
// either a Luma user id ('usr-xxx') or, as used here, a plain email.
type updateMemberStatusRequest struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

// post marshals body, POSTs it to api.baseURL()+path with the standard
// headers, and treats a non-2xx response as an error — the shared shape
// behind AddMemberToTier and RemoveMember. ctx is wired through to the
// outbound request so a caller (e.g. AddToLuma, run synchronously inside
// an HTTP handler) that cancels or times out actually stops waiting on
// Luma instead of leaking a blocked goroutine.
func (api *LumaAPI) post(ctx context.Context, path string, body any) error {
	if api == nil || strings.TrimSpace(api.APIKey) == "" {
		return fmt.Errorf("luma is not configured")
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api.baseURL()+path, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-luma-api-key", api.APIKey)

	resp, err := api.httpClient().Do(req)
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
	return nil
}

// AddMemberToTier adds email to tierID. Only requires an API key — safe to
// call on a nil receiver (returns an error rather than panicking), since
// main.go leaves the client nil when LUMA_API_KEY isn't set.
//
// Known failure mode (diagnosed 2026-09-22, no code fix yet): a person
// already holding an active membership in a *different* tier on this
// calendar does not get added or moved here — the call returns success but
// silently no-ops, leaving them out of tierID entirely. Hit in production
// for three members who'd been placed in the "Community" tier by hand
// before this integration existed; SyncAll reported no failures for them,
// yet they never appeared in the Members tier. Luma's API has no
// move-tier endpoint — the manual fix was declining their existing
// membership (the same update-status call RemoveMember below makes) before
// adding them here. Worth wiring that decline-then-add sequence into an
// admin action if this recurs.
func (api *LumaAPI) AddMemberToTier(ctx context.Context, email, tierID string) error {
	if strings.TrimSpace(tierID) == "" {
		return fmt.Errorf("luma tier id is missing")
	}
	return api.post(ctx, "/memberships/members/add", addMemberRequest{Email: email, MembershipTierID: tierID})
}

// RemoveMember declines email's membership — Luma's only "remove from
// tier" primitive; there's no hard-delete endpoint. This is a status
// change, not a true delete: an admin can still see and re-approve the
// membership from Luma's own dashboard. Treats lumaStatusNoMembership as
// success, not a failure: it means email was never added to any Luma tier
// in the first place (e.g. offboarded before sync-all or "Add to Luma
// Members" ever ran for them) — the end state this call is trying to
// reach (not an active Luma member) is already true either way, same
// "nothing to do here is still success" reasoning as a Mattermost account
// lookup 404 elsewhere in this codebase.
func (api *LumaAPI) RemoveMember(ctx context.Context, email string) error {
	err := api.post(ctx, "/memberships/members/update-status", updateMemberStatusRequest{UserID: email, Status: "declined"})
	var lumaErr *LumaAPIError
	if errors.As(err, &lumaErr) && lumaErr.Status == lumaStatusNoMembership {
		return nil
	}
	return err
}
