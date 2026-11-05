package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/internal/models"

	"github.com/stretchr/testify/require"
)

func TestApplyPersonalFieldsKeepsStoredValueWhenOmitted(t *testing.T) {
	profile := models.Profile{AskMeAbout: "design", SongTitle: "greedy"}

	require.NoError(t, (profilePersonalInput{}).apply(&profile))
	require.Equal(t, "design", profile.AskMeAbout)
	require.Equal(t, "greedy", profile.SongTitle)
}

func TestApplyPersonalFieldsClearsWhenEmptyStringIsSent(t *testing.T) {
	profile := models.Profile{AskMeAbout: "design", SongHref: "https://music.apple.com/song"}
	empty := ""

	require.NoError(t, (profilePersonalInput{AskMeAbout: &empty, SongHref: &empty}).apply(&profile))
	require.Empty(t, profile.AskMeAbout)
	require.Empty(t, profile.SongHref)
}

func TestApplyPersonalFieldsRejectsScriptURL(t *testing.T) {
	bad := "javascript:alert(1)"
	err := (profilePersonalInput{SongHref: &bad}).apply(&models.Profile{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "songHref")
}

func TestApplyPersonalFieldsAcceptsAppleLink(t *testing.T) {
	link := "https://music.apple.com/us/album/greedy/1719660855?i=1719661252"
	profile := models.Profile{}

	require.NoError(t, (profilePersonalInput{SongHref: &link}).apply(&profile))
	require.Equal(t, link, profile.SongHref)
}

func TestApplyPersonalFieldsRejectsOverlongPrompt(t *testing.T) {
	tooLong := strings.Repeat("a", 1001)
	err := (profilePersonalInput{AskMeAbout: &tooLong}).apply(&models.Profile{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "askMeAbout")
}

func TestPersonalFieldsUnmarshalFromJSON(t *testing.T) {
	var input struct {
		FirstName string `json:"firstName"`
		profilePersonalInput
	}

	require.NoError(t, json.Unmarshal([]byte(`{"firstName":"Noor"}`), &input))
	require.Nil(t, input.AskMeAbout)
	require.Nil(t, input.SongHref)

	require.NoError(t, json.Unmarshal([]byte(`{"firstName":"Noor","askMeAbout":"design","songHref":"https://music.apple.com/song"}`), &input))
	require.NotNil(t, input.AskMeAbout)
	require.Equal(t, "design", *input.AskMeAbout)
	require.NotNil(t, input.SongHref)
	require.Equal(t, "https://music.apple.com/song", *input.SongHref)
}
