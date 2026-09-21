package email

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

var (
	// uid, _      = uuid.NewUUID()
	mockProfile = models.Profile{
		UserId:         123,
		Email:          "jack.gugolz@gmail.com",
		FirstName:      "Jack",
		LastName:       "Gugolz",
		University:     "KTH",
		Programme:      models.StudyProgramComputerScience,
		GraduationYear: 2027,
	}

	mockEvent = models.Event{
		Title:              "Test event",
		Description:        "Event for email testing",
		RegistrationMethod: models.RegistrationMethodWebsite,
		ICSFileEndpoint:    "/events/1.ics",
		Location:           "Tech Hub, Room 3A",
		Image:              "https://example.com/images/go-workshop.png",
		RegistrationMax:    50,
		TypeOfEvent:        models.EventTypeWorkshop,
		StartDate:          time.Date(2025, 11, 3, 9, 0, 0, 0, time.UTC),
		EndDate:            time.Date(2025, 11, 3, 17, 0, 0, 0, time.UTC),
		CreatedBy:          1, // user ID (foreign key)
	}
)

type captureMailer struct {
	to       string
	subject  string
	htmlBody string
	textBody string
}

func (m *captureMailer) Send(ctx context.Context, to, subject, htmlBody, textBody string) error {
	m.to = to
	m.subject = subject
	m.htmlBody = htmlBody
	m.textBody = textBody
	return nil
}

func init() {
	// Load .env file if it exists
	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("No .env file found: %v", err)
	}
}

func TestMain(m *testing.M) {
	file := "../../.env"
	if _, err := os.Stat(file); err != nil {
		return
	}
	// Load config before running tests
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("CONFIG ERROR: %+v", err)
		os.Exit(0)
	}
	log.Println("Loaded config:", cfg)

	// Initialize email service with config
	InitEmailService(cfg)
	log.Println("Initialized email service")

	// Run tests
	os.Exit(m.Run())
}

func TestSendRegistrationEmail(t *testing.T) {
	verificationURL := "https://kthais.com"

	err := SendRegistrationEmail(mockProfile, verificationURL)
	assert.Nil(t, err, "SendRegistrationEmail should not return an error")
}

func TestSendGeneralApplicationConfirmation(t *testing.T) {
	previousMailer := defaultMailer
	mailer := &captureMailer{}
	defaultMailer = mailer
	t.Cleanup(func() {
		defaultMailer = previousMailer
	})

	application := models.GeneralApplication{
		ApplicationYear: 2026,
		FirstName:       "Ada",
		LastName:        "Lovelace",
		Email:           "ada@example.com",
		Teams:           pq.StringArray{"Development", "Research"},
		Availability:    "6-8 hours",
	}

	err := SendGeneralApplicationConfirmation(application)

	assert.Nil(t, err, "SendGeneralApplicationConfirmation should not return an error")
	assert.Equal(t, "ada@example.com", mailer.to)
	assert.Equal(t, "KTH AI Society application received", mailer.subject)
	assert.Contains(t, mailer.htmlBody, "Hello, Ada!")
	assert.Contains(t, mailer.htmlBody, "general application for 2026")
	assert.Contains(t, mailer.htmlBody, "Development")
	assert.Contains(t, mailer.htmlBody, "Research")
	assert.Contains(t, mailer.htmlBody, "6-8 hours")
}

func TestSendGeneralApplicationConfirmationDoesNotDependOnWorkingDirectory(t *testing.T) {
	previousMailer := defaultMailer
	mailer := &captureMailer{}
	defaultMailer = mailer
	t.Cleanup(func() {
		defaultMailer = previousMailer
	})

	previousDir, err := os.Getwd()
	assert.NoError(t, err)
	tempDir := t.TempDir()
	assert.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		assert.NoError(t, os.Chdir(previousDir))
	})

	application := models.GeneralApplication{
		ApplicationYear: 2026,
		FirstName:       "Ada",
		LastName:        "Lovelace",
		Email:           "ada@example.com",
		Teams:           pq.StringArray{"Development"},
		Availability:    "6-8 hours",
	}

	err = SendGeneralApplicationConfirmation(application)

	assert.Nil(t, err, "SendGeneralApplicationConfirmation should not depend on the process working directory")
	assert.Equal(t, "ada@example.com", mailer.to)
	assert.Contains(t, mailer.htmlBody, "Hello, Ada!")
}

func TestValidateAWSCredentialEnvRejectsPlaceholders(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "<ACCESS_KEY>")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")

	err := validateAWSCredentialEnv()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
	assert.Contains(t, err.Error(), "placeholder")
}

func TestSendLoginEmail(t *testing.T) {
	passwordResetURL := "http://kthais.com"

	err := sendLoginEmail(mockProfile, passwordResetURL)
	assert.Nil(t, err, "sendLoginEmail should not return an error")
}

func TestSendEventRegistrationEmail(t *testing.T) {
	err := sendEventRegistrationEmail(mockProfile, mockEvent)
	assert.Nil(t, err, "sendEventRegistrationEmail should not return an error")
}

func TestSendEventReminderEmail(t *testing.T) {
	err := sendEventReminderEmail(mockProfile, mockEvent)
	assert.Nil(t, err, "sendEventReminderEmail should not return an error")
}

func TestSendEventCancelEmail(t *testing.T) {
	err := sendEventCancelEmail(mockProfile, mockEvent)
	assert.Nil(t, err, "sendEventCancelEmail should not return an error")
}

func TestSendCustomEmail(t *testing.T) {
	err := sendCustomEmail(mockProfile, "Custom email", "Custom email text :)", "Button text", "https://kthais.com", "")
	assert.Nil(t, err, "sendCustomEmail should not return an error")
}

func TestSendCustomEmailWithImage(t *testing.T) {
	err := sendCustomEmail(mockProfile, "Custom email with image", "Custom email text :)", "Button text", "https://kthais.com", "https://kthais.com/files/__sized__/event/picture/Asort_Ventures_-_Website_Poster-crop-c0-5__0-5-1500x1000-70.jpg")
	assert.Nil(t, err, "sendCustomEmail should not return an error")
}

// TestSendOnboardingEmailDoesNotDuplicateGreeting is a regression test for a
// Go template gotcha: an empty {{define "email_opening"}}{{end}} in
// templates/onboarding/generic.html was silently ignored (Go's template
// engine only lets a later {{define}} override an earlier same-named one
// when the later one is non-empty), so base.html's own default "Hello!"
// greeting kept rendering ahead of the "Hi {name}," greeting
// onboarding-service already bakes into the email body itself.
func TestSendOnboardingEmailDoesNotDuplicateGreeting(t *testing.T) {
	previousMailer := defaultMailer
	mailer := &captureMailer{}
	defaultMailer = mailer
	t.Cleanup(func() {
		defaultMailer = previousMailer
	})

	body := "Hi Test,\n\nCongratulations on being accepted to KTH AI Society!"
	err := SendOnboardingEmail("test@example.com", "Welcome to KTH AI Society", body, "https://kthais.com/onboarding/start?token=abc", "Start onboarding")

	assert.Nil(t, err, "SendOnboardingEmail should not return an error")
	assert.Equal(t, "test@example.com", mailer.to)
	assert.Contains(t, mailer.htmlBody, "Hi Test,")
	assert.NotContains(t, mailer.htmlBody, "Hello!", "base.html's default greeting should be suppressed, not duplicated")
	assert.NotContains(t, mailer.htmlBody, "Hello,", "base.html's default greeting should be suppressed, not duplicated")
}

// TestSendOnboardingEmailLinkifiesBareURLs covers the contract email's
// "Contract: <url>" / "Bylaws: <url>" lines (and any other onboarding email
// body that happens to mention a URL): they must render as real clickable
// links, not just text that looks like one.
func TestSendOnboardingEmailLinkifiesBareURLs(t *testing.T) {
	previousMailer := defaultMailer
	mailer := &captureMailer{}
	defaultMailer = mailer
	t.Cleanup(func() {
		defaultMailer = previousMailer
	})

	body := "Hi Test,\n\nRead ahead.\n\nContract: https://drive.google.com/file/d/abc/view\n\nBylaws: https://kthais.com/bylaws.pdf."
	err := SendOnboardingEmail("test@example.com", "Your KTH AI Society membership contract", body, "https://lu.ma/kickoff", "RSVP for the kick-off event")

	assert.Nil(t, err, "SendOnboardingEmail should not return an error")
	assert.Contains(t, mailer.htmlBody, `<a href="https://drive.google.com/file/d/abc/view">https://drive.google.com/file/d/abc/view</a>`)
	// The trailing sentence-ending period must land outside the link, not
	// get swallowed into the href.
	assert.Contains(t, mailer.htmlBody, `<a href="https://kthais.com/bylaws.pdf">https://kthais.com/bylaws.pdf</a>.`)
}

func TestLinkifyOnboardingBodyURLs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bare URL",
			input: "See https://example.com for details",
			want:  `See <a href="https://example.com">https://example.com</a> for details`,
		},
		{
			name:  "trailing period is excluded from the link",
			input: "See https://example.com.",
			want:  `See <a href="https://example.com">https://example.com</a>.`,
		},
		{
			name:  "trailing comma is excluded from the link",
			input: "https://example.com, and more",
			want:  `<a href="https://example.com">https://example.com</a>, and more`,
		},
		{
			name:  "multiple URLs each get linkified",
			input: "https://a.example.com and https://b.example.com",
			want:  `<a href="https://a.example.com">https://a.example.com</a> and <a href="https://b.example.com">https://b.example.com</a>`,
		},
		{
			name:  "no URL is left unchanged",
			input: "no links here",
			want:  "no links here",
		},
		{
			// Pre-escape text "<https://example.com/doc>" — HTMLEscapeString
			// has already turned the wrapping angle brackets into entities
			// by the time this runs. The escaped '>' must not be swallowed
			// into the href.
			name:  "URL wrapped in already-escaped angle brackets stops at the boundary",
			input: "&lt;https://example.com/doc&gt; see above",
			want:  `&lt;<a href="https://example.com/doc">https://example.com/doc</a>&gt; see above`,
		},
		{
			name:  "a URL's own balanced trailing parenthesis is kept",
			input: "https://en.wikipedia.org/wiki/Function_(mathematics)",
			want:  `<a href="https://en.wikipedia.org/wiki/Function_(mathematics)">https://en.wikipedia.org/wiki/Function_(mathematics)</a>`,
		},
		{
			name:  "an unmatched trailing parenthesis from surrounding prose is excluded",
			input: "(see https://example.com)",
			want:  `(see <a href="https://example.com">https://example.com</a>)`,
		},
		{
			name:  "a URL with a balanced paren followed by prose punctuation trims only the punctuation",
			input: "https://en.wikipedia.org/wiki/Function_(mathematics).",
			want:  `<a href="https://en.wikipedia.org/wiki/Function_(mathematics)">https://en.wikipedia.org/wiki/Function_(mathematics)</a>.`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, linkifyOnboardingBodyURLs(tc.input))
		})
	}
}
