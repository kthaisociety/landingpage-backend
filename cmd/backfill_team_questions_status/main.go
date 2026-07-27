// One-off migration for the Team Questions rollout. Run manually, once, when
// deploying that feature — it is NOT wired into cmd/api/main.go and never
// runs automatically.
//
// Before Team Questions existed, "available" meant "screened, ready to
// claim," and "interviewing" meant claimed and mid-process. Both now require
// having submitted Team Questions first — so anything already at "available"
// or "interviewing" from before this deploy needs to move back to "pending",
// or it would (or already did) get claimed without ever answering the
// team-specific questions. Any in-progress claim is released as part of this
// (InterviewingByUserID/Email cleared) since "pending" applications can't be
// claimed; admins re-claim from scratch once Team Questions is submitted.
//
// Defaults to a dry run. Pass -confirm to actually apply the update.
//
// Usage:
//
//	go run ./cmd/backfill_team_questions_status            # dry run
//	go run ./cmd/backfill_team_questions_status -confirm   # apply
package main

import (
	"flag"
	"fmt"
	"log"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	confirm := flag.Bool("confirm", false, "actually apply the update (default is dry-run)")
	flag.Parse()

	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	log.Println("Connected to database successfully")

	affectedStatuses := []models.GeneralApplicationStatus{
		models.GeneralApplicationStatusAvailable,
		models.GeneralApplicationStatusInterviewing,
	}

	var applications []models.GeneralApplication
	if err := db.
		Where("status IN ?", affectedStatuses).
		Find(&applications).Error; err != nil {
		log.Fatal("Failed to query applications:", err)
	}

	if len(applications) == 0 {
		log.Println("No applications at status \"available\" or \"interviewing\" — nothing to do.")
		return
	}

	log.Printf("Found %d application(s) that predate Team Questions:", len(applications))
	for _, application := range applications {
		claim := ""
		if application.InterviewingByEmail != "" {
			claim = fmt.Sprintf(" (claimed by %s)", application.InterviewingByEmail)
		}
		log.Printf("  %s  %-12s %s %s  <%s>  year=%d%s", application.Id, application.Status, application.FirstName, application.LastName, application.Email, application.ApplicationYear, claim)
	}

	if !*confirm {
		log.Println()
		log.Println("Dry run only — no changes made. Re-run with -confirm to move these to \"pending\" and release any claims.")
		return
	}

	result := db.Model(&models.GeneralApplication{}).
		Where("status IN ?", affectedStatuses).
		Updates(map[string]any{
			"status":                  models.GeneralApplicationStatusPending,
			"interviewing_by_user_id": nil,
			"interviewing_by_email":   "",
		})
	if result.Error != nil {
		log.Fatal("Failed to update applications:", result.Error)
	}

	log.Printf("Moved %d application(s) to \"pending\" and released any claims.", result.RowsAffected)
}
