package database

import (
	"backend/internal/models"

	"gorm.io/gorm"
)

// Migrate runs AutoMigrate over every model the API owns, in the order the
// server has always migrated them.
//
// It lives here rather than inline in cmd/api so the migration regression test
// can exercise the exact list that runs at startup, instead of a copy that
// drifts out of sync with it.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.User{},
		&models.Profile{},
		&models.Event{},
		&models.Registration{},
		&models.TeamMember{},
		&models.BlobData{},
		&models.JobListing{},
		&models.Company{},
		&models.Project{},
		&models.ProjectMember{},
		&models.Team{},
		&models.TeamProjectPair{},
		&models.TeamMemberPair{},
		&models.GeneralApplication{},
		&models.AdminInterviewNote{},
		&models.ApplicationSharedNote{},
		&models.ApplicationSharedNoteEntry{},
		&models.NewsletterSubscription{},
		&models.TeamQuestionsSubmission{},
		&models.TeamQuestionsToken{},
		&models.TeamQuestionsSettings{},
		&models.TeamQuestion{},
		&models.GeneralApplicationSettings{},
		&models.FinalizeRecruitmentPhase{},
		&models.TeamQuestionsDeliveryEvent{},
	)
}
