package utils

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Timothy Lindblom":  "timothy-lindblom",
		"Villim Prpić":      "villim-prpic",
		"  Extra   Spaces ": "extra-spaces",
		"Öyvind Åström":     "oyvind-astrom",
		"O'Brien-Smith":     "o-brien-smith",
		"":                  "",
		"123 Team":          "123-team",
	}

	for input, want := range cases {
		if got := Slugify(input); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
}
