// Package validation holds allow-lists shared by any handler that collects
// applicant-style fields (general applications, newsletter signups), so the
// two flows can't drift out of sync with each other.
package validation

import "fmt"

var AllowedGenders = map[string]struct{}{
	"Female":            {},
	"Male":              {},
	"Non-binary":        {},
	"Prefer not to say": {},
	"Other":             {},
}

var AllowedInterests = map[string]struct{}{
	"Startups & Venture Creation":      {},
	"Venture Capital & Private Equity": {},
	"AI Consulting & Implementation":   {},
	"Healthcare & Biotech":             {},
	"Consumer Tech & Retail":           {},
	"Finance & Investment":             {},
}

// ValidateInterests checks that interests is a non-empty, duplicate-free
// subset of AllowedInterests. Shared by the general application and
// newsletter signup flows so their validation can't drift apart.
func ValidateInterests(interests []string) error {
	if len(interests) == 0 {
		return fmt.Errorf("choose at least one area of interest")
	}
	if len(interests) > len(AllowedInterests) {
		return fmt.Errorf("choose at most %d areas of interest", len(AllowedInterests))
	}
	seen := make(map[string]struct{}, len(interests))
	for _, interest := range interests {
		if _, ok := AllowedInterests[interest]; !ok {
			return fmt.Errorf("invalid interest")
		}
		if _, ok := seen[interest]; ok {
			return fmt.Errorf("each interest can only be selected once")
		}
		seen[interest] = struct{}{}
	}
	return nil
}
