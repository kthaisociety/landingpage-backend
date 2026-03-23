package email

import (
	"log"
	"os"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/joho/godotenv"
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
