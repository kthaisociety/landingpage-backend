package luma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveMemberSuccess(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"membership_id":"mem-1","status":"declined"}`))
	}))
	t.Cleanup(server.Close)

	api := &LumaAPI{APIKey: "test-luma-key", BaseURL: server.URL, HTTPClient: server.Client()}

	require.NoError(t, api.RemoveMember(context.Background(), "grace.hopper@kthais.com"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/memberships/members/update-status", gotPath)
	require.Equal(t, "grace.hopper@kthais.com", gotBody["user_id"])
	require.Equal(t, "declined", gotBody["status"])
}

// TestRemoveMemberNoMembershipIsNotAFailure is a regression for offboarding
// a member who was never added to any Luma tier (e.g. sync-all/"Add to
// Luma Members" never ran for them) — Luma's own status 470 for this case
// ("We can't update the status of a member without a membership") must be
// treated as success, not surfaced as a 502 up through
// OnboardingHandler.RemoveFromLuma and onboarding-service's
// Deactivate/Delete.
func TestRemoveMemberNoMembershipIsNotAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The literal Luma actually returns, not lumaStatusNoMembership —
		// asserting against the same constant RemoveMember checks would let
		// this test stay green even if that constant's value were wrong.
		w.WriteHeader(470)
		_, _ = w.Write([]byte(`{"message":"We can't update the status of a member without a membership.","code":null}`))
	}))
	t.Cleanup(server.Close)

	api := &LumaAPI{APIKey: "test-luma-key", BaseURL: server.URL, HTTPClient: server.Client()}

	require.NoError(t, api.RemoveMember(context.Background(), "georg@kthais.com"),
		"no existing membership means the end state (not an active member) is already true")
}

// TestRemoveMemberOtherFailuresStillReturnAnError proves the no-membership
// tolerance above isn't a blanket swallow of every RemoveMember error — a
// real failure (wrong/expired API key, Luma outage, etc.) must still
// surface.
func TestRemoveMemberOtherFailuresStillReturnAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid API key"}`))
	}))
	t.Cleanup(server.Close)

	api := &LumaAPI{APIKey: "test-luma-key", BaseURL: server.URL, HTTPClient: server.Client()}

	err := api.RemoveMember(context.Background(), "georg@kthais.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}
