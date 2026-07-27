package validation

// TeamQuestion is a single team-specific follow-up question.
type TeamQuestion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
}

// TeamQuestions is the single source of truth for the Team Questions
// follow-up form, keyed by the same team names as allowedApplicationTeams in
// the general application handler. The public token-lookup endpoint reads
// straight from here so the frontend never needs — or receives — a copy of
// any other team's questions.
//
// Placeholder content below; team leads should supply the real question text.
var TeamQuestions = map[string][]TeamQuestion{
	"Business": {
		{ID: "motivation", Text: "Why are you interested in the Business team specifically?", Required: true},
		{ID: "experience", Text: "Describe any relevant experience in business development, marketing, or partnerships.", Required: true},
	},
	"Development": {
		{ID: "motivation", Text: "Why are you interested in the Development team specifically?", Required: true},
		{ID: "stack", Text: "What programming languages or frameworks are you most comfortable with?", Required: true},
		{ID: "project", Text: "Link or describe a project you've built that you're proud of.", Required: false},
	},
	"Research": {
		{ID: "motivation", Text: "Why are you interested in the Research team specifically?", Required: true},
		{ID: "background", Text: "Describe your background in machine learning or a related research area.", Required: true},
	},
	"Growth": {
		{ID: "motivation", Text: "Why are you interested in the Growth team specifically?", Required: true},
		{ID: "experience", Text: "Describe any relevant experience in marketing, content, or community building.", Required: true},
	},
	"IT": {
		{ID: "motivation", Text: "Why are you interested in the IT team specifically?", Required: true},
		{ID: "stack", Text: "What infrastructure, DevOps, or systems experience do you have?", Required: true},
	},
}
