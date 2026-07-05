// Package validation holds allow-lists shared by any handler that collects
// applicant-style fields (general applications, newsletter signups), so the
// two flows can't drift out of sync with each other.
package validation

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
