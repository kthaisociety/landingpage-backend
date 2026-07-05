package handlers

import (
	"backend/internal/mailchimp"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/validation"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NewsletterHandler struct {
	db        *gorm.DB
	mailchimp *mailchimp.MailchimpAPI
}

func NewNewsletterHandler(db *gorm.DB, mailchimpApi *mailchimp.MailchimpAPI) *NewsletterHandler {
	return &NewsletterHandler{db: db, mailchimp: mailchimpApi}
}

func (h *NewsletterHandler) Register(r *gin.RouterGroup) {
	nl := r.Group("/newsletter")
	nl.POST("/subscribe", middleware.RateLimit(), h.Subscribe)
}

type newsletterSubscribeBody struct {
	FirstName            string   `json:"firstName"`
	LastName             string   `json:"lastName"`
	Email                string   `json:"email"`
	Gender               string   `json:"gender"`
	University           string   `json:"university"`
	Programme            string   `json:"programme"`
	GraduationYear       int      `json:"graduationYear"`
	Interests            []string `json:"interests"`
	DataRetentionConsent bool     `json:"dataRetentionConsent"`
}

func (h *NewsletterHandler) Subscribe(c *gin.Context) {
	var body newsletterSubscribeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := validateNewsletterSubscribeBody(body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	subscription, err := upsertNewsletterSubscription(h.db, newsletterSubscriptionFields{
		FirstName:            strings.TrimSpace(body.FirstName),
		LastName:             strings.TrimSpace(body.LastName),
		Email:                strings.TrimSpace(body.Email),
		EmailNormalized:      normalizeEmail(body.Email),
		Gender:               body.Gender,
		University:           strings.TrimSpace(body.University),
		Programme:            strings.TrimSpace(body.Programme),
		GraduationYear:       body.GraduationYear,
		Interests:            body.Interests,
		DataRetentionConsent: body.DataRetentionConsent,
		Source:               models.NewsletterSourceForm,
	})
	if err != nil {
		log.Printf("newsletter subscribe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not subscribe"})
		return
	}

	if err := h.mailchimp.SubscribeNewsletterSubscriber(subscription); err != nil {
		log.Printf("newsletter subscribe: mailchimp sync failed for %s: %v", subscription.Email, err)
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func validateNewsletterSubscribeBody(body newsletterSubscribeBody) error {
	firstName := strings.TrimSpace(body.FirstName)
	if len(firstName) == 0 || len(firstName) > 80 {
		return fmt.Errorf("first name is required and must be at most 80 characters")
	}
	lastName := strings.TrimSpace(body.LastName)
	if len(lastName) == 0 || len(lastName) > 80 {
		return fmt.Errorf("last name is required and must be at most 80 characters")
	}
	if !isValidEmail(body.Email) {
		return fmt.Errorf("valid email is required")
	}
	if _, ok := validation.AllowedGenders[body.Gender]; !ok {
		return fmt.Errorf("invalid gender")
	}
	if strings.TrimSpace(body.University) == "" {
		return fmt.Errorf("university is required")
	}
	if strings.TrimSpace(body.Programme) == "" {
		return fmt.Errorf("programme is required")
	}
	if body.GraduationYear < 2026 || body.GraduationYear > 2100 {
		return fmt.Errorf("graduation year must be 2026 or later")
	}
	if len(body.Interests) == 0 {
		return fmt.Errorf("choose at least one area of interest")
	}
	if len(body.Interests) > len(validation.AllowedInterests) {
		return fmt.Errorf("choose at most %d areas of interest", len(validation.AllowedInterests))
	}
	seenInterests := make(map[string]struct{}, len(body.Interests))
	for _, interest := range body.Interests {
		if _, ok := validation.AllowedInterests[interest]; !ok {
			return fmt.Errorf("invalid interest")
		}
		if _, ok := seenInterests[interest]; ok {
			return fmt.Errorf("each interest can only be selected once")
		}
		seenInterests[interest] = struct{}{}
	}
	if !body.DataRetentionConsent {
		return fmt.Errorf("data retention consent is required")
	}
	return nil
}

// newsletterSubscriptionFields is the shared shape used to create/update a
// NewsletterSubscription from either the newsletter form itself or an
// applicant opting in from the general application form.
type newsletterSubscriptionFields struct {
	FirstName            string
	LastName             string
	Email                string
	EmailNormalized      string
	Gender               string
	University           string
	Programme            string
	GraduationYear       int
	Interests            []string
	DataRetentionConsent bool
	Source               string
}

// upsertNewsletterSubscription creates or updates the subscriber record for
// the given (normalized) email, so re-subscribing or opting in again from an
// application just refreshes the stored fields instead of erroring.
//
// This is done as a single atomic INSERT ... ON CONFLICT rather than a
// SELECT followed by CREATE/UPDATE: two concurrent submissions for the same
// email would otherwise both see "not found" and race to CREATE, and the
// loser would hit the email_normalized unique constraint and return a 500
// (skipping the Mailchimp sync) even though the subscription was already
// stored. Source is intentionally left out of the update so a later
// re-subscribe/opt-in doesn't overwrite how someone first signed up.
func upsertNewsletterSubscription(db *gorm.DB, input newsletterSubscriptionFields) (*models.NewsletterSubscription, error) {
	subscription := models.NewsletterSubscription{
		Id:                   uuid.New(),
		FirstName:            input.FirstName,
		LastName:             input.LastName,
		Email:                input.Email,
		EmailNormalized:      input.EmailNormalized,
		Gender:               input.Gender,
		University:           input.University,
		Programme:            input.Programme,
		GraduationYear:       input.GraduationYear,
		Interests:            pq.StringArray(input.Interests),
		DataRetentionConsent: input.DataRetentionConsent,
		Source:               input.Source,
	}

	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "email_normalized"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"first_name", "last_name", "email", "gender", "university",
			"programme", "graduation_year", "interests", "data_retention_consent",
			"updated_at",
		}),
	}).Create(&subscription).Error
	if err != nil {
		return nil, err
	}

	// Re-read rather than trust the in-memory struct: on the conflict path
	// the DB keeps the original row's Id/CreatedAt, which the local struct
	// above (built with a fresh Id) does not reflect.
	var stored models.NewsletterSubscription
	if err := db.Where("email_normalized = ?", input.EmailNormalized).First(&stored).Error; err != nil {
		return nil, err
	}

	return &stored, nil
}
