package database

import (
	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type seedApplication struct {
	firstName    string
	lastName     string
	email        string
	programme    string
	university   string
	graduationYear int
	teams        []string
	interests    []string
	availability string
	contribution string
	status       models.GeneralApplicationStatus
}

var devApplications = []seedApplication{
	{
		firstName:      "Alice",
		lastName:       "Lindström",
		email:          "alice.lindstrom@seed.local",
		programme:      "Machine Learning",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2027,
		teams:          []string{"Development", "Research"},
		interests:      []string{"Machine Learning", "Natural Language Processing"},
		availability:   "6-8 hours",
		contribution:   "I have been building ML pipelines in PyTorch for two years and want to apply that in a collaborative environment.",
		status:         models.GeneralApplicationStatusAvailable,
	},
	{
		firstName:      "Erik",
		lastName:       "Johansson",
		email:          "erik.johansson@seed.local",
		programme:      "Computer Science",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2026,
		teams:          []string{"IT", "Development"},
		interests:      []string{"Cybersecurity & AI Safety Engineering", "Embedded Systems & Edge AI"},
		availability:   "4-6 hours",
		contribution:   "I maintain several open-source Rust projects and want to help build reliable infrastructure for the society.",
		status:         models.GeneralApplicationStatusAvailable,
	},
	{
		firstName:      "Maja",
		lastName:       "Bergström",
		email:          "maja.bergstrom@seed.local",
		programme:      "Industrial Management",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2027,
		teams:          []string{"Business", "Growth"},
		interests:      []string{"Startups & Venture Creation", "Venture Capital & Private Equity"},
		availability:   "8 hours or more",
		contribution:   "I have interned at two VC firms and led the business track of a student startup. I want to help KTHAIS grow its partner network.",
		status:         models.GeneralApplicationStatusAvailable,
	},
	{
		firstName:      "Omar",
		lastName:       "Hassan",
		email:          "omar.hassan@seed.local",
		programme:      "Engineering Physics",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2028,
		teams:          []string{"Research"},
		interests:      []string{"AI Research & Theoretical ML", "Computer Vision & Graphics"},
		availability:   "4-6 hours",
		contribution:   "My thesis explores diffusion models for scientific simulation. I want to communicate this research to a broader student audience.",
		status:         models.GeneralApplicationStatusInterviewing,
	},
	{
		firstName:      "Sofia",
		lastName:       "Karlsson",
		email:          "sofia.karlsson@seed.local",
		programme:      "Data Science",
		university:     "Stockholm University",
		graduationYear: 2026,
		teams:          []string{"Growth", "Business"},
		interests:      []string{"Data Science & Big Data Infrastructure", "Quantitative Finance & Investment"},
		availability:   "6-8 hours",
		contribution:   "I run the social media for a 10k-follower tech community and want to bring that experience to KTHAIS Growth.",
		status:         models.GeneralApplicationStatusIneligible,
	},
}

const (
	devAdminEmail     = "dev-admin@kthais.local"
	devAdminFirstName = "Dev"
	devAdminLastName  = "Admin"
)

// SeedDev upserts a local admin user + profile and prints a ready-to-use JWT.
// It is a no-op when cfg.DevelopmentMode is false.
func SeedDev(db *gorm.DB, cfg *config.Config) {
	if !cfg.DevelopmentMode {
		return
	}

	log.Println("=== [dev seed] running dev admin seed ===")

	// Stable UUID so the seed is idempotent across restarts.
	devUserID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(devAdminEmail))

	user := models.User{
		UserId:   devUserID,
		Email:    devAdminEmail,
		Provider: "dev-seed",
		Roles:    pq.StringArray{models.RoleUser, models.RoleMember, models.RoleAdmin},
	}

	// Upsert: insert or update roles if the row already exists.
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"roles", "provider"}),
	}).Create(&user).Error; err != nil {
		log.Printf("[dev seed] failed to upsert user: %v", err)
		return
	}

	// Reload to get the correct primary key for the profile FK.
	if err := db.Where("email = ?", devAdminEmail).First(&user).Error; err != nil {
		log.Printf("[dev seed] failed to reload user: %v", err)
		return
	}

	profile := models.Profile{
		UserUUID:  devUserID,
		UserId:    user.ID,
		Email:     devAdminEmail,
		FirstName: devAdminFirstName,
		LastName:  devAdminLastName,
	}

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"first_name", "last_name", "user_uuid", "user_id"}),
	}).Create(&profile).Error; err != nil {
		log.Printf("[dev seed] failed to upsert profile: %v", err)
		return
	}

	// Mint a long-lived JWT (30 days) for local testing.
	token, err := utils.WriteJWT(
		devAdminEmail,
		[]string{models.RoleUser, models.RoleMember, models.RoleAdmin},
		devUserID,
		cfg.JwtSigningKey,
		30*24*60, // 30 days in minutes
	)
	if err != nil {
		log.Printf("[dev seed] failed to mint JWT: %v", err)
		return
	}

	log.Println("=== [dev seed] admin user ready ===")
	log.Printf("  email : %s", devAdminEmail)
	log.Printf("  userID: %s", devUserID)
	log.Println("  JWT cookie — paste this in your browser devtools:")
	log.Println("  document.cookie = `jwt=" + token + "; path=/`")
	log.Println("==========================================")

	seedApplications(db)
}

func seedApplications(db *gorm.DB) {
	seeded := 0
	for _, a := range devApplications {
		// Stable ID derived from email so restarts are idempotent.
		appID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("app:"+a.email))

		// Skip if already present.
		var count int64
		db.Model(&models.GeneralApplication{}).Where("id = ?", appID).Count(&count)
		if count > 0 {
			continue
		}

		app := models.GeneralApplication{
			Id:                    appID,
			ApplicationYear:       2026,
			FirstName:             a.firstName,
			LastName:              a.lastName,
			Email:                 a.email,
			EmailNormalized:       a.email,
			Gender:                "Prefer not to say",
			University:            a.university,
			Programme:             a.programme,
			GraduationYear:        a.graduationYear,
			LinkedinURL:           "https://linkedin.com/in/seed-" + appID.String()[:8],
			AdditionalLinks:       pq.StringArray{},
			ResumeFileName:        "resume.pdf",
			ResumeContentType:     "application/pdf",
			Teams:                 pq.StringArray(a.teams),
			TeamPreferencesRanked: true,
			TeamInterestReason:    "",
			Interests:             pq.StringArray(a.interests),
			Availability:          a.availability,
			Contribution:          a.contribution,
			DataRetentionConsent:  true,
			Status:                a.status,
			CreatedAt:             time.Now(),
		}

		if err := db.Create(&app).Error; err != nil {
			log.Printf("[dev seed] failed to create application for %s: %v", a.email, err)
			continue
		}
		seeded++
	}

	if seeded > 0 {
		log.Printf("[dev seed] seeded %d placeholder application(s)", seeded)
	}
}
