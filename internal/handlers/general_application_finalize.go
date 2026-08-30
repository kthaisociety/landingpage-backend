package handlers

import (
	"errors"
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
// decisions can be made afterward until it's reopened. This is the point at
// which the list of accepted applicants is treated as solid and onboarding
// begins for them (see onboarding-service-plan.md at the repo root) — no
// onboarding call happens here yet, that service doesn't exist yet. Requires
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

	go func(application models.GeneralApplication) {
		var sendErr error
		if application.Status == models.GeneralApplicationStatusAccepted {
			sendErr = email.SendGeneralApplicationAcceptance(application)
		} else {
			sendErr = email.SendGeneralApplicationRejection(application)
		}
		if sendErr != nil {
			log.Printf("failed to send finalize decision email for %s (%s): %v", application.Id, application.Status, sendErr)
		}
	}(application)

	c.JSON(http.StatusOK, application)
}

// errApplicationNotFinalizable is a sentinel returned from inside the
// AdminFinalizeDecision transaction to distinguish "wrong state" (400) from
// "not found" (404) and genuine DB errors (500), since gorm.Transaction only
// gives the callback's returned error back to the caller.
var errApplicationNotFinalizable = errors.New("application not eligible for a finalize decision")
