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

// teamQuestionsReminderDelay is how long an applicant has to submit Team
// Questions before the daily scheduler sends them a one-time reminder. Only
// ever fires once per application — see TeamQuestionsReminderSentAt.
const teamQuestionsReminderDelay = 7 * 24 * time.Hour

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

	teamAnswers := make([]email.TeamQuestionsTeamAnswers, 0, len(effectiveTeams))
	for _, team := range effectiveTeams {
		answers := make([]email.TeamQuestionsAnswer, 0, len(questionsByTeam[team]))
		for _, question := range questionsByTeam[team] {
			answers = append(answers, email.TeamQuestionsAnswer{
				Question: question.Text,
				Answer:   body.Answers[team][question.ID],
			})
		}
		teamAnswers = append(teamAnswers, email.TeamQuestionsTeamAnswers{
			Team:    team,
			Answers: answers,
		})
	}

	go func(application models.GeneralApplication, teamAnswers []email.TeamQuestionsTeamAnswers, withdrawnTeams []string) {
		if err := email.SendTeamQuestionsConfirmation(application, teamAnswers, withdrawnTeams); err != nil {
			log.Printf("failed to send team questions confirmation email for %s: %v", application.Id, err)
		}
	}(application, teamAnswers, body.WithdrawnTeams)

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

// defaultTeamQuestionsEmailSubject may contain {{first_name}} and {{teams}}
// placeholders — see email.RenderTeamQuestionsInvite.
const defaultTeamQuestionsEmailSubject = "{{first_name}}'s application for {{teams}}"

// defaultTeamQuestionsReminderTemplate is the fallback reminder body, used
// the same way defaultTeamQuestionsEmailTemplate is for the initial invite.
const defaultTeamQuestionsReminderTemplate = "Just a reminder — we still haven't received your answers to the team questions for your KTH AI Society application. Please complete them so we can move your application forward.\n\nYour previous link has expired; use the button below instead."

// defaultTeamQuestionsReminderSubject is the fallback reminder subject, used
// the same way defaultTeamQuestionsEmailSubject is for the initial invite.
const defaultTeamQuestionsReminderSubject = "Reminder: {{first_name}}'s application for {{teams}}"

// getSettings always returns usable, non-empty templates and subjects: the
// saved ones if an IT admin has set them, otherwise the defaults above.
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
	if strings.TrimSpace(settings.EmailSubject) == "" {
		settings.EmailSubject = defaultTeamQuestionsEmailSubject
	}
	if strings.TrimSpace(settings.ReminderEmailTemplate) == "" {
		settings.ReminderEmailTemplate = defaultTeamQuestionsReminderTemplate
	}
	if strings.TrimSpace(settings.ReminderEmailSubject) == "" {
		settings.ReminderEmailSubject = defaultTeamQuestionsReminderSubject
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
// count before committing to a bulk send. Also reports whether the requester
// is allowed to actually send (only the head of IT — see AdminSendBulk) and
// when the daily scheduler will next send on its own, so the UI can show
// both without a separate round trip.
func (h *TeamQuestionsHandler) AdminSendBulkPreview(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}
	canSend, err := requesterIsHeadOfTeam(h.db, adminID, "IT")
	if err != nil {
		canSend = false
	}

	applications, err := h.pendingUninvitedApplications()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pending applications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count":        len(applications),
		"can_send":     canSend,
		"next_send_at": nextTeamQuestionsRun(time.Now().In(teamQuestionsInviteTZ)),
	})
}

// AdminSendBulk is restricted to the head of IT — bulk-emailing every
// pending applicant is disruptive enough (and hard to undo, since it stamps
// TeamQuestionsInviteSentAt) that it shouldn't be something any IT team
// member can trigger, unlike the per-team Team Questions CRUD endpoints
// which use the broader requesterIsOnTeam.
func (h *TeamQuestionsHandler) AdminSendBulk(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}
	isHead, err := requesterIsHeadOfTeam(h.db, adminID, "IT")
	if err != nil || !isHead {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the head of IT can send team questions invites in bulk"})
		return
	}

	sent, failed, err := h.SendPendingInvites()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send team questions invites"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sent": sent, "failed": failed})
}

// SendPendingInvites emails the Team Questions invite to every pending,
// never-invited application (see pendingUninvitedApplications). Shared by the
// admin bulk-send endpoint and the daily scheduler so both go through the
// exact same send path.
func (h *TeamQuestionsHandler) SendPendingInvites() (sent int, failed []string, err error) {
	applications, err := h.pendingUninvitedApplications()
	if err != nil {
		return 0, nil, err
	}
	if len(applications) == 0 {
		return 0, nil, nil
	}

	settings, err := h.getSettings()
	if err != nil {
		return 0, nil, err
	}

	failed = []string{}
	for _, application := range applications {
		if err := h.issueAndSend(application, settings.EmailTemplate, settings.EmailSubject); err != nil {
			log.Printf("failed to send team questions invite for application %s: %v", application.Id, err)
			failed = append(failed, application.Id.String())
			continue
		}
		sent++
	}

	return sent, failed, nil
}

// pendingApplicationsNeedingReminder finds applications that were invited to
// Team Questions at least teamQuestionsReminderDelay ago, are still pending
// (haven't submitted, been marked ineligible, or withdrawn), and have never
// been sent a reminder — so this only ever fires once per application, no
// matter how many days go by after the 7-day mark.
func (h *TeamQuestionsHandler) pendingApplicationsNeedingReminder() ([]models.GeneralApplication, error) {
	var applications []models.GeneralApplication
	cutoff := time.Now().Add(-teamQuestionsReminderDelay)
	err := h.db.
		Where("application_year = ? AND status = ? AND team_questions_invite_sent_at IS NOT NULL AND team_questions_invite_sent_at <= ? AND team_questions_reminder_sent_at IS NULL",
			generalApplicationYear, models.GeneralApplicationStatusPending, cutoff).
		Find(&applications).Error
	return applications, err
}

// SendPendingReminders emails the 7-day reminder to every application that
// pendingApplicationsNeedingReminder finds. Called by the daily scheduler
// alongside SendPendingInvites.
func (h *TeamQuestionsHandler) SendPendingReminders() (sent int, failed []string, err error) {
	applications, err := h.pendingApplicationsNeedingReminder()
	if err != nil {
		return 0, nil, err
	}
	if len(applications) == 0 {
		return 0, nil, nil
	}

	settings, err := h.getSettings()
	if err != nil {
		return 0, nil, err
	}

	failed = []string{}
	for _, application := range applications {
		if err := h.issueAndSendReminder(application, settings.ReminderEmailTemplate, settings.ReminderEmailSubject); err != nil {
			log.Printf("failed to send team questions reminder for application %s: %v", application.Id, err)
			failed = append(failed, application.Id.String())
			continue
		}
		sent++
	}

	return sent, failed, nil
}

// issueAndSendReminder mints a fresh token — the original invite's raw token
// was never persisted, only its hash, so a reminder can't reuse the same
// link — and sends the reminder email. Stamps TeamQuestionsReminderSentAt so
// this application is never picked up by pendingApplicationsNeedingReminder
// again, regardless of whether the applicant ever responds.
func (h *TeamQuestionsHandler) issueAndSendReminder(application models.GeneralApplication, templateText, subjectTemplate string) error {
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
		if err := email.SendTeamQuestionsReminder(application, templateText, subjectTemplate, h.formURL(raw)); err != nil {
			return err
		}
	} else {
		log.Printf("[dev] skipping SES — would have sent team questions reminder to %s (%s %s) for application %s",
			application.Email, application.FirstName, application.LastName, application.Id)
	}

	now := time.Now()
	application.TeamQuestionsReminderSentAt = &now
	return h.db.Save(&application).Error
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

	if err := h.issueAndSend(application, settings.EmailTemplate, settings.EmailSubject); err != nil {
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

func (h *TeamQuestionsHandler) issueAndSend(application models.GeneralApplication, templateText, subjectTemplate string) error {
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
		if err := email.SendTeamQuestionsInvite(application, templateText, subjectTemplate, h.formURL(raw)); err != nil {
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
		"email_template":          settings.EmailTemplate,
		"email_subject":           settings.EmailSubject,
		"reminder_email_template": settings.ReminderEmailTemplate,
		"reminder_email_subject":  settings.ReminderEmailSubject,
		"updated_by_email":        settings.UpdatedByEmail,
		"can_edit":                canEdit,
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
		EmailTemplate         string `json:"email_template"`
		EmailSubject          string `json:"email_subject"`
		ReminderEmailTemplate string `json:"reminder_email_template"`
		ReminderEmailSubject  string `json:"reminder_email_subject"`
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
	settings.EmailSubject = strings.TrimSpace(body.EmailSubject)
	settings.ReminderEmailTemplate = strings.TrimSpace(body.ReminderEmailTemplate)
	settings.ReminderEmailSubject = strings.TrimSpace(body.ReminderEmailSubject)
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
	responseSubject := settings.EmailSubject
	if strings.TrimSpace(responseSubject) == "" {
		responseSubject = defaultTeamQuestionsEmailSubject
	}
	responseReminderTemplate := settings.ReminderEmailTemplate
	if strings.TrimSpace(responseReminderTemplate) == "" {
		responseReminderTemplate = defaultTeamQuestionsReminderTemplate
	}
	responseReminderSubject := settings.ReminderEmailSubject
	if strings.TrimSpace(responseReminderSubject) == "" {
		responseReminderSubject = defaultTeamQuestionsReminderSubject
	}
	c.JSON(http.StatusOK, gin.H{
		"email_template":          responseTemplate,
		"email_subject":           responseSubject,
		"reminder_email_template": responseReminderTemplate,
		"reminder_email_subject":  responseReminderSubject,
		"updated_by_email":        settings.UpdatedByEmail,
		"can_edit":                true,
	})
}

// AdminPreviewTemplate renders the team questions invite or reminder email
// exactly as issueAndSend/issueAndSendReminder do, using a dummy name and an
// example link, so the preview can never drift from what actually sends.
// Kind selects which one: "reminder", or anything else (including omitted)
// for the invite.
func (h *TeamQuestionsHandler) AdminPreviewTemplate(c *gin.Context) {
	var body struct {
		EmailTemplate string `json:"email_template"`
		EmailSubject  string `json:"email_subject"`
		Kind          string `json:"kind"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	render := email.RenderTeamQuestionsInvite
	subjectTemplate := body.EmailSubject
	if strings.TrimSpace(subjectTemplate) == "" {
		subjectTemplate = defaultTeamQuestionsEmailSubject
	}
	if body.Kind == "reminder" {
		render = email.RenderTeamQuestionsReminder
		if strings.TrimSpace(body.EmailSubject) == "" {
			subjectTemplate = defaultTeamQuestionsReminderSubject
		}
	}

	subject, html, err := render("Alex", "Jones", []string{"Development", "Research"}, body.EmailTemplate, subjectTemplate, h.formURL("example-token"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render preview"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subject": subject, "html": html})
}
