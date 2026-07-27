package validation

// TeamQuestion is the wire-format shape of a single team-specific follow-up
// question, returned by the public token-lookup endpoint. The actual
// questions live in the team_questions table (models.TeamQuestion), managed
// per team by that team's head from the admin UI — this struct is just the
// DTO shape, not a data source.
type TeamQuestion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
}
