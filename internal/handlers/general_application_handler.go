package handlers

import (
	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/mailchimp"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"backend/internal/validation"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	generalApplicationYear        = 2026
	generalApplicationMaxResumeMB = 10
	generalApplicationMaxResume   = generalApplicationMaxResumeMB << 20
)

var allowedApplicationTeams = map[string]struct{}{
	"Business":    {},
	"Development": {},
	"Research":    {},
	"Growth":      {},
	"IT":          {},
}

var allowedApplicationAvailability = map[string]struct{}{
	"4-6 hours":       {},
	"6-8 hours":       {},
	"8 hours or more": {},
}

var allowedApplicationInterests = validation.AllowedInterests

var allowedApplicationGenders = validation.AllowedGenders

var allowedApplicationStatuses = map[models.GeneralApplicationStatus]struct{}{
	models.GeneralApplicationStatusAvailable:    {},
	models.GeneralApplicationStatusInterviewing: {},
	models.GeneralApplicationStatusIneligible:   {},
}

var allowedResumeExtensions = map[string]struct{}{
	".pdf":  {},
	".doc":  {},
	".docx": {},
}

var allowedResumeContentTypes = map[string]struct{}{
	"application/pdf":    {},
	"application/msword": {},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": {},
}

type GeneralApplicationHandler struct {
	db        *gorm.DB
	cfg       *config.Config
	mailchimp *mailchimp.MailchimpAPI
}

func getAdminIdentity(c *gin.Context) (userID uuid.UUID, adminEmail string, ok bool) {
	token := utils.GetJWT(c)
	if token == nil {
		return uuid.UUID{}, "", false
	}
	claims := utils.GetClaims(token)
	rawID, hasID := claims["user_id"].(string)
	rawEmail, hasEmail := claims["email"].(string)
	if !hasID || !hasEmail {
		return uuid.UUID{}, "", false
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.UUID{}, "", false
	}
	return parsed, rawEmail, true
}

type generalApplicationInput struct {
	FirstName            string
	LastName             string
	Email                string
	Gender               string
	University           string
	Programme            string
	GraduationYear       int
	LinkedinURL          string
	AdditionalLinks      []string
	Teams                []string
	Interests            []string
	Availability         string
	Contribution         string
	DataRetentionConsent bool
	NewsletterOptIn      bool
}

func NewGeneralApplicationHandler(db *gorm.DB, cfg *config.Config, mailchimpApi *mailchimp.MailchimpAPI) *GeneralApplicationHandler {
	return &GeneralApplicationHandler{db: db, cfg: cfg, mailchimp: mailchimpApi}
}

func (h *GeneralApplicationHandler) Register(r *gin.RouterGroup) {
	applications := r.Group("/applications")
	applications.POST("/general", middleware.RateLimit(), h.Create)

	admin := applications.Group("/admin")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.GET("", h.AdminList)
	admin.PATCH("/:id/status", h.AdminUpdateStatus)
	admin.DELETE("/:id", h.AdminDelete)
	admin.GET("/:id/resume", h.AdminDownloadResume)
	admin.POST("/:id/claim", h.AdminClaimApplication)
	admin.POST("/:id/release", h.AdminReleaseApplication)
	admin.POST("/:id/cancel-interview", h.AdminCancelInterview)
	admin.POST("/:id/send-invite", h.AdminSendInterviewInvite)
	admin.PATCH("/:id/ineligible", h.AdminMarkIneligible)
	admin.PATCH("/:id/restore", h.AdminRestoreApplication)
	admin.GET("/:id/notes", h.AdminGetNotes)
	admin.PUT("/:id/notes", h.AdminUpdateNotes)
}

func (h *GeneralApplicationHandler) Create(c *gin.Context) {
	input, err := parseGeneralApplicationForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateGeneralApplicationInput(input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fileHeader, err := c.FormFile("resume")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resume is required"})
		return
	}

	resumeContentType, err := validateResumeFile(fileHeader)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	emailNormalized := normalizeEmail(input.Email)
	var existing int64
	if err := h.db.Model(&models.GeneralApplication{}).
		Where("application_year = ? AND email_normalized = ?", generalApplicationYear, emailNormalized).
		Count(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing application"})
		return
	}
	if existing > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "application already exists for this email"})
		return
	}

	resumeBytes, err := readUploadedFile(fileHeader)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read resume"})
		return
	}

	applicationID := uuid.New()
	var application models.GeneralApplication
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := purgeSoftDeletedGeneralApplication(tx, generalApplicationYear, emailNormalized); err != nil {
			return fmt.Errorf("failed to clear deleted application: %w", err)
		}

		application = models.GeneralApplication{
			Id:                    applicationID,
			ApplicationYear:       generalApplicationYear,
			FirstName:             strings.TrimSpace(input.FirstName),
			LastName:              strings.TrimSpace(input.LastName),
			Email:                 strings.TrimSpace(input.Email),
			EmailNormalized:       emailNormalized,
			Gender:                strings.TrimSpace(input.Gender),
			University:            strings.TrimSpace(input.University),
			Programme:             strings.TrimSpace(input.Programme),
			GraduationYear:        input.GraduationYear,
			LinkedinURL:           normalizeWebsiteLink(input.LinkedinURL),
			AdditionalLinks:       pq.StringArray(normalizeWebsiteLinks(input.AdditionalLinks)),
			ResumeFileName:        filepath.Base(fileHeader.Filename),
			ResumeContentType:     resumeContentType,
			Teams:                 pq.StringArray(input.Teams),
			TeamPreferencesRanked: true,
			TeamInterestReason:    "",
			Interests:             pq.StringArray(input.Interests),
			Availability:          strings.TrimSpace(input.Availability),
			Contribution:          strings.TrimSpace(input.Contribution),
			DataRetentionConsent:  input.DataRetentionConsent,
			Status:                models.GeneralApplicationStatusAvailable,
		}

		if shouldStoreResumeInDatabase(h.cfg) {
			application.ResumeData = resumeBytes
		} else {
			r2, err := utils.InitS3SDK(h.cfg)
			if err != nil {
				return fmt.Errorf("failed to initialize resume storage: %w", err)
			}
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileHeader.Filename)), ".")
			name := strings.TrimSuffix(filepath.Base(fileHeader.Filename), filepath.Ext(fileHeader.Filename))
			blob, err := models.NewBlobData(name, ext, applicationID, resumeBytes, tx, r2)
			if err != nil {
				return err
			}
			application.ResumeBlobID = blob.BlobId
		}

		return tx.Create(&application).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create application"})
		return
	}

	go func(application models.GeneralApplication) {
		if err := email.SendGeneralApplicationConfirmation(application); err != nil {
			log.Printf("failed to send general application confirmation email for %s: %v", application.Id, err)
		}
	}(application)

	if input.NewsletterOptIn {
		go func(application models.GeneralApplication) {
			subscription, err := upsertNewsletterSubscription(h.db, newsletterSubscriptionFields{
				FirstName:            application.FirstName,
				LastName:             application.LastName,
				Email:                application.Email,
				EmailNormalized:      application.EmailNormalized,
				Gender:               application.Gender,
				University:           application.University,
				Programme:            application.Programme,
				GraduationYear:       application.GraduationYear,
				Interests:            application.Interests,
				DataRetentionConsent: true,
				Source:               models.NewsletterSourceApplicationOptIn,
			})
			if err != nil {
				log.Printf("failed to store newsletter opt-in for application %s: %v", application.Id, err)
				return
			}
			if err := h.mailchimp.SubscribeNewsletterSubscriber(subscription); err != nil {
				log.Printf("newsletter opt-in: mailchimp sync failed for %s: %v", subscription.Email, err)
			}
		}(application)
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         application.Id,
		"status":     application.Status,
		"created_at": application.CreatedAt,
	})
}

func (h *GeneralApplicationHandler) AdminList(c *gin.Context) {
	year := generalApplicationYear
	if rawYear := c.Query("year"); rawYear != "" {
		parsed, err := strconv.Atoi(rawYear)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid year"})
			return
		}
		year = parsed
	}

	query := h.db.Model(&models.GeneralApplication{}).
		Where("application_year = ?", year).
		Order("created_at DESC")

	if rawStatus := strings.TrimSpace(c.Query("status")); rawStatus != "" {
		status := models.GeneralApplicationStatus(rawStatus)
		if _, ok := allowedApplicationStatuses[status]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
			return
		}
		query = query.Where("status = ?", status)
	}

	if team := strings.TrimSpace(c.Query("team")); team != "" {
		if _, ok := allowedApplicationTeams[team]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team"})
			return
		}
		query = query.Where("teams @> ARRAY[?]::text[]", team)
	}

	if q := strings.TrimSpace(c.Query("q")); q != "" {
		like := "%" + q + "%"
		query = query.Where(
			"first_name ILIKE ? OR last_name ILIKE ? OR email ILIKE ? OR university ILIKE ? OR programme ILIKE ? OR linkedin_url ILIKE ?",
			like,
			like,
			like,
			like,
			like,
			like,
		)
	}

	var applications []models.GeneralApplication
	if err := query.Find(&applications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch applications"})
		return
	}

	c.JSON(http.StatusOK, applications)
}

func (h *GeneralApplicationHandler) AdminUpdateStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var body struct {
		Status models.GeneralApplicationStatus `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if _, ok := allowedApplicationStatuses[body.Status]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	application.Status = body.Status
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update application status"})
		return
	}

	c.JSON(http.StatusOK, application)
}

func (h *GeneralApplicationHandler) AdminDelete(c *gin.Context) {
	token := utils.GetJWT(c)
	claims := utils.GetClaims(token)
	requesterID, err := uuid.Parse(claims["user_id"].(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	isIT, err := h.requesterIsOnTeam(requesterID, "IT")
	if err != nil || !isIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only IT admins can delete applications"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	if err := h.db.Unscoped().Delete(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete application"})
		return
	}

	c.Status(http.StatusNoContent)
}

// requesterIsOnTeam resolves a user's admin team the same way the frontend
// does: their own TeamMember entry for the given team takes priority, falling
// back to their self-declared Profile.AdminTeam.
func (h *GeneralApplicationHandler) requesterIsOnTeam(userID uuid.UUID, team string) (bool, error) {
	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		return false, err
	}

	var count int64
	if err := h.db.Model(&models.TeamMember{}).
		Where("user_id = ? AND team_member_department = ?", profile.UserId, team).
		Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}

	return profile.AdminTeam == team, nil
}

func purgeSoftDeletedGeneralApplication(db *gorm.DB, applicationYear int, emailNormalized string) error {
	return db.Unscoped().
		Where("application_year = ? AND email_normalized = ? AND deleted_at IS NOT NULL", applicationYear, emailNormalized).
		Delete(&models.GeneralApplication{}).
		Error
}

func (h *GeneralApplicationHandler) AdminDownloadResume(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	contentType := application.ResumeContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := strings.ReplaceAll(filepath.Base(application.ResumeFileName), `"`, "")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if len(application.ResumeData) > 0 {
		c.Data(http.StatusOK, contentType, application.ResumeData)
		return
	}

	var resumeBlob models.BlobData
	if err := h.db.First(&resumeBlob, "blob_id = ?", application.ResumeBlobID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resume not found"})
		return
	}

	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize resume storage"})
		return
	}
	data, err := resumeBlob.GetData(r2)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch resume"})
		return
	}

	c.Data(http.StatusOK, contentType, data)
}

func parseGeneralApplicationForm(c *gin.Context) (generalApplicationInput, error) {
	graduationYear, err := strconv.Atoi(strings.TrimSpace(c.PostForm("graduationYear")))
	if err != nil {
		return generalApplicationInput{}, fmt.Errorf("graduation year is required")
	}

	return generalApplicationInput{
		FirstName:            strings.TrimSpace(c.PostForm("firstName")),
		LastName:             strings.TrimSpace(c.PostForm("lastName")),
		Email:                strings.TrimSpace(c.PostForm("email")),
		Gender:               strings.TrimSpace(c.PostForm("gender")),
		University:           strings.TrimSpace(c.PostForm("university")),
		Programme:            strings.TrimSpace(c.PostForm("programme")),
		GraduationYear:       graduationYear,
		LinkedinURL:          strings.TrimSpace(c.PostForm("linkedinUrl")),
		AdditionalLinks:      normalizeLinks(c.PostFormArray("additionalLinks"), c.PostForm("additionalLinks")),
		Teams:                normalizeRepeatedValues(c.PostFormArray("teams")),
		Interests:            normalizeRepeatedValues(c.PostFormArray("interests")),
		Availability:         strings.TrimSpace(c.PostForm("availability")),
		Contribution:         strings.TrimSpace(c.PostForm("contribution")),
		DataRetentionConsent: strings.EqualFold(strings.TrimSpace(c.PostForm("dataRetentionConsent")), "true"),
		NewsletterOptIn:      strings.EqualFold(strings.TrimSpace(c.PostForm("newsletterOptIn")), "true"),
	}, nil
}

func validateGeneralApplicationInput(input generalApplicationInput) error {
	if len(input.FirstName) == 0 || len(input.FirstName) > 80 {
		return fmt.Errorf("first name is required and must be at most 80 characters")
	}
	if len(input.LastName) == 0 || len(input.LastName) > 80 {
		return fmt.Errorf("last name is required and must be at most 80 characters")
	}
	if !isValidEmail(input.Email) {
		return fmt.Errorf("valid email is required")
	}
	if _, ok := allowedApplicationGenders[input.Gender]; !ok {
		return fmt.Errorf("invalid gender")
	}
	if input.University == "" {
		return fmt.Errorf("university is required")
	}
	if input.Programme == "" {
		return fmt.Errorf("programme is required")
	}
	if input.GraduationYear < 2026 || input.GraduationYear > 2100 {
		return fmt.Errorf("graduation year must be 2026 or later")
	}
	if !isValidLinkedInURL(input.LinkedinURL) {
		return fmt.Errorf("valid LinkedIn URL is required")
	}
	if len(input.AdditionalLinks) > 5 {
		return fmt.Errorf("provide at most 5 additional links")
	}
	for _, link := range input.AdditionalLinks {
		if !isValidHTTPURL(link) {
			return fmt.Errorf("additional links must be valid URLs")
		}
	}
	if len(input.Teams) == 0 {
		return fmt.Errorf("choose at least one team")
	}
	if len(input.Teams) > len(allowedApplicationTeams) {
		return fmt.Errorf("choose at most five teams")
	}
	seenTeams := make(map[string]struct{}, len(input.Teams))
	for _, team := range input.Teams {
		if _, ok := allowedApplicationTeams[team]; !ok {
			return fmt.Errorf("invalid team")
		}
		if _, ok := seenTeams[team]; ok {
			return fmt.Errorf("each team can only be ranked once")
		}
		seenTeams[team] = struct{}{}
	}
	if err := validation.ValidateInterests(input.Interests); err != nil {
		return err
	}
	if _, ok := allowedApplicationAvailability[input.Availability]; !ok {
		return fmt.Errorf("invalid availability")
	}
	if len(input.Contribution) < 20 || len(input.Contribution) > 2000 {
		return fmt.Errorf("contribution must be between 20 and 2000 characters")
	}
	if !input.DataRetentionConsent {
		return fmt.Errorf("data retention consent is required")
	}
	return nil
}

func validateResumeFile(file *multipart.FileHeader) (string, error) {
	if file.Size <= 0 {
		return "", fmt.Errorf("resume is required")
	}
	if file.Size > generalApplicationMaxResume {
		return "", fmt.Errorf("resume must be at most %d MiB", generalApplicationMaxResumeMB)
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if _, ok := allowedResumeExtensions[ext]; !ok {
		return "", fmt.Errorf("resume must be a PDF, DOC, or DOCX file")
	}
	contentType := file.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = fallbackResumeContentType(ext)
	}
	if _, ok := allowedResumeContentTypes[contentType]; !ok {
		return "", fmt.Errorf("resume must be a PDF, DOC, or DOCX file")
	}
	return contentType, nil
}

func readUploadedFile(file *multipart.FileHeader) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeRepeatedValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func normalizeLinks(repeated []string, textarea string) []string {
	values := append([]string{}, repeated...)
	if textarea != "" {
		values = append(values, strings.FieldsFunc(textarea, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ','
		})...)
	}
	return normalizeRepeatedValues(values)
}

func isValidEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	parts := strings.Split(email, "@")
	return len(parts) == 2 && parts[0] != "" && strings.Contains(parts[1], ".")
}

func isValidLinkedInURL(raw string) bool {
	parsed, ok := parseHTTPURL(raw)
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(parsed.Hostname()), "linkedin.com")
}

func isValidHTTPURL(raw string) bool {
	_, ok := parseHTTPURL(raw)
	return ok
}

func parseHTTPURL(raw string) (*url.URL, bool) {
	raw = normalizeWebsiteLink(raw)
	if raw == "" {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, false
	}
	if !strings.Contains(parsed.Hostname(), ".") {
		return nil, false
	}
	return parsed, true
}

func normalizeWebsiteLink(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}

func normalizeWebsiteLinks(rawLinks []string) []string {
	links := make([]string, 0, len(rawLinks))
	for _, link := range rawLinks {
		normalized := normalizeWebsiteLink(link)
		if normalized != "" {
			links = append(links, normalized)
		}
	}
	return links
}

func fallbackResumeContentType(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return "application/octet-stream"
	}
}

func shouldStoreResumeInDatabase(cfg *config.Config) bool {
	return cfg.DevelopmentMode
}

// AdminClaimApplication locks an available application to the requesting admin.
// Returns 409 if the application is already claimed by another admin.
func (h *GeneralApplicationHandler) AdminClaimApplication(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	if application.Status == models.GeneralApplicationStatusInterviewing {
		if application.InterviewingByUserID != nil && *application.InterviewingByUserID == adminID {
			// Already claimed by this admin — idempotent success
			c.JSON(http.StatusOK, application)
			return
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":                "application is already claimed",
			"interviewing_by_email": application.InterviewingByEmail,
		})
		return
	}

	application.Status = models.GeneralApplicationStatusInterviewing
	application.InterviewingByUserID = &adminID
	application.InterviewingByEmail = adminEmail
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to claim application"})
		return
	}

	c.JSON(http.StatusOK, application)
}

// AdminReleaseApplication releases the claim on an application, records the admin
// in interviewed_by, and sets the status back to available.
func (h *GeneralApplicationHandler) AdminReleaseApplication(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	if application.InterviewingByUserID == nil || *application.InterviewingByUserID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not hold the claim on this application"})
		return
	}

	// Append to interviewed_by if not already present
	alreadyInterviewed := false
	for _, e := range application.InterviewedBy {
		if e == adminEmail {
			alreadyInterviewed = true
			break
		}
	}
	if !alreadyInterviewed {
		application.InterviewedBy = append(application.InterviewedBy, adminEmail)
	}

	application.Status = models.GeneralApplicationStatusAvailable
	application.InterviewingByUserID = nil
	application.InterviewingByEmail = ""
	log.Printf("[release] saving application %s with interviewed_by=%v", application.Id, []string(application.InterviewedBy))
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to release application"})
		return
	}

	// Re-read from DB to ensure the response reflects what was actually persisted.
	if err := h.db.First(&application, "id = ?", application.Id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload application"})
		return
	}
	log.Printf("[release] reloaded application %s, interviewed_by=%v", application.Id, []string(application.InterviewedBy))

	c.JSON(http.StatusOK, application)
}

// AdminSendInterviewInvite sends an interview invite email to the applicant using
// the requesting admin's stored booking page URL and email template.
func (h *GeneralApplicationHandler) AdminSendInterviewInvite(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	var profile models.Profile
	if err := h.db.Where("user_uuid = ?", adminID).First(&profile).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin profile not found; set up your interview settings first"})
		return
	}
	if profile.BookingPageURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "booking page URL not set; update your interview settings first"})
		return
	}
	if profile.InterviewEmailTemplate == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email template not set; update your interview settings first"})
		return
	}

	if h.cfg.DevelopmentMode {
		log.Printf("[dev] skipping SES — would have sent interview invite to %s (%s %s) for application %s",
			application.Email, application.FirstName, application.LastName, application.Id)
		c.JSON(http.StatusOK, gin.H{"message": "interview invite sent (dev mode — email not actually sent)"})
		return
	}

	if err := email.SendInterviewInvite(application, profile.InterviewEmailTemplate, profile.BookingPageURL); err != nil {
		log.Printf("failed to send interview invite for application %s: %v", application.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send interview invite email"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "interview invite sent"})
}

// AdminCancelInterview releases the claim without recording the admin in interviewed_by.
func (h *GeneralApplicationHandler) AdminCancelInterview(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	if application.InterviewingByUserID == nil || *application.InterviewingByUserID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not hold the claim on this application"})
		return
	}

	application.Status = models.GeneralApplicationStatusAvailable
	application.InterviewingByUserID = nil
	application.InterviewingByEmail = ""
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel interview"})
		return
	}

	c.JSON(http.StatusOK, application)
}

// AdminMarkIneligible sets an application's status to ineligible regardless of current state.
func (h *GeneralApplicationHandler) AdminMarkIneligible(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	application.Status = models.GeneralApplicationStatusIneligible
	application.InterviewingByUserID = nil
	application.InterviewingByEmail = ""
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark application as ineligible"})
		return
	}

	c.JSON(http.StatusOK, application)
}

// AdminRestoreApplication sets an ineligible application back to available.
func (h *GeneralApplicationHandler) AdminRestoreApplication(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	if application.Status != models.GeneralApplicationStatusIneligible {
		c.JSON(http.StatusBadRequest, gin.H{"error": "application is not ineligible"})
		return
	}

	application.Status = models.GeneralApplicationStatusAvailable
	if err := h.db.Save(&application).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore application"})
		return
	}

	c.JSON(http.StatusOK, application)
}

// AdminGetNotes returns the requesting admin's private notes on an application.
func (h *GeneralApplicationHandler) AdminGetNotes(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var notes []models.AdminInterviewNote
	if err := h.db.Where("application_id = ? AND admin_user_id = ?", id, adminID).Limit(1).Find(&notes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch notes"})
		return
	}
	if len(notes) == 0 {
		c.JSON(http.StatusOK, gin.H{"note": ""})
		return
	}

	c.JSON(http.StatusOK, gin.H{"note": notes[0].Note})
}

// AdminUpdateNotes upserts the requesting admin's private notes on an application.
func (h *GeneralApplicationHandler) AdminUpdateNotes(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var body struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	note := models.AdminInterviewNote{
		ApplicationID: id,
		AdminUserID:   adminID,
		Note:          body.Note,
	}
	result := h.db.
		Where(models.AdminInterviewNote{ApplicationID: id, AdminUserID: adminID}).
		Assign(models.AdminInterviewNote{Note: body.Note}).
		FirstOrCreate(&note)
	if result.Error != nil {
		// FirstOrCreate may race; fall back to upsert
		if err := h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "application_id"}, {Name: "admin_user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"note", "updated_at"}),
		}).Create(&note).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save notes"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"note": note.Note})
}
