package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
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
	"gorm.io/gorm/clause"
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

var errTeamQuestionsClosed = errors.New("team questions submissions closed after September 8, 2026 (Europe/Stockholm)")
var errTeamQuestionsOrdinarySendEnded = errors.New("ordinary automatic team questions emails have ended")

type TeamQuestionsHandler struct {
	db            *gorm.DB
	cfg           *config.Config
	now           func() time.Time
	sendFinalCall func(models.GeneralApplication, string, string, string) error
}

func NewTeamQuestionsHandler(db *gorm.DB, cfg *config.Config) *TeamQuestionsHandler {
	return &TeamQuestionsHandler{db: db, cfg: cfg, now: time.Now, sendFinalCall: email.SendTeamQuestionsInvite}
}

func (h *TeamQuestionsHandler) rejectClosed(c *gin.Context) bool {
	if teamQuestionsClosed(h.now()) {
		c.JSON(http.StatusGone, gin.H{"error": errTeamQuestionsClosed.Error()})
		return true
	}
	return false
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
	return h.lookupTokenInDB(h.db, raw)
}

func (h *TeamQuestionsHandler) lookupTokenInDB(db *gorm.DB, raw string) (*models.TeamQuestionsToken, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var token models.TeamQuestionsToken
	if err := db.Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", utils.HashToken(raw), h.now()).
		First(&token).Error; err != nil {
		return nil, err
	}

	var newerCount int64
	if err := db.Model(&models.TeamQuestionsToken{}).
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
	if h.rejectClosed(c) {
		return
	}
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

	if h.rejectClosed(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"first_name": application.FirstName,
		"teams":      application.Teams,
		"questions":  questions,
	})
}

func (h *TeamQuestionsHandler) SubmitForm(c *gin.Context) {
	if h.rejectClosed(c) {
		return
	}
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
		// Serialize submission with final calls and manual resends. A sender
		// that gets the lock next must see this submission before emailing.
		var current models.GeneralApplication
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "id = ?", application.Id).Error; err != nil {
			return err
		}
		if teamQuestionsClosed(h.now()) {
			return errTeamQuestionsClosed
		}
		if current.Status != models.GeneralApplicationStatusPending || !slices.Equal(current.Teams, application.Teams) {
			return gorm.ErrRecordNotFound
		}
		// A final call may have superseded the link while this request was
		// being validated. Check it again using the locked transaction.
		var err error
		token, err = h.lookupTokenInDB(tx, c.Param("token"))
		if err != nil {
			return err
		}
		now := h.now()
		if teamQuestionsClosed(now) {
			return errTeamQuestionsClosed
		}
		submission := models.TeamQuestionsSubmission{
			ApplicationID:  application.Id,
			Answers:        string(answersJSON),
			WithdrawnTeams: pq.StringArray(body.WithdrawnTeams),
			SubmittedAt:    now,
		}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}

		application.Teams = pq.StringArray(effectiveTeams)
		application.Status = newStatus
		if err := tx.Model(&models.GeneralApplication{}).Where("id = ?", application.Id).
			Updates(map[string]interface{}{"teams": application.Teams, "status": newStatus}).Error; err != nil {
			return err
		}

		if err := tx.Model(token).Update("used_at", now).Error; err != nil {
			return err
		}
		if teamQuestionsClosed(h.now()) {
			return errTeamQuestionsClosed
		}
		return nil
	})
	if errors.Is(err, errTeamQuestionsClosed) {
		c.JSON(http.StatusGone, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, invalidLinkError)
		return
	}
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
		if teamQuestionsClosed(h.now()) {
			return
		}
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

const defaultTeamQuestionsFinalCallTemplate = "Final call — we still haven't received your answers to the team questions for your 2026 KTH AI Society application.\n\nPlease submit your answers by the end of September 8, 2026 (Europe/Stockholm). The form closes at 00:00 on September 9, and we cannot accept submissions after that.\n\nUse the button below for your new Team Questions link. It replaces any previous link."
const defaultTeamQuestionsFinalCallSubject = "FINAL CALL: {{first_name}}'s application for {{teams}}"

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

// pendingUninvitedApplications finds pending applications never previously
// issued a Team Questions token. Before the final-call window, this query
// is shared by the scheduler, admin bulk send, and its preview.
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
	if teamQuestionsClosed(h.now()) {
		c.JSON(http.StatusOK, gin.H{"count": 0, "can_send": false, "next_send_at": nil})
		return
	}
	canSend, err := requesterIsHeadOfTeam(h.db, adminID, "IT")
	if err != nil {
		canSend = false
	}

	var applications []models.GeneralApplication
	if teamQuestionsFinalCallWindow(h.now()) {
		applications, err = h.pendingApplicationsNeedingFinalCall()
	} else {
		applications, err = h.pendingUninvitedApplications()
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pending applications"})
		return
	}
	var nextSendAt *time.Time
	next := nextTeamQuestionsRun(h.now().In(teamQuestionsInviteTZ))
	if next.Before(teamQuestionsSubmissionCutoff) {
		nextSendAt = &next
	}
	c.JSON(http.StatusOK, gin.H{
		"count":        len(applications),
		"can_send":     canSend,
		"next_send_at": nextSendAt,
	})
}

// AdminSendBulk is restricted to the head of IT — bulk-emailing every
// pending applicant is disruptive enough (and hard to undo, since it stamps
// TeamQuestionsInviteSentAt) that it shouldn't be something any IT team
// member can trigger, unlike the per-team Team Questions CRUD endpoints
// which use the broader requesterIsOnTeam.
// During the final-call window, bulk send retries only unsent final calls.
func (h *TeamQuestionsHandler) AdminSendBulk(c *gin.Context) {
	if h.rejectClosed(c) {
		return
	}
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

	var sent int
	var failed []string
	if teamQuestionsFinalCallWindow(h.now()) {
		sent, failed, err = h.SendPendingFinalCalls()
	} else {
		sent, failed, err = h.SendPendingInvites()
	}
	// Report whatever was actually sent even if the window closed partway
	// through — an admin relies on these counts, and discarding them here
	// just because the deadline passed mid-send loses that information for
	// no benefit (the pre-send h.rejectClosed(c) above already refuses to
	// start a send that's already closed).
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
	if !h.now().Before(teamQuestionsFinalCallStart) {
		return 0, nil, nil
	}
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
		if err := h.issueAndSendOrdinary(application, settings.EmailTemplate, settings.EmailSubject, false, true); err != nil {
			if errors.Is(err, errTeamQuestionsClosed) || errors.Is(err, errTeamQuestionsOrdinarySendEnded) {
				break
			}
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
	cutoff := h.now().Add(-teamQuestionsReminderDelay)
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
	if !h.now().Before(teamQuestionsFinalCallStart) {
		return 0, nil, nil
	}
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
			if errors.Is(err, errTeamQuestionsClosed) || errors.Is(err, errTeamQuestionsOrdinarySendEnded) {
				break
			}
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
	return h.issueAndSendOrdinary(application, templateText, subjectTemplate, true, true)
}

// pendingApplicationsNeedingFinalCall includes never-invited applicants too.
// Status alone is insufficient: an admin can reset a submitted application
// to pending. Exclude every existing submission, including soft-deleted ones.
func (h *TeamQuestionsHandler) pendingApplicationsNeedingFinalCall() ([]models.GeneralApplication, error) {
	var applications []models.GeneralApplication
	err := h.db.Where("application_year = ? AND status = ? AND team_questions_final_call_sent_at IS NULL", generalApplicationYear, models.GeneralApplicationStatusPending).
		Where("NOT EXISTS (SELECT 1 FROM team_questions_submissions WHERE application_id::text = general_applications.id)").
		Find(&applications).Error
	return applications, err
}

func (h *TeamQuestionsHandler) SendPendingFinalCalls() (sent int, failed []string, err error) {
	if !teamQuestionsFinalCallWindow(h.now()) {
		return 0, nil, nil
	}
	// A development-mode skip is not a successful delivery. Leave the token
	// and final-call marker untouched so a later real send remains possible.
	if h.cfg.DevelopmentMode {
		log.Print("[dev] skipping team questions final calls")
		return 0, nil, nil
	}
	applications, err := h.pendingApplicationsNeedingFinalCall()
	if err != nil {
		return 0, nil, err
	}
	failed = []string{}
	for _, application := range applications {
		if !teamQuestionsFinalCallWindow(h.now()) {
			break
		}
		delivered, err := h.issueAndSendFinalCall(application.Id)
		if err != nil {
			log.Printf("failed to send team questions final call for application %s: %v", application.Id, err)
			failed = append(failed, application.Id.String())
			continue
		}
		if delivered {
			sent++
		}
	}
	return sent, failed, nil
}

func (h *TeamQuestionsHandler) issueAndSendFinalCall(applicationID uuid.UUID) (bool, error) {
	if h.cfg.DevelopmentMode || !teamQuestionsFinalCallWindow(h.now()) {
		return false, nil
	}
	delivered := false
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var application models.GeneralApplication
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&application, "id = ?", applicationID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !teamQuestionsFinalCallWindow(h.now()) || application.ApplicationYear != generalApplicationYear ||
			application.Status != models.GeneralApplicationStatusPending || application.TeamQuestionsFinalCallSentAt != nil {
			return nil
		}
		var submitted int64
		if err := tx.Unscoped().Model(&models.TeamQuestionsSubmission{}).Where("application_id = ?", applicationID).Count(&submitted).Error; err != nil {
			return err
		}
		if submitted > 0 {
			return nil
		}
		raw, hash, err := utils.GenerateToken()
		if err != nil {
			return err
		}
		token := models.TeamQuestionsToken{ApplicationID: applicationID, TokenHash: hash, ExpiresAt: teamQuestionsSubmissionCutoff}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if !teamQuestionsFinalCallWindow(h.now()) {
			return errTeamQuestionsClosed // Roll back the unsent token.
		}
		if err := h.sendFinalCall(application, defaultTeamQuestionsFinalCallTemplate, defaultTeamQuestionsFinalCallSubject, h.formURL(raw)); err != nil {
			return err
		}
		// Explicit SQL is intentional: ordinary GORM saves cannot update this
		// marker. Record accepted delivery even if the send finished after cutoff.
		// SES acceptance and this commit are not atomic; a crash between them
		// can still cause a duplicate on retry.
		result := tx.Exec("UPDATE general_applications SET team_questions_final_call_sent_at = ? WHERE id = ? AND team_questions_final_call_sent_at IS NULL", h.now(), applicationID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("final call delivered but its timestamp was not recorded")
		}
		delivered = true
		return nil
	})
	return delivered && err == nil, err
}

// AdminResend issues a fresh token for a single application (retiring any
// older unused token for it) and re-sends the invite. Only valid while the
// application is still pending — once Team Questions has been submitted,
// marked ineligible, or withdrawn, there is nothing to resend.
func (h *TeamQuestionsHandler) AdminResend(c *gin.Context) {
	if h.rejectClosed(c) {
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
		if errors.Is(err, errTeamQuestionsClosed) {
			c.JSON(http.StatusGone, gin.H{"error": err.Error()})
			return
		}
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
	return h.issueAndSendOrdinary(application, templateText, subjectTemplate, false, false)
}

func (h *TeamQuestionsHandler) ordinarySendWindowError(automatic bool) error {
	now := h.now()
	if teamQuestionsClosed(now) {
		return errTeamQuestionsClosed
	}
	if automatic && !now.Before(teamQuestionsFinalCallStart) {
		return errTeamQuestionsOrdinarySendEnded
	}
	return nil
}

// Keep manual resends available until closure. Automatic invites/reminders
// stop at the final-call boundary, including a batch already in progress.
func (h *TeamQuestionsHandler) issueAndSendOrdinary(application models.GeneralApplication, templateText, subjectTemplate string, reminder, automatic bool) error {
	if err := h.ordinarySendWindowError(automatic); err != nil {
		return err
	}
	return h.db.Transaction(func(tx *gorm.DB) error {
		var current models.GeneralApplication
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", application.Id).Error; err != nil {
			return err
		}
		if err := h.ordinarySendWindowError(automatic); err != nil {
			return err
		}
		if current.Status != models.GeneralApplicationStatusPending {
			return gorm.ErrRecordNotFound
		}
		raw, hash, err := utils.GenerateToken()
		if err != nil {
			return err
		}
		token := models.TeamQuestionsToken{
			ApplicationID: current.Id,
			TokenHash:     hash,
			ExpiresAt:     h.now().Add(teamQuestionsTokenValidity),
		}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if err := h.ordinarySendWindowError(automatic); err != nil {
			return err
		}
		sender := email.SendTeamQuestionsInvite
		column := "team_questions_invite_sent_at"
		if reminder {
			sender = email.SendTeamQuestionsReminder
			column = "team_questions_reminder_sent_at"
		}
		if !h.cfg.DevelopmentMode {
			if err := sender(current, templateText, subjectTemplate, h.formURL(raw)); err != nil {
				return err
			}
		} else {
			log.Printf("[dev] skipping SES — would have sent team questions email for application %s", current.Id)
		}
		return tx.Model(&models.GeneralApplication{}).Where("id = ?", current.Id).Update(column, h.now()).Error
	})
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
