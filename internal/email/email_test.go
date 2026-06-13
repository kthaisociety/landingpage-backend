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
