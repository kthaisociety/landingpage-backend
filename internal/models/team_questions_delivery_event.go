package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TeamQuestionsDeliveryEventKind identifies which automatic Team Questions
// email a TeamQuestionsDeliveryEvent is about.
type TeamQuestionsDeliveryEventKind string

const (
	TeamQuestionsDeliveryEventKindInvite    TeamQuestionsDeliveryEventKind = "invite"
	TeamQuestionsDeliveryEventKindReminder  TeamQuestionsDeliveryEventKind = "reminder"
	TeamQuestionsDeliveryEventKindFinalCall TeamQuestionsDeliveryEventKind = "final_call"
)

// TeamQuestionsDeliveryOutcome is the terminal result of one Team Questions
// send attempt.
type TeamQuestionsDeliveryOutcome string

const (
	// TeamQuestionsDeliveryOutcomeSent is a plain, fully recorded success.
	TeamQuestionsDeliveryOutcomeSent TeamQuestionsDeliveryOutcome = "sent"
	// TeamQuestionsDeliveryOutcomeFailed means the send was never dispatched
	// or the provider rejected it — safe to retry.
	TeamQuestionsDeliveryOutcomeFailed TeamQuestionsDeliveryOutcome = "failed"
	// TeamQuestionsDeliveryOutcomeSuperseded means a concurrent operation
	// already covered this application before dispatch — see
	// errTeamQuestionsTokenSuperseded in the handlers package.
	TeamQuestionsDeliveryOutcomeSuperseded TeamQuestionsDeliveryOutcome = "superseded"
	// TeamQuestionsDeliveryOutcomeNotRecorded means the provider accepted the
	// message but the database write recording that failed — see
	// errTeamQuestionsDeliveryNotRecorded in the handlers package. Needs
	// manual reconciliation; must never be treated as a plain failure.
	TeamQuestionsDeliveryOutcomeNotRecorded TeamQuestionsDeliveryOutcome = "delivered_not_recorded"
)

// TeamQuestionsDeliveryEvent is an append-only record of every Team
// Questions send attempt's outcome, automatic or admin-triggered. Before
// this existed, sends/failures/superseded-skips/unrecorded-deliveries were
// only ever visible as server log lines — this is what the admin-facing
// delivery activity view reads from.
type TeamQuestionsDeliveryEvent struct {
	gorm.Model
	ApplicationID uuid.UUID                      `gorm:"type:uuid;not null;index" json:"application_id"`
	Kind          TeamQuestionsDeliveryEventKind `gorm:"type:text;not null" json:"kind"`
	Outcome       TeamQuestionsDeliveryOutcome   `gorm:"type:text;not null;index" json:"outcome"`
	// Automatic is false only for a manual admin resend (AdminResend); every
	// scheduled invite/reminder/final-call is true.
	Automatic bool   `gorm:"not null;default:false" json:"automatic"`
	Detail    string `gorm:"type:text;not null;default:''" json:"detail"`
}
