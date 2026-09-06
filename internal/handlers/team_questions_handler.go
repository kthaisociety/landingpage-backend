package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
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

// errTeamQuestionsTokenSuperseded means a token committed by this call was
// no longer safe to deliver by the time its lock-free pre-dispatch check
// ran: a concurrent resend/reminder/final-call minted a newer token for the
// same application (this one's link would already be dead on arrival — see
// lookupTokenInDB), or the application left pending by some other path.
// Whatever superseded it is expected to cover the applicant instead.
var errTeamQuestionsTokenSuperseded = errors.New("a concurrent update superseded this send")

// errTeamQuestionsDeliveryNotRecorded wraps a failure to persist
// team_questions_{invite,reminder,final_call}_sent_at after the provider
// already accepted the message. Callers must never treat this the same as
// an ordinary send failure: the email is out, so counting it as failed (and
// inviting a resend) risks double-emailing the applicant. It's surfaced
// loudly instead, for manual reconciliation.
var errTeamQuestionsDeliveryNotRecorded = errors.New("team questions email was delivered but its delivery could not be recorded")

type TeamQuestionsHandler struct {
	db               *gorm.DB
	cfg              *config.Config
	now              func() time.Time
	sendFinalCall    func(models.GeneralApplication, string, string, string) error
	sendInvite       func(models.GeneralApplication, string, string, string) error
	sendReminder     func(models.GeneralApplication, string, string, string) error
	sendConfirmation func(models.GeneralApplication, []email.TeamQuestionsTeamAnswers, []string) error

	// finalCallStartOverride and submissionCutoffOverride cache
	// TeamQuestionsSettings.FinalCallStart/SubmissionCutoff so the deadline
	// guards used on the public GetForm/SubmitForm hot path (teamQuestionsClosed,
	// teamQuestionsFinalCallWindow) never need a DB read. nil means "not
	// configured" — effectiveFinalCallStart/effectiveSubmissionCutoff fall
	// back to the hardcoded defaults. The cache is refreshed by getSettings(),
	// called from most admin endpoints, once at scheduler boot, and once per
	// scheduler loop iteration (see StartDailyTeamQuestionsScheduler) — so an
	// admin-saved override reaches every consumer within this same process
	// immediately (cmd/api/main.go constructs exactly one TeamQuestionsHandler
	// and shares it between route registration and the scheduler — a second
	// instance would keep its own permanently-stale copy). Across separate
	// processes (multiple replicas), a saved override still only reaches each
	// other process on its own next getSettings() call; that cross-process
	// staleness window is accepted rather than solved with a ticker or
	// pub-sub, to keep this change small.
	finalCallStartOverride   atomic.Pointer[time.Time]
	submissionCutoffOverride atomic.Pointer[time.Time]
}

func NewTeamQuestionsHandler(db *gorm.DB, cfg *config.Config) *TeamQuestionsHandler {
	return &TeamQuestionsHandler{
		db:               db,
		cfg:              cfg,
		now:              time.Now,
		sendFinalCall:    email.SendTeamQuestionsInvite,
		sendInvite:       email.SendTeamQuestionsInvite,
		sendReminder:     email.SendTeamQuestionsReminder,
		sendConfirmation: email.SendTeamQuestionsConfirmation,
	}
}

// effectiveFinalCallStart and effectiveSubmissionCutoff are the cache reads
// backing every deadline guard in this file — see the cache fields' comment
// on TeamQuestionsHandler for why this is a plain atomic load rather than a
// settings lookup.
func (h *TeamQuestionsHandler) effectiveFinalCallStart() time.Time {
	if v := h.finalCallStartOverride.Load(); v != nil {
		return *v
	}
	return defaultTeamQuestionsFinalCallStart
}

func (h *TeamQuestionsHandler) effectiveSubmissionCutoff() time.Time {
	if v := h.submissionCutoffOverride.Load(); v != nil {
		return *v
	}
	return defaultTeamQuestionsSubmissionCutoff
}

// applyDeadlineCache refreshes the cache backing effectiveFinalCallStart/
// effectiveSubmissionCutoff from a freshly loaded settings row. Called by
// getSettings() so every existing caller keeps the cache warm as a side
// effect, and explicitly after AdminUpdateTemplate saves an override.
func (h *TeamQuestionsHandler) applyDeadlineCache(settings models.TeamQuestionsSettings) {
	h.finalCallStartOverride.Store(settings.FinalCallStart)
	h.submissionCutoffOverride.Store(settings.SubmissionCutoff)
}

// effectiveTeamQuestionsFinalCallStart and effectiveTeamQuestionsSubmissionCutoff
// are the same fallback logic as the cache methods above, but for callers
// that already hold a freshly loaded TeamQuestionsSettings (e.g. admin
// endpoints right after their own getSettings() call) and want the true
// current DB state rather than the cache, which can lag by up to one
// scheduler tick on another process.
func effectiveTeamQuestionsFinalCallStart(settings models.TeamQuestionsSettings) time.Time {
	if settings.FinalCallStart != nil {
		return *settings.FinalCallStart
	}
	return defaultTeamQuestionsFinalCallStart
}

func effectiveTeamQuestionsSubmissionCutoff(settings models.TeamQuestionsSettings) time.Time {
	if settings.SubmissionCutoff != nil {
		return *settings.SubmissionCutoff
	}
	return defaultTeamQuestionsSubmissionCutoff
}

func (h *TeamQuestionsHandler) rejectClosed(c *gin.Context, cutoff time.Time) bool {
	if teamQuestionsClosed(h.now(), cutoff) {
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
	admin.GET("/team-questions/delivery-events", h.AdminListDeliveryEvents)
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

// sendCandidateStale reports whether a token committed by issueAndSendOrdinary
// or issueAndSendFinalCall is no longer safe to actually deliver. It's called
// after that token's transaction commits and its row lock is released — a
// concurrent resend, reminder, or final call may have minted a newer token
// for the same application in the meantime (this one's link would already
// be dead on arrival: lookupTokenInDB only accepts the newest unused token),
// or the application may have left pending by some other path entirely
// (e.g. a direct status change) while nothing held the row lock. Either way,
// this token's send should not go out.
//
// This narrows the race rather than closing it — there's still a small gap
// between this check and the sender call actually being invoked — the same
// order of remaining risk this file already accepts elsewhere (see the
// final-call stamp's "not atomic" comment below).
func (h *TeamQuestionsHandler) sendCandidateStale(applicationID uuid.UUID, tokenID uint) (bool, error) {
	var newerCount int64
	if err := h.db.Model(&models.TeamQuestionsToken{}).
		Where("application_id = ? AND id > ?", applicationID, tokenID).
		Count(&newerCount).Error; err != nil {
		return false, err
	}
	if newerCount > 0 {
		return true, nil
	}
	var current models.GeneralApplication
	if err := h.db.Select("status").Where("id = ?", applicationID).First(&current).Error; err != nil {
		return false, err
	}
	return current.Status != models.GeneralApplicationStatusPending, nil
}

// recordDeliveryEvent is the only place Team Questions send outcomes become
// visible to admins — before this, sent/failed/superseded/not-recorded
// outcomes only ever existed as log.Printf lines. Best-effort and
// synchronous: a logging failure here must never change a send's outcome or
// bubble up as an error (hence swallowed, just logged), and it must not run
// in a goroutine, since the scripted-SQL test harness in
// team_questions_db_test.go requires strict single-threaded query ordering
// and closes its connection pool in t.Cleanup.
//
// Raw SQL, deliberately: a plain db.Create here would open its own implicit
// transaction for a single insert, for no benefit — same reasoning as the
// other post-lock raw-SQL writes in this file.
func (h *TeamQuestionsHandler) recordDeliveryEvent(applicationID uuid.UUID, kind models.TeamQuestionsDeliveryEventKind, outcome models.TeamQuestionsDeliveryOutcome, automatic bool, detail string) {
	now := h.now()
	err := h.db.Exec(
		"INSERT INTO team_questions_delivery_events (created_at, updated_at, application_id, kind, outcome, automatic, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, now, applicationID, kind, outcome, automatic, detail,
	).Error
	if err != nil {
		log.Printf("failed to record team questions delivery event (application %s, kind %s, outcome %s): %v", applicationID, kind, outcome, err)
	}
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
	cutoff := h.effectiveSubmissionCutoff()
	if h.rejectClosed(c, cutoff) {
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

	if h.rejectClosed(c, cutoff) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"first_name": application.FirstName,
		"teams":      application.Teams,
		"questions":  questions,
	})
}

func (h *TeamQuestionsHandler) SubmitForm(c *gin.Context) {
	cutoff := h.effectiveSubmissionCutoff()
	if h.rejectClosed(c, cutoff) {
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
		if teamQuestionsClosed(h.now(), cutoff) {
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
		if teamQuestionsClosed(now, cutoff) {
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
		if teamQuestionsClosed(h.now(), cutoff) {
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

	// The submission above is already committed and its HTTP response is a
	// success — whether the deadline happens to fall between that commit and
	// this goroutine running is irrelevant to whether the applicant earned a
	// copy of what they submitted, so this does not recheck teamQuestionsClosed.
	go func(application models.GeneralApplication, teamAnswers []email.TeamQuestionsTeamAnswers, withdrawnTeams []string) {
		if err := h.sendConfirmation(application, teamAnswers, withdrawnTeams); err != nil {
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
	h.applyDeadlineCache(settings)
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
	settings, err := h.getSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team questions settings"})
		return
	}
	finalCallStart := effectiveTeamQuestionsFinalCallStart(settings)
	submissionCutoff := effectiveTeamQuestionsSubmissionCutoff(settings)
	if teamQuestionsClosed(h.now(), submissionCutoff) {
		c.JSON(http.StatusOK, gin.H{"count": 0, "can_send": false, "next_send_at": nil})
		return
	}
	canSend, err := requesterIsHeadOfTeam(h.db, adminID, "IT")
	if err != nil {
		canSend = false
	}

	var applications []models.GeneralApplication
	if teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) {
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
	if next.Before(submissionCutoff) {
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
	if h.rejectClosed(c, h.effectiveSubmissionCutoff()) {
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
	if teamQuestionsFinalCallWindow(h.now(), h.effectiveFinalCallStart(), h.effectiveSubmissionCutoff()) {
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

// AdminListDeliveryEvents returns the most recent Team Questions send
// outcomes for the admin-facing delivery activity view — open to any admin
// (matching AdminGetSubmission/AdminGetTemplate's read-openness), unlike
// AdminSendBulk/AdminUpdateTemplate, which are IT-only to act on.
func (h *TeamQuestionsHandler) AdminListDeliveryEvents(c *gin.Context) {
	var events []models.TeamQuestionsDeliveryEvent
	if err := h.db.Order("created_at DESC").Limit(200).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load delivery events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// SendPendingInvites emails the Team Questions invite to every pending,
// never-invited application (see pendingUninvitedApplications). Shared by the
// admin bulk-send endpoint and the daily scheduler so both go through the
// exact same send path.
func (h *TeamQuestionsHandler) SendPendingInvites() (sent int, failed []string, err error) {
	if !h.now().Before(h.effectiveFinalCallStart()) {
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
			if errors.Is(err, errTeamQuestionsTokenSuperseded) {
				continue // Something else already covers this application.
			}
			if errors.Is(err, errTeamQuestionsDeliveryNotRecorded) {
				log.Printf("ATTENTION: team questions invite for application %s was delivered but not recorded — verify manually before resending: %v", application.Id, err)
				sent++
				continue
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
	if !h.now().Before(h.effectiveFinalCallStart()) {
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
			if errors.Is(err, errTeamQuestionsTokenSuperseded) {
				continue // Something else already covers this application.
			}
			if errors.Is(err, errTeamQuestionsDeliveryNotRecorded) {
				log.Printf("ATTENTION: team questions reminder for application %s was delivered but not recorded — verify manually before resending: %v", application.Id, err)
				sent++
				continue
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
	finalCallStart, submissionCutoff := h.effectiveFinalCallStart(), h.effectiveSubmissionCutoff()
	if !teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) {
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
		if !teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) {
			break
		}
		delivered, err := h.issueAndSendFinalCall(application.Id)
		if err != nil {
			if errors.Is(err, errTeamQuestionsDeliveryNotRecorded) {
				log.Printf("ATTENTION: team questions final call for application %s was delivered but not recorded — verify manually before resending: %v", application.Id, err)
				sent++
				continue
			}
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

// The row lock is released (by committing the token) before sendFinalCall is
// called — final calls run against many applications in quick succession, so
// holding the lock (and a DB connection) across each SES round-trip would
// serialize unrelated work on the same application row and, in a large
// batch, risks exhausting the pool. Unlike issueAndSendOrdinary, a failed
// send here leaves the committed-but-unsent token in place rather than
// deleting it: pendingApplicationsNeedingFinalCall doesn't consult the
// tokens table, so the application is retried regardless, and a stray token
// is harmless — it simply expires unused.
func (h *TeamQuestionsHandler) issueAndSendFinalCall(applicationID uuid.UUID) (bool, error) {
	finalCallStart, submissionCutoff := h.effectiveFinalCallStart(), h.effectiveSubmissionCutoff()
	if h.cfg.DevelopmentMode || !teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) {
		return false, nil
	}

	var application models.GeneralApplication
	var raw string
	var tokenID uint
	ready := false
	err := h.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&application, "id = ?", applicationID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) || application.ApplicationYear != generalApplicationYear ||
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
		var hash string
		raw, hash, err = utils.GenerateToken()
		if err != nil {
			return err
		}
		token := models.TeamQuestionsToken{ApplicationID: applicationID, TokenHash: hash, ExpiresAt: submissionCutoff}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		tokenID = token.ID
		// One more check before committing and releasing the lock — a window
		// closing here still rolls back the unsent token along with it.
		if !teamQuestionsFinalCallWindow(h.now(), finalCallStart, submissionCutoff) {
			return errTeamQuestionsClosed
		}
		ready = true
		return nil
	})
	if err != nil {
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeFailed, true, err.Error())
		return false, err
	}
	if !ready {
		return false, nil
	}

	// The lock is released; before spending a network round-trip, make sure
	// nothing superseded this token in the meantime — see sendCandidateStale.
	if stale, staleErr := h.sendCandidateStale(applicationID, tokenID); staleErr != nil {
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeFailed, true, staleErr.Error())
		return false, staleErr
	} else if stale {
		if delErr := h.db.Exec("DELETE FROM team_questions_tokens WHERE id = ?", tokenID).Error; delErr != nil {
			log.Printf("failed to remove superseded team questions token for application %s: %v", applicationID, delErr)
		}
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeSuperseded, true, "")
		return false, nil
	}

	if err := h.sendFinalCall(application, defaultTeamQuestionsFinalCallTemplate, defaultTeamQuestionsFinalCallSubject, h.formURL(raw)); err != nil {
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeFailed, true, err.Error())
		return false, err
	}
	// Explicit SQL is intentional: ordinary GORM saves cannot update this
	// marker. Record accepted delivery even if the send finished after cutoff.
	// SES acceptance and this commit are not atomic; a crash between them
	// can still cause a duplicate on retry.
	//
	// Any failure from here on is wrapped in errTeamQuestionsDeliveryNotRecorded:
	// the email is already sent, so callers must never treat this as an
	// ordinary send failure (which would invite a duplicate resend) — it
	// needs a human to reconcile instead.
	result := h.db.Exec("UPDATE general_applications SET team_questions_final_call_sent_at = ? WHERE id = ? AND team_questions_final_call_sent_at IS NULL", h.now(), applicationID)
	if result.Error != nil {
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeNotRecorded, true, result.Error.Error())
		return false, fmt.Errorf("%w: %w", errTeamQuestionsDeliveryNotRecorded, result.Error)
	}
	if result.RowsAffected != 1 {
		h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeNotRecorded, true, "timestamp update affected no rows")
		return false, fmt.Errorf("%w: final call delivered but its timestamp was not recorded", errTeamQuestionsDeliveryNotRecorded)
	}
	h.recordDeliveryEvent(applicationID, models.TeamQuestionsDeliveryEventKindFinalCall, models.TeamQuestionsDeliveryOutcomeSent, true, "")
	return true, nil
}

// AdminResend issues a fresh token for a single application (retiring any
// older unused token for it) and re-sends the invite. Only valid while the
// application is still pending — once Team Questions has been submitted,
// marked ineligible, or withdrawn, there is nothing to resend.
func (h *TeamQuestionsHandler) AdminResend(c *gin.Context) {
	// Cache-based, not a fresh getSettings() call: this guard must fire
	// before any DB access so malformed-input requests on a closed window
	// (see TestTeamQuestionsClosedHTTPGuards, which exercises this with a
	// nil DB) return 410 without ever touching h.db.
	if h.rejectClosed(c, h.effectiveSubmissionCutoff()) {
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
		if errors.Is(err, errTeamQuestionsTokenSuperseded) {
			c.JSON(http.StatusConflict, gin.H{"error": "a concurrent update issued a newer link for this application; refresh and try again"})
			return
		}
		if errors.Is(err, errTeamQuestionsDeliveryNotRecorded) {
			log.Printf("ATTENTION: team questions resend for application %s was delivered but not recorded — verify manually before resending: %v", application.Id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "the email was sent but could not be recorded — check this application manually before resending"})
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
	if teamQuestionsClosed(now, h.effectiveSubmissionCutoff()) {
		return errTeamQuestionsClosed
	}
	if automatic && !now.Before(h.effectiveFinalCallStart()) {
		return errTeamQuestionsOrdinarySendEnded
	}
	return nil
}

// Keep manual resends available until closure. Automatic invites/reminders
// stop at the final-call boundary, including a batch already in progress.
//
// Token creation is committed (and the row lock released) before the SES
// call: that call can take seconds, and holding the row lock — and a DB
// connection — for the whole round-trip would block a concurrent submission
// or resend on this same application and, during a bulk send, risks
// exhausting the pool. If the send fails, the now-unusable token is removed
// so this application is retried on the next run instead of being treated
// as already invited forever.
func (h *TeamQuestionsHandler) issueAndSendOrdinary(application models.GeneralApplication, templateText, subjectTemplate string, reminder, automatic bool) error {
	if err := h.ordinarySendWindowError(automatic); err != nil {
		return err
	}

	var current models.GeneralApplication
	var raw string
	var tokenID uint
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", application.Id).Error; err != nil {
			return err
		}
		if err := h.ordinarySendWindowError(automatic); err != nil {
			return err
		}
		if current.Status != models.GeneralApplicationStatusPending {
			return gorm.ErrRecordNotFound
		}
		var hash string
		var tokenErr error
		raw, hash, tokenErr = utils.GenerateToken()
		if tokenErr != nil {
			return tokenErr
		}
		token := models.TeamQuestionsToken{
			ApplicationID: current.Id,
			TokenHash:     hash,
			ExpiresAt:     h.now().Add(teamQuestionsTokenValidity),
		}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		tokenID = token.ID
		// One more check before committing and releasing the lock — a window
		// closing here still rolls back the unsent token along with it.
		return h.ordinarySendWindowError(automatic)
	})
	kind := models.TeamQuestionsDeliveryEventKindInvite
	if reminder {
		kind = models.TeamQuestionsDeliveryEventKindReminder
	}
	if err != nil {
		h.recordDeliveryEvent(application.Id, kind, models.TeamQuestionsDeliveryOutcomeFailed, automatic, err.Error())
		return err
	}

	// The lock is released; before spending a network round-trip, make sure
	// nothing superseded this token in the meantime — see sendCandidateStale.
	if stale, staleErr := h.sendCandidateStale(current.Id, tokenID); staleErr != nil {
		h.recordDeliveryEvent(current.Id, kind, models.TeamQuestionsDeliveryOutcomeFailed, automatic, staleErr.Error())
		return staleErr
	} else if stale {
		if delErr := h.db.Exec("DELETE FROM team_questions_tokens WHERE id = ?", tokenID).Error; delErr != nil {
			log.Printf("failed to remove superseded team questions token for application %s: %v", current.Id, delErr)
		}
		h.recordDeliveryEvent(current.Id, kind, models.TeamQuestionsDeliveryOutcomeSuperseded, automatic, "")
		return errTeamQuestionsTokenSuperseded
	}

	sender := h.sendInvite
	stampSQL := "UPDATE general_applications SET team_questions_invite_sent_at = ? WHERE id = ?"
	if reminder {
		sender = h.sendReminder
		stampSQL = "UPDATE general_applications SET team_questions_reminder_sent_at = ? WHERE id = ?"
	}
	if !h.cfg.DevelopmentMode {
		if err := sender(current, templateText, subjectTemplate, h.formURL(raw)); err != nil {
			// Raw SQL, deliberately outside any transaction: a plain
			// tx.Delete/Update here would each open their own implicit
			// transaction for a single statement, for no benefit.
			if delErr := h.db.Exec("DELETE FROM team_questions_tokens WHERE id = ?", tokenID).Error; delErr != nil {
				log.Printf("failed to remove unsent team questions token for application %s: %v", current.Id, delErr)
			}
			h.recordDeliveryEvent(current.Id, kind, models.TeamQuestionsDeliveryOutcomeFailed, automatic, err.Error())
			return err
		}
	} else {
		log.Printf("[dev] skipping SES — would have sent team questions email for application %s", current.Id)
	}
	// The email is already out at this point (or dev-mode skipped it): a
	// failure here must never be reported as an ordinary send failure — the
	// token stays in place so the automatic queries don't pick this
	// application up again, but errTeamQuestionsDeliveryNotRecorded tells
	// callers delivery happened and only the bookkeeping failed, so a human
	// reconciles it instead of resending.
	if err := h.db.Exec(stampSQL, h.now(), current.Id).Error; err != nil {
		h.recordDeliveryEvent(current.Id, kind, models.TeamQuestionsDeliveryOutcomeNotRecorded, automatic, err.Error())
		return fmt.Errorf("%w: %w", errTeamQuestionsDeliveryNotRecorded, err)
	}
	h.recordDeliveryEvent(current.Id, kind, models.TeamQuestionsDeliveryOutcomeSent, automatic, "")
	return nil
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
		"email_template":             settings.EmailTemplate,
		"email_subject":              settings.EmailSubject,
		"reminder_email_template":    settings.ReminderEmailTemplate,
		"reminder_email_subject":     settings.ReminderEmailSubject,
		"final_call_start":           effectiveTeamQuestionsFinalCallStart(settings),
		"submission_cutoff":          effectiveTeamQuestionsSubmissionCutoff(settings),
		"final_call_start_override":  settings.FinalCallStart,
		"submission_cutoff_override": settings.SubmissionCutoff,
		"updated_by_email":           settings.UpdatedByEmail,
		"can_edit":                   canEdit,
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

	// Decoded as raw key presence, not a plain struct: this is a partial
	// update. The email templates and the deadlines are edited from two
	// separate admin panels, potentially by two different admins at close to
	// the same time — binding into a struct with the full set of fields
	// required on every request would mean whichever request lands second
	// silently reverts the other's change back to whatever it happened to
	// have loaded. A key's presence means "set this field" (including
	// explicitly to null, for the two deadline overrides, which is how the
	// admin UI resets one to the default); a key's absence means "leave
	// whatever is already saved alone."
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	var settings models.TeamQuestionsSettings
	err = h.db.First(&settings).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load email template"})
		return
	}
	isNew := errors.Is(err, gorm.ErrRecordNotFound)

	setString := func(key string, dst *string) bool {
		v, present := raw[key]
		if !present {
			return true
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": key + " must be a string"})
			return false
		}
		*dst = strings.TrimSpace(s)
		return true
	}
	setTimePtr := func(key string, dst **time.Time) bool {
		v, present := raw[key]
		if !present {
			return true
		}
		var t *time.Time
		if err := json.Unmarshal(v, &t); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": key + " must be a timestamp or null"})
			return false
		}
		*dst = t
		return true
	}

	if !setString("email_template", &settings.EmailTemplate) ||
		!setString("email_subject", &settings.EmailSubject) ||
		!setString("reminder_email_template", &settings.ReminderEmailTemplate) ||
		!setString("reminder_email_subject", &settings.ReminderEmailSubject) ||
		!setTimePtr("final_call_start_override", &settings.FinalCallStart) ||
		!setTimePtr("submission_cutoff_override", &settings.SubmissionCutoff) {
		return
	}

	if !effectiveTeamQuestionsFinalCallStart(settings).Before(effectiveTeamQuestionsSubmissionCutoff(settings)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "final call start must be before the submission cutoff"})
		return
	}

	settings.UpdatedByEmail = adminEmail

	if isNew {
		err = h.db.Create(&settings).Error
	} else {
		err = h.db.Save(&settings).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save email template"})
		return
	}
	h.applyDeadlineCache(settings)

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
		"email_template":             responseTemplate,
		"email_subject":              responseSubject,
		"reminder_email_template":    responseReminderTemplate,
		"reminder_email_subject":     responseReminderSubject,
		"final_call_start":           effectiveTeamQuestionsFinalCallStart(settings),
		"submission_cutoff":          effectiveTeamQuestionsSubmissionCutoff(settings),
		"final_call_start_override":  settings.FinalCallStart,
		"submission_cutoff_override": settings.SubmissionCutoff,
		"updated_by_email":           settings.UpdatedByEmail,
		"can_edit":                   true,
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
