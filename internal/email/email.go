package email

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"os"
	"path"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

type mailer interface {
	Send(ctx context.Context, to, subject, htmlBody, textBody string) error
}

type SESMailer struct {
	svc     *ses.Client
	sender  string
	replyTo string
	charset string
}

var defaultMailer mailer

//go:embed templates/*.html templates/*/*.html
var emailTemplates embed.FS

func InitEmailService(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("no config set")
	}
	if cfg.SES.Sender == "" {
		return fmt.Errorf("no SES sender set")
	}
	if err := validateAWSCredentialEnv(); err != nil {
		return err
	}

	// Build AWS config load options
	opts := []func(*awsconfig.LoadOptions) error{}

	if cfg.SES.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.SES.Region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	defaultMailer = &SESMailer{
		svc:     ses.NewFromConfig(awsCfg),
		sender:  cfg.SES.Sender,
		replyTo: cfg.SES.ReplyTo,
		charset: "UTF-8",
	}
	return nil
}

func validateAWSCredentialEnv() error {
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		value := strings.TrimSpace(os.Getenv(key))
		if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
			return fmt.Errorf("%s is still set to a placeholder value; set real AWS credentials or remove it to use the default AWS credential chain", key)
		}
	}
	return nil
}

// Email data struct, contains all fields used in emails
type EmailData struct {
	EmailConfig
	Profile  models.Profile
	Event    models.Event // For event emails
	URL      string       // For registration, password reset, and event emails
	ImageURL string
	Text     string // For custom text used in event survey and custom emails
}

type GeneralApplicationEmailData struct {
	EmailData
	Application models.GeneralApplication
}

// Helper function to create a new EmailData struct with default values
func newEmailData() EmailData {
	return EmailData{
		EmailConfig: DefaultEmailConfig,
	}
}

// Sends an email using Amazon SES.
//
// Parameters:
//
//   - ctx: GO context
//   - to: The email adress of the recipient
//   - subject: The subject line of the email
//   - htmlBody: The HTML email body
//   - textBody: Fallback for non-HTML email clients (Defaults to message telling user to use a proper client)
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func (m *SESMailer) Send(ctx context.Context, to, subject, htmlBody, textBody string) error {
	if textBody == "" {
		textBody = "Please use an HTML-capable email client to view this email."
	}
	_, err := m.svc.SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(m.sender),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		ReplyToAddresses: []string{m.replyTo},
		Message: &types.Message{
			Subject: &types.Content{
				Charset: aws.String(m.charset),
				Data:    aws.String(subject),
			},
			Body: &types.Body{
				Html: &types.Content{
					Charset: aws.String(m.charset),
					Data:    aws.String(htmlBody),
				},
				Text: &types.Content{
					Charset: aws.String(m.charset),
					Data:    aws.String(textBody),
				},
			},
		},
	})
	return err
}

// Internal helper used by all public senders
func sendEmail(recipient, subject, body string) error {
	if defaultMailer == nil {
		return fmt.Errorf("email service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return defaultMailer.Send(ctx, recipient, subject, body, "")
}

func parseEmailTemplate(parts ...string) (*template.Template, error) {
	files := []string{
		"templates/base.html",
		path.Join(append([]string{"templates"}, parts...)...),
	}

	return template.New("base").Funcs(templateFuncs).ParseFS(emailTemplates, files...)
}

// Custom template functions for formatting
var templateFuncs = template.FuncMap{
	// formatDate formats a time.Time to a human-readable date string
	// Example: "Monday, 2 January, 2006"
	"formatDate": func(t time.Time) string {
		return t.Format("Monday, 2 January, 2006")
	},
	// formatTime formats a time.Time to a human-readable time string
	// Example: "15:04"
	"formatTime": func(t time.Time) string {
		return t.Format("15:04")
	},
	// formatDateTime formats a time.Time to a human-readable date and time string
	// Example: "Monday, 2 January, 2006 at 15:04 PM"
	"formatDateTime": func(t time.Time) string {
		return t.Format("Monday, 2 January, 2006 at 15:04 PM")
	},
}

// SendGeneralApplicationConfirmation sends a confirmation email after a general application is received.
func SendGeneralApplicationConfirmation(application models.GeneralApplication) error {
	tmpl, err := parseEmailTemplate("application", "confirmation.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	data := GeneralApplicationEmailData{
		EmailData:   newEmailData(),
		Application: application,
	}
	data.Profile = models.Profile{
		Email:     application.Email,
		FirstName: application.FirstName,
		LastName:  application.LastName,
	}
	data.URL = "https://kthais.com/"

	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return sendEmail(application.Email, "KTH AI Society application received", htmlBody.String())
}

// Sends a registration confirmation email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - verificationURL: The URL for registration confirmation
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func SendRegistrationEmail(profile models.Profile, verificationURL string) error {
	// Parse both base and registration templates
	tmpl, err := parseEmailTemplate("profile", "register.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	data := newEmailData()
	data.Profile = profile
	data.URL = verificationURL

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email
	subject := "Complete Your KTHAIS Registration"

	return sendEmail(recipient, subject, htmlBody.String())
}

// Sends a login email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - loginURL: The URL for logging in
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func sendLoginEmail(profile models.Profile, loginURL string) error {
	// Parse both base and password templates
	tmpl, err := parseEmailTemplate("profile", "login.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	data := newEmailData()
	data.Profile = profile
	data.URL = loginURL

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email
	subject := "Reset Your KTHAIS Password"

	return sendEmail(recipient, subject, htmlBody.String())
}

// Sends an event registration confirmation email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - event: The struct for the event
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func sendEventRegistrationEmail(profile models.Profile, event models.Event) error {
	// Parse both base and password templates
	tmpl, err := parseEmailTemplate("event", "register.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	data := newEmailData()
	data.Profile = profile
	data.Event = event
	data.URL = "google.se" // Placeholder
	data.Event = event

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email
	subject := "Your registration to " + event.Title

	return sendEmail(recipient, subject, htmlBody.String())
}

// Sends an event reminder email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - event: The struct for the event
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func sendEventReminderEmail(profile models.Profile, event models.Event) error {
	// Parse both base and password templates
	tmpl, err := parseEmailTemplate("event", "reminder.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	data := newEmailData()
	data.Profile = profile
	data.Event = event
	data.URL = "google.se" // Placeholder
	data.Event = event

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email
	subject := "Remember " + event.Title + "?"

	return sendEmail(recipient, subject, htmlBody.String())
}

// Sends an event cancelation email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - event: The struct for the event
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func sendEventCancelEmail(profile models.Profile, event models.Event) error {
	// Parse both base and password templates
	tmpl, err := parseEmailTemplate("event", "cancel.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	data := newEmailData()
	data.Profile = profile
	data.Event = event
	data.URL = "google.se" // Placeholder
	data.Event = event

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email
	subject := "Remember " + event.Title + "?"

	return sendEmail(recipient, subject, htmlBody.String())
}

// Sends a custom email
//
// Parameters:
//   - profile: The profile struct for the recipient
//   - subject: The email subject
//   - customText: The email text
//   - customButtonText: The email button text
//   - customButtonURL: The email button URL
//   - customImageURL: The email image url (use an empty string for no image)
//
// Returns:
//   - error: nil if the email was sent successfully, or an error if it failed
func sendCustomEmail(profile models.Profile, subject string, customText string, customButtonText string, customButtonURL string, customImageURL string) error {
	// Parse both base and password templates
	tmpl, err := parseEmailTemplate("profile", "custom.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Prepare data for the email template
	type CustomEmailData struct {
		EmailData
		CustomText       string
		CustomButtonText string
		CustomButtonURL  string
		CustomImageURL   string
	}
	data := CustomEmailData{
		EmailData:        newEmailData(),
		CustomText:       customText,
		CustomButtonText: customButtonText,
		CustomButtonURL:  customButtonURL,
		CustomImageURL:   customImageURL,
	}
	data.Profile = profile

	// Render the template into a buffer
	var htmlBody bytes.Buffer
	err = tmpl.ExecuteTemplate(&htmlBody, "base", data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Define email parameters
	recipient := profile.Email

	return sendEmail(recipient, subject, htmlBody.String())
}
