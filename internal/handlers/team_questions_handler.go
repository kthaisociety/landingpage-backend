package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/email"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"backend/internal/validation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const teamQuestionsTokenValidity = 30 * 24 * time.Hour

// invalidLinkError is returned for any token lookup failure — unknown, expired,
// used, or superseded by a resend — so a stale link can't be used to probe
// which of those states it's actually in.
var invalidLinkError = gin.H{"error": "this link is invalid or has expired"}

type TeamQuestionsHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewTeamQuestionsHandler(db *gorm.DB, cfg *config.Config) *TeamQuestionsHandler {
	return &TeamQuestionsHandler{db: db, cfg: cfg}
}

func (h *TeamQuestionsHandler) Register(r *gin.RouterGroup) {
	applications := r.Group("/applications")
	applications.GET("/team-questions/:token", middleware.RateLimit(), h.GetForm)
	applications.POST("/team-questions/:token", middleware.RateLimit(), h.SubmitForm)

	admin := applications.Group("/admin")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.GET("/team-questions/send-bulk/preview", h.AdminSendBulkPreview)
	admin.POST("/team-questions/send-bulk", h.AdminSendBulk)
	admin.POST("/:id/team-questions/resend", h.AdminResend)
	admin.GET("/:id/team-questions", h.AdminGetSubmission)
	admin.GET("/team-questions/template", h.AdminGetTemplate)
	admin.PUT("/team-questions/template", h.AdminUpdateTemplate)
	admin.POST("/team-questions/template/preview", h.AdminPreviewTemplate)
	admin.GET("/team-questions/questions", h.AdminListTeamQuestions)
	admin.POST("/team-questions/questions", h.AdminCreateTeamQuestion)
	admin.PUT("/team-questions/questions/reorder", h.AdminReorderTeamQuestions)
	admin.PUT("/team-questions/questions/:questionId", h.AdminUpdateTeamQuestion)
	admin.DELETE("/team-questions/questions/:questionId", h.AdminDeleteTeamQuestion)
}

// lookupToken finds the token row for a raw token value. It only accepts a
// token that is unused, unexpired, and not superseded by a later resend for
// the same application (resending issues a fresh row, which retires every
// older row for that application even though those rows aren't deleted).
func (h *TeamQuestionsHandler) lookupToken(raw string) (*models.TeamQuestionsToken, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var token models.TeamQuestionsToken
	if err := h.db.Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", utils.HashToken(raw), time.Now()).
		First(&token).Error; err != nil {
		return nil, err
	}

	var newerCount int64
	if err := h.db.Model(&models.TeamQuestionsToken{}).
		Where("application_id = ? AND id > ?", token.ApplicationID, token.ID).
		Count(&newerCount).Error; err != nil {
		return nil, err
	}
	if newerCount > 0 {
		return nil, gorm.ErrRecordNotFound
	}

	return &token, nil
}

// scopedTeamQuestions loads the configured questions for exactly the given
// teams, in admin-defined display order, and nothing for any other team —
// the public form never receives another team's questions.
func (h *TeamQuestionsHandler) scopedTeamQuestions(teams []string) (map[string][]validation.TeamQuestion, error) {
	questions := make(map[string][]validation.TeamQuestion, len(teams))
	if len(teams) == 0 {
		return questions, nil
	}

	var rows []models.TeamQuestion
	if err := h.db.Where("team IN ?", teams).Order("sort_order, created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		questions[row.Team] = append(questions[row.Team], validation.TeamQuestion{
			ID:       row.Id.String(),
			Text:     row.Text,
			Required: row.Required,
		})
	}
	return questions, nil
}

func (h *TeamQuestionsHandler) GetForm(c *gin.Context) {
	token, err := h.lookupToken(c.Param("token"))
	if err != nil {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", token.ApplicationID).Error; err != nil {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}
	if application.Status != models.GeneralApplicationStatusPending {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}

	questions, err := h.scopedTeamQuestions(application.Teams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load questions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"first_name": application.FirstName,
		"teams":      application.Teams,
		"questions":  questions,
	})
}

func (h *TeamQuestionsHandler) SubmitForm(c *gin.Context) {
	token, err := h.lookupToken(c.Param("token"))
	if err != nil {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", token.ApplicationID).Error; err != nil {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}
	if application.Status != models.GeneralApplicationStatusPending {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}

	var body struct {
		Answers        map[string]map[string]string `json:"answers"`
		WithdrawnTeams []string                     `json:"withdrawn_teams"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	originalTeams := make(map[string]struct{}, len(application.Teams))
	for _, team := range application.Teams {
		originalTeams[team] = struct{}{}
	}
	withdrawn := make(map[string]struct{}, len(body.WithdrawnTeams))
	for _, team := range body.WithdrawnTeams {
		if _, ok := originalTeams[team]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "withdrawn_teams contains a team not on this application"})
			return
		}
		withdrawn[team] = struct{}{}
	}

	// Not nil: a nil slice serializes as SQL NULL via pq.StringArray, which
	// violates teams' NOT NULL constraint when every team is withdrawn.
	effectiveTeams := []string{}
	for _, team := range application.Teams {
		if _, ok := withdrawn[team]; !ok {
			effectiveTeams = append(effectiveTeams, team)
		}
	}

	for team := range body.Answers {
		if _, ok := withdrawn[team]; ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "answers include a team that was withdrawn"})
			return
		}
		if _, ok := originalTeams[team]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "answers include a team not on this application"})
			return
		}
	}

	questionsByTeam, err := h.scopedTeamQuestions(effectiveTeams)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load questions"})
		return
	}
	for _, team := range effectiveTeams {
		for _, question := range questionsByTeam[team] {
			if !question.Required {
				continue
			}
			if strings.TrimSpace(body.Answers[team][question.ID]) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("missing required answer for %s: %s", team, question.ID)})
				return
			}
		}
	}

	answersJSON, err := json.Marshal(body.Answers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode answers"})
		return
	}

	newStatus := models.GeneralApplicationStatusAvailable
	if len(effectiveTeams) == 0 {
		newStatus = models.GeneralApplicationStatusWithdrawn
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		submission := models.TeamQuestionsSubmission{
			ApplicationID:  application.Id,
			Answers:        string(answersJSON),
			WithdrawnTeams: pq.StringArray(body.WithdrawnTeams),
			SubmittedAt:    time.Now(),
		}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}

		application.Teams = pq.StringArray(effectiveTeams)
		application.Status = newStatus
		if err := tx.Save(&application).Error; err != nil {
			return err
		}

		now := time.Now()
		token.UsedAt = &now
		return tx.Save(token).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit team questions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": newStatus})
}

func (h *TeamQuestionsHandler) formURL(rawToken string) string {
	return fmt.Sprintf("%s/apply/team-questions/%s", strings.TrimSuffix(h.cfg.FrontendURL, "/"), rawToken)
}

// defaultTeamQuestionsEmailTemplate is a normal, ready-to-send default —
// nothing team-specific or per-admin about it, so there's no reason to make
// sending depend on an IT admin visiting the settings panel first. Matches
// the frontend's DEFAULT_TEAM_QUESTIONS_TEMPLATE constant.
const defaultTeamQuestionsEmailTemplate = "Thanks for applying to KTH AI Society! To move forward, we need you to answer a few extra questions about the team(s) you applied to.\n\nIt only takes a few minutes."

// getSettings always returns a usable, non-empty EmailTemplate: the saved one
// if an IT admin has set one, otherwise defaultTeamQuestionsEmailTemplate.
// Sending never has to block on "configure a template first", and the admin
// UI always displays exactly what would actually be sent.
func (h *TeamQuestionsHandler) getSettings() (models.TeamQuestionsSettings, error) {
	var settings models.TeamQuestionsSettings
	err := h.db.First(&settings).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return settings, err
	}
	if strings.TrimSpace(settings.EmailTemplate) == "" {
		settings.EmailTemplate = defaultTeamQuestionsEmailTemplate
	}
	return settings, nil
}

// AdminSendBulk emails the Team Questions invite to every pending application
// (application_year = current year, not ineligible/withdrawn) that has never
// been sent one before. Applications that already have a token — whether
// they've submitted or are still waiting — are untouched; use the per-
// application resend action for those.
// pendingUninvitedApplications finds every application that AdminSendBulk
// would email: pending, and never previously issued a Team Questions token.
// Shared with AdminSendBulkPreview so the preview count can never drift from
// what a send actually processes.
func (h *TeamQuestionsHandler) pendingUninvitedApplications() ([]models.GeneralApplication, error) {
	var applications []models.GeneralApplication
	// general_applications.id is a text column while team_questions_tokens.application_id
	// is a real uuid column, so the subquery side needs an explicit cast — Postgres won't
	// compare text to uuid without one.
	err := h.db.
		Where("application_year = ? AND status = ? AND id NOT IN (SELECT application_id::text FROM team_questions_tokens)", generalApplicationYear, models.GeneralApplicationStatusPending).
		Find(&applications).Error
	return applications, err
}

// AdminSendBulkPreview reports how many applications the next AdminSendBulk
// call would email, without sending anything — lets the admin UI confirm the
// count before committing to a bulk send.
func (h *TeamQuestionsHandler) AdminSendBulkPreview(c *gin.Context) {
	applications, err := h.pendingUninvitedApplications()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pending applications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(applications)})
}

func (h *TeamQuestionsHandler) AdminSendBulk(c *gin.Context) {
	settings, err := h.getSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load email template"})
		return
	}

	applications, err := h.pendingUninvitedApplications()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pending applications"})
		return
	}

	sent := 0
	failed := []string{}
	for _, application := range applications {
		if err := h.issueAndSend(application, settings.EmailTemplate); err != nil {
			log.Printf("failed to send team questions invite for application %s: %v", application.Id, err)
			failed = append(failed, application.Id.String())
			continue
		}
		sent++
	}

	c.JSON(http.StatusOK, gin.H{"sent": sent, "failed": failed})
}

// AdminResend issues a fresh token for a single application (retiring any
// older unused token for it) and re-sends the invite. Only valid while the
// application is still pending — once Team Questions has been submitted,
// marked ineligible, or withdrawn, there is nothing to resend.
func (h *TeamQuestionsHandler) AdminResend(c *gin.Context) {
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
	if application.Status != models.GeneralApplicationStatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "application is not awaiting team questions"})
		return
	}

	settings, err := h.getSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load email template"})
		return
	}

	if err := h.issueAndSend(application, settings.EmailTemplate); err != nil {
		log.Printf("failed to resend team questions invite for application %s: %v", application.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send team questions invite"})
		return
	}

	// Re-read so the response reflects team_questions_invite_sent_at, which
	// issueAndSend persisted on its own (unexported) copy of the struct.
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invite sent, but failed to reload application"})
		return
	}

	c.JSON(http.StatusOK, application)
}

func (h *TeamQuestionsHandler) issueAndSend(application models.GeneralApplication, templateText string) error {
	raw, hash, err := utils.GenerateToken()
	if err != nil {
		return err
	}

	token := models.TeamQuestionsToken{
		ApplicationID: application.Id,
		TokenHash:     hash,
		ExpiresAt:     time.Now().Add(teamQuestionsTokenValidity),
	}
	if err := h.db.Create(&token).Error; err != nil {
		return err
	}

	if !h.cfg.DevelopmentMode {
		if err := email.SendTeamQuestionsInvite(application, templateText, h.formURL(raw)); err != nil {
			return err
		}
	} else {
		log.Printf("[dev] skipping SES — would have sent team questions invite to %s (%s %s) for application %s",
			application.Email, application.FirstName, application.LastName, application.Id)
	}

	now := time.Now()
	application.TeamQuestionsInviteSentAt = &now
	return h.db.Save(&application).Error
}

func (h *TeamQuestionsHandler) AdminGetSubmission(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var submission models.TeamQuestionsSubmission
	if err := h.db.Where("application_id = ?", id).First(&submission).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"submitted": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load submission"})
		return
	}

	var answers map[string]map[string]string
	if err := json.Unmarshal([]byte(submission.Answers), &answers); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode submission"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"submitted":       true,
		"answers":         answers,
		"withdrawn_teams": submission.WithdrawnTeams,
		"submitted_at":    submission.SubmittedAt,
	})
}

func (h *TeamQuestionsHandler) AdminGetTemplate(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	settings, err := h.getSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load email template"})
		return
	}

	canEdit, err := requesterIsOnTeam(h.db, adminID, "IT")
	if err != nil {
		canEdit = false
	}

	c.JSON(http.StatusOK, gin.H{
		"email_template":   settings.EmailTemplate,
		"updated_by_email": settings.UpdatedByEmail,
		"can_edit":         canEdit,
	})
}

func (h *TeamQuestionsHandler) AdminUpdateTemplate(c *gin.Context) {
	adminID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	isIT, err := requesterIsOnTeam(h.db, adminID, "IT")
	if err != nil || !isIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only IT admins can edit the team questions email template"})
		return
	}

	var body struct {
		EmailTemplate string `json:"email_template"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	var settings models.TeamQuestionsSettings
	err = h.db.First(&settings).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load email template"})
		return
	}

	settings.EmailTemplate = strings.TrimSpace(body.EmailTemplate)
	settings.UpdatedByEmail = adminEmail

	if err == gorm.ErrRecordNotFound {
		err = h.db.Create(&settings).Error
	} else {
		err = h.db.Save(&settings).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save email template"})
		return
	}

	responseTemplate := settings.EmailTemplate
	if strings.TrimSpace(responseTemplate) == "" {
		responseTemplate = defaultTeamQuestionsEmailTemplate
	}
	c.JSON(http.StatusOK, gin.H{
		"email_template":   responseTemplate,
		"updated_by_email": settings.UpdatedByEmail,
		"can_edit":         true,
	})
}

// AdminPreviewTemplate renders the team questions invite email exactly as
// issueAndSend does, using a dummy name and an example link, so the preview
// can never drift from what actually sends.
func (h *TeamQuestionsHandler) AdminPreviewTemplate(c *gin.Context) {
	var body struct {
		EmailTemplate string `json:"email_template"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	subject, html, err := email.RenderTeamQuestionsInvite("Alex", "Jones", []string{"Development", "Research"}, body.EmailTemplate, h.formURL("example-token"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render preview"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subject": subject, "html": html})
}
