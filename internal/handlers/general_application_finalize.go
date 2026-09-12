package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"backend/internal/email"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// finalizePhaseOpenConfirmPhrase and finalizePhaseCloseConfirmPhrase must be
// typed exactly, byte-for-byte, to open/close the finalize phase. This is the
// "no accidental accepting" guard: there is no UI affordance that can trigger
// either by a stray click, only a deliberate, typed action.
const (
	finalizePhaseOpenConfirmPhrase  = "OPEN FINALIZE PHASE"
	finalizePhaseCloseConfirmPhrase = "CLOSE FINALIZE PHASE"
)

// getFinalizeRecruitmentPhase returns the singleton phase row, creating it
// (in its never-opened zero state) on first use — same lazy-singleton
// convention as getGeneralApplicationSettings.
func (h *GeneralApplicationHandler) getFinalizeRecruitmentPhase() (models.FinalizeRecruitmentPhase, error) {
	var phase models.FinalizeRecruitmentPhase
	err := h.db.First(&phase).Error
	if err == gorm.ErrRecordNotFound {
		if createErr := h.db.Create(&phase).Error; createErr != nil {
			return phase, createErr
		}
		return phase, nil
	}
	return phase, err
}

// requesterAcceptTeam resolves which team an admin's acceptance should be
// recorded under: their declared Profile.AdminTeam if it's a valid
// recruitment team, otherwise a TeamMember row of theirs in one. Returns ""
// if neither resolves — that admin has no team to accept applicants onto.
func requesterAcceptTeam(db *gorm.DB, userID uuid.UUID) (string, error) {
	var profile models.Profile
	if err := db.Where("user_uuid = ?", userID).First(&profile).Error; err != nil {
		return "", err
	}
	if _, ok := allowedApplicationTeams[profile.AdminTeam]; ok {
		return profile.AdminTeam, nil
	}

	var member models.TeamMember
	err := db.Where("user_id = ? AND team_member_department IN ?", profile.UserId, applicationTeamNames).
		Order("created_at DESC").
		First(&member).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return member.TeamMemberDepartment, nil
}

// AdminOpenFinalizePhase opens the finalize recruitment phase, unlocking
// AdminFinalizeDecision for every admin. Requires an IT admin and the exact
// confirmation phrase. Also usable to re-open a previously closed phase (same
// gate applies) — not a normal step, but not blocked either.
func (h *GeneralApplicationHandler) AdminOpenFinalizePhase(c *gin.Context) {
	userID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	isIT, err := requesterIsOnTeam(h.db, userID, "IT")
	if err != nil || !isIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only IT admins can open the finalize phase"})
		return
	}

	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.Confirm != finalizePhaseOpenConfirmPhrase {
		c.JSON(http.StatusBadRequest, gin.H{"error": `type "` + finalizePhaseOpenConfirmPhrase + `" exactly to confirm`})
		return
	}

	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load finalize phase"})
		return
	}

	now := time.Now()
	phase.OpenedByEmail = adminEmail
	phase.OpenedAt = &now
	phase.ClosedByEmail = ""
	phase.ClosedAt = nil
	if err := h.db.Save(&phase).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open finalize phase"})
		return
	}

	c.JSON(http.StatusOK, phase)
}

// AdminFinalizePhaseStatus returns the current phase state. Any admin may
// call this — everyone needs to know whether they can act.
func (h *GeneralApplicationHandler) AdminFinalizePhaseStatus(c *gin.Context) {
	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load finalize phase"})
		return
	}
	c.JSON(http.StatusOK, phase)
}

// AdminCloseFinalizePhase closes the finalize phase: no further finalize
// decisions can be made afterward until it's reopened. Onboarding itself
// does not happen here — it's already triggered per-applicant, as each one
// is individually accepted via AdminFinalizeDecision (see
// notifyOnboardingService there), not batched at phase-close. Requires
// specifically the head of IT (not just any IT admin) and the exact
// confirmation phrase.
func (h *GeneralApplicationHandler) AdminCloseFinalizePhase(c *gin.Context) {
	userID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	isHeadOfIT, err := requesterIsHeadOfTeam(h.db, userID, "IT")
	if err != nil || !isHeadOfIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the head of IT can close the finalize phase"})
		return
	}

	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.Confirm != finalizePhaseCloseConfirmPhrase {
		c.JSON(http.StatusBadRequest, gin.H{"error": `type "` + finalizePhaseCloseConfirmPhrase + `" exactly to confirm`})
		return
	}

	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load finalize phase"})
		return
	}
	if !phase.IsOpen() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "finalize phase is not open"})
		return
	}

	now := time.Now()
	phase.ClosedByEmail = adminEmail
	phase.ClosedAt = &now
	if err := h.db.Save(&phase).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to close finalize phase"})
		return
	}

	c.JSON(http.StatusOK, phase)
}

// allowedFinalizeDecisions are the only two statuses AdminFinalizeDecision can
// set. Both are absent from allowedApplicationStatuses by design (see the
// comment there) — this is their only entry point.
var allowedFinalizeDecisions = map[models.GeneralApplicationStatus]struct{}{
	models.GeneralApplicationStatusAccepted: {},
	models.GeneralApplicationStatusRejected: {},
}

// isFinalizeEligible reports whether an application has been through an
// interview and so is eligible for a terminal decision: either it's
// currently interviewing, or it cycled back to available after at least one
// interview (e.g. released post-interview, or claimable again by a second
// team it also applied to). An available applicant who was never
// interviewed is not eligible — they haven't been through the round yet.
func isFinalizeEligible(application models.GeneralApplication) bool {
	if application.Status == models.GeneralApplicationStatusInterviewing {
		return true
	}
	return application.Status == models.GeneralApplicationStatusAvailable &&
		len(application.InterviewedBy) > 0
}

// AdminFinalizeDecision records the terminal accept/reject decision for one
// interviewed applicant. Requires the finalize phase to be open. Any admin
// may reject; accepting additionally requires the requester to resolve to a
// real team (see requesterAcceptTeam) — that team, never a client-supplied
// one, is what gets recorded, so "head of Business accepts someone" always
// means AssignedTeam ends up "Business". Only applications isFinalizeEligible
// can be finalized, checked and updated inside a row lock so two admins
// deciding the same applicant at once can't both succeed.
func (h *GeneralApplicationHandler) AdminFinalizeDecision(c *gin.Context) {
	userID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check finalize phase"})
		return
	}
	if !phase.IsOpen() {
		c.JSON(http.StatusForbidden, gin.H{"error": "the finalize phase is not open"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}

	var body struct {
		Decision models.GeneralApplicationStatus `json:"decision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if _, ok := allowedFinalizeDecisions[body.Decision]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": `decision must be "accepted" or "rejected"`})
		return
	}

	var acceptTeam string
	if body.Decision == models.GeneralApplicationStatusAccepted {
		acceptTeam, err = requesterAcceptTeam(h.db, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve your team"})
			return
		}
		if acceptTeam == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "you must be assigned to a team to accept applicants"})
			return
		}
	}

	var application models.GeneralApplication
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&application, "id = ?", id).Error; err != nil {
			return err
		}
		if !isFinalizeEligible(application) {
			return errApplicationNotFinalizable
		}

		now := time.Now()
		application.Status = body.Decision
		application.FinalizedByEmail = adminEmail
		application.FinalizedAt = &now
		if body.Decision == models.GeneralApplicationStatusAccepted {
			application.AssignedTeam = acceptTeam
		}
		return tx.Save(&application).Error
	})
	if err == errApplicationNotFinalizable {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only applicants who are interviewing, or available after being interviewed, can be finalized"})
		return
	}
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save finalize decision"})
		return
	}

	// Accepted applicants get no direct email here — onboarding-service's own
	// "start onboarding" email (sent via notifyOnboardingService below) is
	// their welcome notice, and now carries the assigned team too. Only
	// rejection needs a one-off email straight from this handler.
	if application.Status == models.GeneralApplicationStatusRejected {
		go func(application models.GeneralApplication) {
			if err := h.sendRejectionEmailAndMark(application); err != nil {
				log.Printf("failed to send finalize rejection email for %s: %v", application.Id, err)
			}
		}(application)
	}

	if application.Status == models.GeneralApplicationStatusAccepted {
		go h.notifyOnboardingServiceAndMark(application)
	}

	c.JSON(http.StatusOK, application)
}

// errApplicationNotFinalizable is a sentinel returned from inside the
// AdminFinalizeDecision transaction to distinguish "wrong state" (400) from
// "not found" (404) and genuine DB errors (500), since gorm.Transaction only
// gives the callback's returned error back to the caller.
var errApplicationNotFinalizable = errors.New("application not eligible for a finalize decision")

// notifyOnboardingServiceAndMark calls notifyOnboardingService and, only on
// success, stamps OnboardingNotifiedAt — the durable record that this
// applicant actually got their (only) welcome notice. A failure here is not
// silent: notifyOnboardingService itself already logs loudly after
// exhausting its retries, and OnboardingNotifiedAt staying nil is the
// admin-visible sign that AdminRetryOnboardingNotify still needs to run for
// this applicant.
func (h *GeneralApplicationHandler) notifyOnboardingServiceAndMark(application models.GeneralApplication) {
	if !notifyOnboardingService(h.cfg, application.Id.String(), application.FirstName, application.LastName, application.Email, application.AssignedTeam) {
		return
	}
	if err := h.db.Model(&models.GeneralApplication{}).Where("id = ?", application.Id).
		Update("onboarding_notified_at", time.Now()).Error; err != nil {
		log.Printf("ATTENTION: onboarding notify for application %s succeeded but was not recorded — it may look unnotified when it wasn't: %v", application.Id, err)
	}
}

// AdminRetryOnboardingNotify re-runs notifyOnboardingService for one
// accepted applicant whose OnboardingNotifiedAt is still nil — the recovery
// action for AdminFinalizeDecision's async notify having exhausted its
// retries and failed outright (onboarding-service down, misconfigured, or
// erroring for longer than the retry window covers). Safe to call even if
// the applicant secretly was notified after all: onboarding-service's own
// /notify handler already dedupes on application_id and returns the
// existing record rather than creating a second one. Open to any admin,
// same reasoning as AdminFastTrackApplication — this is a recovery action,
// not a decision, and who ran it is worth recording for accountability.
func (h *GeneralApplicationHandler) AdminRetryOnboardingNotify(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid application id"})
		return
	}
	if _, _, ok := getAdminIdentity(c); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var application models.GeneralApplication
	if err := h.db.First(&application, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	if application.Status != models.GeneralApplicationStatusAccepted {
		c.JSON(http.StatusConflict, gin.H{"error": "only an accepted application can be re-notified"})
		return
	}
	if application.OnboardingNotifiedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "this application was already notified"})
		return
	}

	if !notifyOnboardingService(h.cfg, application.Id.String(), application.FirstName, application.LastName, application.Email, application.AssignedTeam) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "onboarding service is still unreachable or erroring — check its logs"})
		return
	}
	now := time.Now()
	if err := h.db.Model(&application).Update("onboarding_notified_at", now).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notified successfully but failed to record it — refresh before retrying again"})
		return
	}
	application.OnboardingNotifiedAt = &now
	c.JSON(http.StatusOK, application)
}

// claimRejectionEmail atomically claims application for a rejection-email
// send: it flips RejectionEmailSentAt from NULL to now in one conditional
// UPDATE and reports whether *this* call was the one that flipped it.
// Claiming before sending (rather than sending then marking) is what makes
// concurrent callers safe — two admins double-clicking a bulk send, or a
// bulk sweep racing an individual AdminFinalizeDecision rejection for the
// same applicant — since only one UPDATE...WHERE rejection_email_sent_at IS
// NULL can ever affect a row; every other concurrent caller sees
// RowsAffected == 0 and must skip rather than send. Call
// releaseRejectionEmailClaim if the send that follows a successful claim
// then fails, so the application remains eligible for a later retry instead
// of being permanently (and wrongly) marked as emailed.
func (h *GeneralApplicationHandler) claimRejectionEmail(applicationID uuid.UUID) (claimed bool, err error) {
	result := h.db.Model(&models.GeneralApplication{}).
		Where("id = ? AND rejection_email_sent_at IS NULL", applicationID).
		Update("rejection_email_sent_at", time.Now())
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// releaseRejectionEmailClaim undoes a claimRejectionEmail claim after the
// send that was supposed to follow it failed. Logs loudly rather than
// returning an error: this runs from inside an already-failed send path, so
// there's no additional response to attach the error to — an admin needs to
// notice this in the logs and check the application manually if it fails.
func (h *GeneralApplicationHandler) releaseRejectionEmailClaim(applicationID uuid.UUID) {
	if err := h.db.Model(&models.GeneralApplication{}).Where("id = ?", applicationID).
		Update("rejection_email_sent_at", nil).Error; err != nil {
		log.Printf("ATTENTION: failed to release the rejection-email claim for application %s after a failed send — it will not be retried until this is cleared manually: %v", applicationID, err)
	}
}

// sendRejectionEmailAndMark claims, then sends, the rejection email for
// application — see claimRejectionEmail for why the claim comes first. A
// lost claim race (someone else got there first) is not an error: it means
// this applicant is already being handled.
func (h *GeneralApplicationHandler) sendRejectionEmailAndMark(application models.GeneralApplication) error {
	claimed, err := h.claimRejectionEmail(application.Id)
	if err != nil {
		return fmt.Errorf("failed to claim rejection email: %w", err)
	}
	if !claimed {
		return nil
	}

	settings, err := h.getGeneralApplicationSettings()
	if err != nil {
		h.releaseRejectionEmailClaim(application.Id)
		return fmt.Errorf("failed to load application settings: %w", err)
	}
	if err := email.SendGeneralApplicationRejection(application, settings.RejectionIntroText); err != nil {
		h.releaseRejectionEmailClaim(application.Id)
		return err
	}
	return nil
}

// pendingRejectionApplications finds every application from this
// recruitment cycle still sitting in a non-terminal status — pending,
// available, interviewing, or ineligible — that hasn't already been sent a
// rejection email. This deliberately reaches further than
// AdminFinalizeDecision ever does: isFinalizeEligible only lets an admin
// decide applicants who were actually interviewed, so anyone never
// interviewed, or marked ineligible, would otherwise never reach a terminal
// decision or hear back at all. Deliberately excludes "rejected" too, not
// just "accepted"/"withdrawn": an application already rejected went through
// AdminFinalizeDecision, which already attempted its own send (see
// sendRejectionEmailAndMark) — this bulk sweep exists to catch applicants
// who never got an individual decision, not to retry sends that may or may
// not have gone out historically (there's no reliable record either way for
// anything rejected before RejectionEmailSentAt existed). Shared by
// AdminSendRejectionsBulk and its preview.
func (h *GeneralApplicationHandler) pendingRejectionApplications() ([]models.GeneralApplication, error) {
	var applications []models.GeneralApplication
	err := h.db.
		Where("application_year = ? AND status IN ? AND rejection_email_sent_at IS NULL",
			generalApplicationYear,
			[]string{
				string(models.GeneralApplicationStatusPending),
				string(models.GeneralApplicationStatusAvailable),
				string(models.GeneralApplicationStatusInterviewing),
				string(models.GeneralApplicationStatusIneligible),
			},
		).
		Find(&applications).Error
	return applications, err
}

// AdminSendRejectionsBulkPreview reports how many applications the next
// AdminSendRejectionsBulk call would email, without sending anything — lets
// the admin UI confirm the count before committing. Also reports whether the
// requester is allowed to actually send (only the head of IT) and whether
// the finalize phase has been closed yet, which AdminSendRejectionsBulk
// requires.
func (h *GeneralApplicationHandler) AdminSendRejectionsBulkPreview(c *gin.Context) {
	userID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load finalize phase"})
		return
	}
	canSend, err := requesterIsHeadOfTeam(h.db, userID, "IT")
	if err != nil {
		canSend = false
	}
	if phase.ClosedAt == nil {
		c.JSON(http.StatusOK, gin.H{"count": 0, "can_send": false, "phase_closed": false})
		return
	}

	applications, err := h.pendingRejectionApplications()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load applications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(applications), "can_send": canSend, "phase_closed": true})
}

// AdminSendRejectionsBulk is restricted to the head of IT — bulk-emailing
// every remaining applicant, and stamping a terminal decision on
// applications that were never individually decided, is disruptive and hard
// to undo, the same reasoning as TeamQuestionsHandler.AdminSendBulk. Requires
// the finalize phase to have been closed at least once (see
// AdminCloseFinalizePhase): this is meant to run once recruitment for the
// cycle has genuinely concluded — after acceptances have gone out and new
// members are being onboarded — not mid-process.
func (h *GeneralApplicationHandler) AdminSendRejectionsBulk(c *gin.Context) {
	userID, adminEmail, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	isHeadOfIT, err := requesterIsHeadOfTeam(h.db, userID, "IT")
	if err != nil || !isHeadOfIT {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the head of IT can bulk-send rejection emails"})
		return
	}
	phase, err := h.getFinalizeRecruitmentPhase()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load finalize phase"})
		return
	}
	if phase.ClosedAt == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the finalize phase must be closed before bulk-sending rejections"})
		return
	}

	sent, failed, err := h.SendPendingRejections(adminEmail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send rejection emails"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": sent, "failed": failed})
}

// SendPendingRejections emails the rejection notice to every application
// pendingRejectionApplications finds, stamping each rejected as it goes —
// this bulk sweep is their only path to a terminal decision, since
// pendingRejectionApplications only ever returns applications
// AdminFinalizeDecision could never reach (see its doc comment). Each
// application is claimed (see claimRejectionEmail) before it's sent, so a
// concurrent bulk-send call or a concurrent individual
// AdminFinalizeDecision for the same applicant can never both send — a lost
// claim is simply skipped, not counted as a failure.
func (h *GeneralApplicationHandler) SendPendingRejections(adminEmail string) (sent int, failed []string, err error) {
	applications, err := h.pendingRejectionApplications()
	if err != nil {
		return 0, nil, err
	}
	if len(applications) == 0 {
		return 0, nil, nil
	}

	settings, err := h.getGeneralApplicationSettings()
	if err != nil {
		return 0, nil, err
	}

	failed = []string{}
	for _, application := range applications {
		claimed, claimErr := h.claimRejectionEmail(application.Id)
		if claimErr != nil {
			log.Printf("failed to claim rejection email for application %s: %v", application.Id, claimErr)
			failed = append(failed, application.Id.String())
			continue
		}
		if !claimed {
			continue
		}

		if sendErr := email.SendGeneralApplicationRejection(application, settings.RejectionIntroText); sendErr != nil {
			log.Printf("failed to send bulk rejection email for application %s: %v", application.Id, sendErr)
			h.releaseRejectionEmailClaim(application.Id)
			failed = append(failed, application.Id.String())
			continue
		}

		// The email is already sent and claimed at this point — this write is
		// "just" bookkeeping (Status/FinalizedAt), but a transient failure
		// here (e.g. a momentary DB hiccup) would otherwise strand the
		// application in a self-contradictory state forever: emailed and
		// excluded from any future sweep (via the claim above), yet still
		// sitting in a non-terminal status with no reconciliation path.
		// Retried a few times before giving up, same reasoning as
		// notifyOnboardingService's retries.
		now := time.Now()
		updates := map[string]interface{}{
			"status":             models.GeneralApplicationStatusRejected,
			"finalized_by_email": adminEmail,
			"finalized_at":       now,
		}
		var updateErr error
		for attempt := 1; attempt <= 3; attempt++ {
			updateErr = h.db.Model(&models.GeneralApplication{}).Where("id = ?", application.Id).Updates(updates).Error
			if updateErr == nil {
				break
			}
			if attempt < 3 {
				time.Sleep(200 * time.Millisecond)
			}
		}
		if updateErr != nil {
			log.Printf("ATTENTION: rejection email for application %s was sent but the terminal status was not recorded after retries — it is stuck non-terminal even though it was emailed; fix its status manually: %v", application.Id, updateErr)
		}
		sent++
	}

	return sent, failed, nil
}
