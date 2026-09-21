package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ManualOnboardingHandler owns the admin-facing /admin/onboarding/* routes:
// starting a manual onboarding (outside the recruitment pipeline entirely —
// board appointments, special cases; no GeneralApplication is created or
// required — see notifyOnboardingService's doc comment and
// onboarding-service-plan.md for why an empty applicationID is the honest
// representation of "this person didn't come through the application
// pipeline," rather than fabricating a placeholder id or a new table), and
// proxying admin visibility/recovery actions straight through to
// onboarding-service (ListRecords, CancelOnboarding, RestartOnboarding) —
// this backend never persists a copy of onboarding state, onboarding-service
// stays the source of truth for all of it.
type ManualOnboardingHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewManualOnboardingHandler(db *gorm.DB, cfg *config.Config) *ManualOnboardingHandler {
	return &ManualOnboardingHandler{db: db, cfg: cfg}
}

func (h *ManualOnboardingHandler) Register(r *gin.RouterGroup) {
	admin := r.Group("/admin/onboarding")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("/manual", h.Create)
	admin.GET("/records", h.ListRecords)
	admin.POST("/retry", h.RetryOnboarding)
	admin.POST("/cancel", h.CancelOnboarding)
	admin.POST("/restart", h.RestartOnboarding)
	admin.POST("/delete-record", h.DeleteOnboardingRecord)
	admin.GET("/email-settings", h.GetEmailSettings)
	admin.PUT("/email-settings", h.UpdateEmailSettings)
	admin.POST("/email-settings/preview", h.PreviewEmailSettings)
	admin.GET("/contract-template", h.GetContractTemplateMeta)
	admin.POST("/contract-template", h.UploadContractTemplate)
}

type manualOnboardingRequest struct {
	FirstName    string `json:"first_name" binding:"required"`
	LastName     string `json:"last_name" binding:"required"`
	Email        string `json:"email" binding:"required"`
	AssignedTeam string `json:"assigned_team" binding:"required"`
}

// Create fires the same onboarding-service notify call the recruitment
// pipeline uses, with an empty applicationID — see
// notifyOnboardingService. Fire-and-forget, matching that same pattern:
// this endpoint always responds success once the request is valid,
// regardless of whether onboarding-service itself is reachable.
func (h *ManualOnboardingHandler) Create(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req manualOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "first_name, last_name, email, and assigned_team are required"})
		return
	}

	// Same normalization/bounds as validateGeneralApplicationInput
	// (general_application_handler.go) — this is the same "a name and an
	// email address" shape, just arriving from an admin form instead of the
	// public application form.
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Email = strings.TrimSpace(req.Email)
	if len(req.FirstName) == 0 || len(req.FirstName) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "first name is required and must be at most 80 characters"})
		return
	}
	if len(req.LastName) == 0 || len(req.LastName) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "last name is required and must be at most 80 characters"})
		return
	}
	if !isValidEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email is required"})
		return
	}
	if _, ok := allowedApplicationTeams[req.AssignedTeam]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid assigned_team"})
		return
	}

	log.Printf("manual onboarding: %s %s <%s> (%s) initiated by %s", req.FirstName, req.LastName, req.Email, req.AssignedTeam, adminEmail)
	go notifyOnboardingService(h.cfg, "", req.FirstName, req.LastName, req.Email, req.AssignedTeam)

	c.JSON(http.StatusOK, gin.H{"status": "notified"})
}

// callOnboardingService builds and executes a secret-authenticated request
// to onboarding-service, returning the raw response status/body for
// pass-through. A free function (not a method) since more than one handler
// now proxies to onboarding-service — see OffboardingHandler. Callers are
// responsible for their own OnboardingServiceURL-unset handling: a read
// (ListRecords) can reasonably fall back to an empty result, but a
// mutating action failing loudly is correct, not a silent no-op.
func callOnboardingService(cfg *config.Config, method, path string, body []byte) (status int, respBody []byte, err error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, cfg.OnboardingServiceURL+path, reqBody)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Service-Secret", cfg.OnboardingServiceSecret)

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// ListRecords proxies onboarding-service's own /internal/onboarding/records
// straight through. Returns an empty list rather than an error when
// OnboardingServiceURL isn't configured, matching how notifyOnboardingService
// treats that same case as a silent no-op rather than a failure.
func (h *ManualOnboardingHandler) ListRecords(c *gin.Context) {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusOK, []any{})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodGet, "/internal/onboarding/records", nil)
	if err != nil {
		log.Printf("onboarding records: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

type onboardingRecordActionRequest struct {
	ID uint `json:"id" binding:"required"`
}

// RetryOnboarding proxies to onboarding-service's own
// /retry-provisioning — see that service's RecordActionsHandler.Retry for
// what it actually does (re-runs provisioning from wherever it left off;
// only valid for a record in kth_email_confirmed or failed).
func (h *ManualOnboardingHandler) RetryOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/retry-provisioning")
}

// CancelOnboarding proxies to onboarding-service's own /cancel — see that
// service's RecordActionsHandler.Cancel for what it actually does. Unlike
// ListRecords, an unconfigured OnboardingServiceURL is a real error here:
// this is a mutating action, so failing loudly is correct, not a silent
// no-op.
func (h *ManualOnboardingHandler) CancelOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/cancel")
}

// RestartOnboarding proxies to onboarding-service's own /restart — see
// that service's RecordActionsHandler.Restart.
func (h *ManualOnboardingHandler) RestartOnboarding(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/restart")
}

// DeleteOnboardingRecord proxies to onboarding-service's own
// /delete-record — see that service's RecordActionsHandler.Delete. Only
// removes onboarding-service's own tracking row; a real Google
// Workspace/Mattermost account, if one was ever created, is untouched (see
// OffboardingHandler for that).
func (h *ManualOnboardingHandler) DeleteOnboardingRecord(c *gin.Context) {
	h.proxyRecordAction(c, "/internal/onboarding/delete-record")
}

func (h *ManualOnboardingHandler) proxyRecordAction(c *gin.Context, path string) {
	var req onboardingRecordActionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	payload, err := json.Marshal(map[string]uint{"id": req.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodPost, path, payload)
	if err != nil {
		log.Printf("onboarding %s: %v", path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

// GetEmailSettings proxies onboarding-service's own
// /internal/onboarding/email-settings straight through — see that service's
// EmailSettingsHandler for why the admin-editable intro text lives there,
// not here.
func (h *ManualOnboardingHandler) GetEmailSettings(c *gin.Context) {
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodGet, "/internal/onboarding/email-settings", nil)
	if err != nil {
		log.Printf("onboarding email-settings: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

type onboardingEmailSettingsRequest struct {
	StartIntroText      string `json:"start_intro_text"`
	ConfirmIntroText    string `json:"confirm_intro_text"`
	AccountIntroText    string `json:"account_intro_text"`
	MattermostIntroText string `json:"mattermost_intro_text"`
	ContractIntroText   string `json:"contract_intro_text"`
	BylawsURL           string `json:"bylaws_url"`
	LumaKickoffURL      string `json:"luma_kickoff_url"`
}

// UpdateEmailSettings proxies to onboarding-service's own PUT
// /internal/onboarding/email-settings, attaching the admin's identity so
// onboarding-service (which has no notion of admin identity itself) can
// record who last changed it.
func (h *ManualOnboardingHandler) UpdateEmailSettings(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req onboardingEmailSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	payload, err := json.Marshal(map[string]string{
		"start_intro_text":      req.StartIntroText,
		"confirm_intro_text":    req.ConfirmIntroText,
		"account_intro_text":    req.AccountIntroText,
		"mattermost_intro_text": req.MattermostIntroText,
		"contract_intro_text":   req.ContractIntroText,
		"bylaws_url":            req.BylawsURL,
		"luma_kickoff_url":      req.LumaKickoffURL,
		"updated_by_email":      adminEmail,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodPut, "/internal/onboarding/email-settings", payload)
	if err != nil {
		log.Printf("onboarding email-settings update: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

// previewSampleStartButtonURL/previewSampleConfirmButtonURL are placeholder
// links, matching how the interview invite preview substitutes a fake
// booking URL when none is set yet — never a real link the admin panel
// would let anyone click through to. Both the start and confirm emails'
// real buttons are per-record portal token URLs that don't exist yet for a
// preview; the account and Mattermost emails' buttons are fixed values, so
// onboarding-service's preview response already carries the real thing —
// see PreviewEmailSettings below.
const previewSampleStartButtonURL = "https://kthais.com/onboarding/start"
const previewSampleConfirmButtonURL = "https://kthais.com/onboarding/confirm"
const previewSampleContractButtonURL = "https://kthais.com/onboarding/contract"

type previewOnboardingEmailRequest struct {
	Kind      string `json:"kind"`
	IntroText string `json:"intro_text"`
}

// PreviewEmailSettings renders one of the five onboarding emails exactly
// as it would be sent for the given (possibly unsaved) intro text: it asks
// onboarding-service to build the same subject/body a real send would (see
// that service's EmailSettingsHandler.Preview), then wraps it in this
// backend's own HTML template via the same RenderOnboardingEmail used when
// actually sending, so the preview can never drift from the real email.
func (h *ManualOnboardingHandler) PreviewEmailSettings(c *gin.Context) {
	var req previewOnboardingEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Kind != "start" && req.Kind != "confirm" && req.Kind != "account" && req.Kind != "mattermost" && req.Kind != "contract" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be one of: start, confirm, account, mattermost, contract"})
		return
	}
	if h.cfg.OnboardingServiceURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "onboarding service is not configured"})
		return
	}

	payload, err := json.Marshal(map[string]string{"kind": req.Kind, "intro_text": req.IntroText})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	status, body, err := callOnboardingService(h.cfg, http.MethodPost, "/internal/onboarding/email-settings/preview", payload)
	if err != nil {
		log.Printf("onboarding email-settings preview: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is unreachable"})
		return
	}
	if status != http.StatusOK {
		c.Data(status, "application/json; charset=utf-8", body)
		return
	}

	var rendered struct {
		Subject    string `json:"subject"`
		Body       string `json:"body"`
		ButtonURL  string `json:"button_url"`
		ButtonText string `json:"button_text"`
	}
	// json.Unmarshal of a bare `null` body succeeds and leaves rendered
	// zero-valued — checked for explicitly (rather than just handling the
	// unmarshal error) so a malformed-but-syntactically-valid upstream
	// response can never look like a real, blank preview to an admin.
	if err := json.Unmarshal(body, &rendered); err != nil || rendered.Subject == "" || rendered.Body == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service returned an invalid response"})
		return
	}

	// account and mattermost have a fixed button — onboarding-service's
	// response above already carries the real URL/text, used as-is. start
	// and confirm both have a per-record portal token URL that doesn't
	// exist for a preview, so only their button text is kept (falling back
	// to a local default if talking to an older onboarding-service that
	// predates it returning button_text at all — deploy ordering/rollback
	// should never make the preview silently degrade to "Contact us") and
	// the URL is swapped for a local placeholder.
	buttonURL, buttonText := rendered.ButtonURL, rendered.ButtonText
	switch req.Kind {
	case "start":
		buttonURL = previewSampleStartButtonURL
		if buttonText == "" {
			buttonText = "Start onboarding"
		}
	case "confirm":
		buttonURL = previewSampleConfirmButtonURL
		if buttonText == "" {
			buttonText = "Continue to confirm"
		}
	case "contract":
		buttonURL = previewSampleContractButtonURL
		if buttonText == "" {
			buttonText = "View your contract"
		}
	}

	html, err := email.RenderOnboardingEmail(rendered.Subject, rendered.Body, buttonURL, buttonText)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render preview"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subject": rendered.Subject, "html": html})
}

// contractTemplateResponse mirrors what an admin needs to know about the
// currently uploaded membership-contract file, without the bytes
// themselves — Uploaded is false (and every other field blank) before an
// admin has ever uploaded one.
func contractTemplateResponse(t models.OnboardingContractTemplate) gin.H {
	if t.ID == 0 {
		return gin.H{"uploaded": false}
	}
	return gin.H{
		"uploaded":         true,
		"file_name":        t.FileName,
		"content_type":     t.ContentType,
		"updated_by_email": t.UpdatedByEmail,
		"updated_at":       t.UpdatedAt,
	}
}

// GetContractTemplateMeta lets the admin panel show which contract file is
// currently live, without downloading its bytes.
func (h *ManualOnboardingHandler) GetContractTemplateMeta(c *gin.Context) {
	template, err := models.LoadOnboardingContractTemplate(h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	c.JSON(http.StatusOK, contractTemplateResponse(template))
}

// maxContractTemplateSize bounds the upload — a signed-document template is
// never expected to be more than a few pages.
const maxContractTemplateSize = 10 << 20 // 10MB

// UploadContractTemplate replaces the membership-contract file onboarding-
// service's contract email links members to (see OnboardingHandler.
// GetContractTemplate). Same dual-storage split as GeneralApplication's
// resume upload: DevelopmentMode keeps the bytes in Postgres so local dev
// never needs real R2 credentials, everywhere else they go to R2 via
// BlobData. Any previous file (in either storage) is best-effort deleted
// once the new one is safely stored, so this never leaves an orphaned R2
// object or Postgres blob behind after repeated uploads.
func (h *ManualOnboardingHandler) UploadContractTemplate(c *gin.Context) {
	_, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	if fileHeader.Size > maxContractTemplateSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file must be at most 10MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	filename := filepath.Base(fileHeader.Filename)

	template, err := models.LoadOnboardingContractTemplate(h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	previousBlobID := template.BlobID

	template.FileName = filename
	template.ContentType = contentType
	template.UpdatedByEmail = adminEmail

	var r2 utils.R2Client
	var r2Initialized bool
	if shouldStoreResumeInDatabase(h.cfg) {
		template.Data = data
		template.BlobID = nil
	} else {
		r2, err = utils.InitS3SDK(h.cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize storage"})
			return
		}
		r2Initialized = true

		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
		name := strings.TrimSuffix(filename, filepath.Ext(filename))
		blob, err := models.NewBlobData(name, ext, uuid.Nil, data, h.db, r2)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store file"})
			return
		}
		template.BlobID = &blob.BlobId
		template.Data = nil
	}

	if template.ID == 0 {
		err = h.db.Create(&template).Error
	} else {
		err = h.db.Save(&template).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save contract template"})
		return
	}

	if previousBlobID != nil {
		if !r2Initialized {
			r2, err = utils.InitS3SDK(h.cfg)
			r2Initialized = err == nil
		}
		if r2Initialized {
			if err := r2.DeleteObject(previousBlobID.String()); err != nil {
				log.Printf("onboarding contract-template: failed to delete superseded blob %s: %v", previousBlobID, err)
			}
		}
		if err := h.db.Where("blob_id = ?", previousBlobID).Delete(&models.BlobData{}).Error; err != nil {
			log.Printf("onboarding contract-template: failed to delete superseded blob row %s: %v", previousBlobID, err)
		}
	}

	log.Printf("onboarding contract-template: %q uploaded by %s (%d bytes)", filename, adminEmail, len(data))
	c.JSON(http.StatusOK, contractTemplateResponse(template))
}
