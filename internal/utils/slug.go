package utils

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// diacriticsStripper transliterates accented characters to their closest
// ASCII form (e.g. "ö" -> "o") by decomposing and dropping combining marks,
// so slugs stay URL-friendly for names with non-ASCII letters.
var diacriticsStripper = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Slugify normalizes a display name (or any string) into a lowercase,
// hyphen-separated URL segment: "Timothy Lindblom" -> "timothy-lindblom".
// It does not guarantee uniqueness — callers append a "-2", "-3", ... suffix
// on collision.
func Slugify(name string) string {
	ascii, _, err := transform.String(diacriticsStripper, name)
	if err != nil {
		ascii = name
	}

	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(ascii) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen && b.Len() > 0:
			b.WriteRune('-')
			prevHyphen = true
		}
	}

	return strings.TrimRight(b.String(), "-")
}
