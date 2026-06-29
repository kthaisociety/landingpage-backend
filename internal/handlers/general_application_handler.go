package handlers

import (
	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
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

var allowedApplicationGenders = map[string]struct{}{
	"Female":            {},
	"Male":              {},
	"Non-binary":        {},
	"Prefer not to say": {},
	"Other":             {},
}

var allowedApplicationStatuses = map[models.GeneralApplicationStatus]struct{}{
	models.GeneralApplicationStatusPending:  {},
	models.GeneralApplicationStatusReviewed: {},
	models.GeneralApplicationStatusAccepted: {},
	models.GeneralApplicationStatusRejected: {},
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
	db  *gorm.DB
	cfg *config.Config
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
	Availability         string
	Contribution         string
	DataRetentionConsent bool
}

func NewGeneralApplicationHandler(db *gorm.DB, cfg *config.Config) *GeneralApplicationHandler {
	return &GeneralApplicationHandler{db: db, cfg: cfg}
}

func (h *GeneralApplicationHandler) Register(r *gin.RouterGroup) {
	applications := r.Group("/applications")
	applications.POST("/general", middleware.RateLimit(), h.Create)

	admin := applications.Group("/admin")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.GET("", h.AdminList)
	admin.PATCH("/:id/status", h.AdminUpdateStatus)
	admin.GET("/:id/resume", h.AdminDownloadResume)
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
			Availability:          strings.TrimSpace(input.Availability),
			Contribution:          strings.TrimSpace(input.Contribution),
			DataRetentionConsent:  input.DataRetentionConsent,
			Status:                models.GeneralApplicationStatusPending,
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
		Availability:         strings.TrimSpace(c.PostForm("availability")),
		Contribution:         strings.TrimSpace(c.PostForm("contribution")),
		DataRetentionConsent: strings.EqualFold(strings.TrimSpace(c.PostForm("dataRetentionConsent")), "true"),
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
	if len(input.Teams) != len(allowedApplicationTeams) {
		return fmt.Errorf("rank all five teams")
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
