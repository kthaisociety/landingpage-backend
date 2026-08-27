package database

import (
	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// devQuestionID derives a stable UUID for a seeded team question from a
// human-readable slug, the same way devUserID/appID are derived elsewhere in
// this file — so re-running the seed is idempotent and the answer maps below
// can reference a question's id before it's actually been created.
func devQuestionID(team, slug string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("teamquestion:"+team+":"+slug))
}

type devTeamQuestionSeed struct {
	slug     string
	text     string
	required bool
}

// devTeamQuestions seeds a few starter questions per team in local dev only —
// production ships with an empty team_questions table; team heads populate
// their own from the new admin UI (Team Questions -> manage questions).
var devTeamQuestions = map[string][]devTeamQuestionSeed{
	"Business": {
		{slug: "motivation", text: "Why are you interested in the Business team specifically?", required: true},
		{slug: "experience", text: "Describe any relevant experience in business development, marketing, or partnerships.", required: true},
	},
	"Development": {
		{slug: "motivation", text: "Why are you interested in the Development team specifically?", required: true},
		{slug: "stack", text: "What programming languages or frameworks are you most comfortable with?", required: true},
	},
	"Research": {
		{slug: "motivation", text: "Why are you interested in the Research team specifically?", required: true},
		{slug: "background", text: "Describe your background in machine learning or a related research area.", required: true},
	},
	"Growth": {
		{slug: "motivation", text: "Why are you interested in the Growth team specifically?", required: true},
		{slug: "experience", text: "Describe any relevant experience in marketing, content, or community building.", required: true},
	},
	"IT": {
		{slug: "motivation", text: "Why are you interested in the IT team specifically?", required: true},
		{slug: "stack", text: "What infrastructure, DevOps, or systems experience do you have?", required: true},
	},
}

func seedTeamQuestions(db *gorm.DB) {
	seeded := 0
	for team, questions := range devTeamQuestions {
		for i, q := range questions {
			id := devQuestionID(team, q.slug)
			var count int64
			db.Model(&models.TeamQuestion{}).Where("id = ?", id).Count(&count)
			if count > 0 {
				continue
			}
			question := models.TeamQuestion{
				Id:        id,
				Team:      team,
				Text:      q.text,
				Required:  q.required,
				SortOrder: i,
			}
			if err := db.Create(&question).Error; err != nil {
				log.Printf("[dev seed] failed to seed team question %s/%s: %v", team, q.slug, err)
				continue
			}
			seeded++
		}
	}
	if seeded > 0 {
		log.Printf("[dev seed] seeded %d team question(s)", seeded)
	}
}

// seedTeamQuestionsSubmission describes a completed Team Questions submission
// to attach to a seed application. Teams not listed in answers but present on
// the application are treated as withdrawn, mirroring what the real submit
// endpoint does.
type seedTeamQuestionsSubmission struct {
	answers        map[string]map[string]string
	withdrawnTeams []string
}

type seedApplication struct {
	firstName      string
	lastName       string
	email          string
	programme      string
	university     string
	graduationYear int
	teams          []string
	interests      []string
	availability   string
	contribution   string
	status         models.GeneralApplicationStatus

	// teamQuestions is nil for applications that never got past the initial
	// screen (still pending, or marked ineligible before Team Questions).
	teamQuestions *seedTeamQuestionsSubmission
	// issueUnusedInvite mints a fresh, usable Team Questions link and logs it
	// every time the seed runs, regardless of whether the application row
	// already existed — a resend naturally supersedes the previous token, so
	// this is always safe and always gives you a live link to test against.
	issueUnusedInvite bool
	// claimedByDevAdmin points InterviewingByUserID/Email at the seeded dev
	// admin, so "interviewing" applications show up under "my interviews".
	claimedByDevAdmin bool
}

// devApplications covers the full lifecycle so the admin UI has something to
// show for every stage without manual setup:
//   - Alice: pending, never invited (Send to all pending picks her up)
//   - Erik: pending, invited but hasn't submitted (Resend / the public form)
//   - Maja: available — submitted Team Questions, withdrew from one team
//   - Omar: interviewing — submitted, then claimed by the dev admin
//   - Sofia: ineligible before ever being invited to Team Questions
//   - Nadia: ineligible after already submitting (restore should send her to
//     available, not pending)
//   - Viktor: withdrawn — opted out of every team on the Team Questions form
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
		status:         models.GeneralApplicationStatusPending,
	},
	{
		firstName:         "Erik",
		lastName:          "Johansson",
		email:             "erik.johansson@seed.local",
		programme:         "Computer Science",
		university:        "KTH Royal Institute of Technology",
		graduationYear:    2026,
		teams:             []string{"IT", "Development"},
		interests:         []string{"Cybersecurity & AI Safety Engineering", "Embedded Systems & Edge AI"},
		availability:      "4-6 hours",
		contribution:      "I maintain several open-source Rust projects and want to help build reliable infrastructure for the society.",
		status:            models.GeneralApplicationStatusPending,
		issueUnusedInvite: true,
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
		teamQuestions: &seedTeamQuestionsSubmission{
			answers: map[string]map[string]string{
				"Business": {
					devQuestionID("Business", "motivation").String(): "I want to own partner relationships end-to-end, not just support them.",
					devQuestionID("Business", "experience").String(): "Interned at two VC firms and led the business track of a student startup.",
				},
			},
			withdrawnTeams: []string{"Growth"},
		},
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
		teamQuestions: &seedTeamQuestionsSubmission{
			answers: map[string]map[string]string{
				"Research": {
					devQuestionID("Research", "motivation").String(): "I want feedback from peers outside my lab before publishing.",
					devQuestionID("Research", "background").String(): "Thesis work on diffusion models for scientific simulation.",
				},
			},
		},
		claimedByDevAdmin: true,
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
	{
		firstName:      "Nadia",
		lastName:       "Petrov",
		email:          "nadia.petrov@seed.local",
		programme:      "Information and Communication Technology",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2027,
		teams:          []string{"IT"},
		interests:      []string{"Embedded Systems & Edge AI"},
		availability:   "4-6 hours",
		contribution:   "I've run a homelab for three years and want hands-on infra experience with a real user base.",
		status:         models.GeneralApplicationStatusIneligible,
		teamQuestions: &seedTeamQuestionsSubmission{
			answers: map[string]map[string]string{
				"IT": {
					devQuestionID("IT", "motivation").String(): "I want production experience, not just homelab tinkering.",
					devQuestionID("IT", "stack").String():      "Proxmox, Docker, a bit of Terraform.",
				},
			},
		},
	},
	{
		firstName:      "Viktor",
		lastName:       "Nilsson",
		email:          "viktor.nilsson@seed.local",
		programme:      "Technology and Management",
		university:     "KTH Royal Institute of Technology",
		graduationYear: 2026,
		teams:          []string{"Development", "Growth"},
		interests:      []string{"Startups & Venture Creation"},
		availability:   "4-6 hours",
		contribution:   "Applying broadly while I figure out which side of the society fits me best.",
		status:         models.GeneralApplicationStatusWithdrawn,
		teamQuestions: &seedTeamQuestionsSubmission{
			answers:        map[string]map[string]string{},
			withdrawnTeams: []string{"Development", "Growth"},
		},
	},
}

const (
	devAdminEmail     = "dev-admin@kthais.local"
	devAdminFirstName = "Dev"
	devAdminLastName  = "Admin"
)

type devTeamMembershipSeed struct {
	department   string
	role         string
	academicYear string
}

type devTeamMemberSeed struct {
	email     string
	firstName string
	lastName  string
	adminTeam string
	entries   []devTeamMembershipSeed
}

// devTeamMembers seeds a handful of other admins into team_members (on top of
// the single dev-admin user above), so the shared-notes @mention directory
// has more than one person to test locally — including Ludvig, who
// deliberately has two membership rows across different teams/years, since
// that's the shape that caused the mention-autocomplete duplicate bug (one
// person, multiple team_members rows, same email).
var devTeamMembers = []devTeamMemberSeed{
	{
		email:     "ludvig@kthais.local",
		firstName: "Ludvig",
		lastName:  "Ek",
		adminTeam: "Development",
		entries: []devTeamMembershipSeed{
			{department: "Development", role: "Head of Development", academicYear: "2025/2026"},
			{department: "IT", role: "Member", academicYear: "2024/2025"},
		},
	},
	{
		email:     "sam@kthais.local",
		firstName: "Sam",
		lastName:  "Berg",
		adminTeam: "Growth",
		entries: []devTeamMembershipSeed{
			{department: "Growth", role: "Head of Growth", academicYear: "2025/2026"},
		},
	},
	{
		email:     "maja.admin@kthais.local",
		firstName: "Maja",
		lastName:  "Sund",
		adminTeam: "Business",
		entries: []devTeamMembershipSeed{
			{department: "Business", role: "Head of Business", academicYear: "2025/2026"},
		},
	},
}

// seedDevTeamMembers upserts each devTeamMembers person as a user + profile,
// gives the existing dev-admin a team_members row too (they had none before,
// so they never showed up in their own mention directory), and creates every
// listed team_members row — skipping any (user, department, academic_year)
// combination that already exists, so re-running the seed on restart never
// duplicates rows itself.
func seedDevTeamMembers(db *gorm.DB) {
	created := 0

	createMembership := func(userID uint, m devTeamMembershipSeed) {
		var count int64
		db.Model(&models.TeamMember{}).
			Where("user_id = ? AND team_member_department = ? AND academic_year = ?", userID, m.department, m.academicYear).
			Count(&count)
		if count > 0 {
			return
		}
		member := models.TeamMember{
			UserID:               userID,
			TeamMemberRole:       m.role,
			TeamMemberDepartment: m.department,
			AcademicYear:         m.academicYear,
		}
		if err := db.Create(&member).Error; err != nil {
			log.Printf("[dev seed] failed to seed team member row (user %d, %s): %v", userID, m.department, err)
			return
		}
		created++
	}

	// The dev-admin user/profile already exist (upserted above) — just add
	// its team_members row.
	var devAdminUser models.User
	if err := db.Where("email = ?", devAdminEmail).First(&devAdminUser).Error; err != nil {
		log.Printf("[dev seed] failed to reload dev admin for team member seed: %v", err)
	} else {
		createMembership(devAdminUser.ID, devTeamMembershipSeed{
			department:   "IT",
			role:         "Head of IT",
			academicYear: "2025/2026",
		})
	}

	for _, person := range devTeamMembers {
		userID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(person.email))
		user := models.User{
			UserId:   userID,
			Email:    person.email,
			Provider: "dev-seed",
			Roles:    pq.StringArray{models.RoleUser, models.RoleMember, models.RoleAdmin},
		}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "email"}},
			DoUpdates: clause.AssignmentColumns([]string{"roles", "provider"}),
		}).Create(&user).Error; err != nil {
			log.Printf("[dev seed] failed to upsert team member user %s: %v", person.email, err)
			continue
		}
		if err := db.Where("email = ?", person.email).First(&user).Error; err != nil {
			log.Printf("[dev seed] failed to reload team member user %s: %v", person.email, err)
			continue
		}

		profile := models.Profile{
			UserUUID:  userID,
			UserId:    user.ID,
			Email:     person.email,
			FirstName: person.firstName,
			LastName:  person.lastName,
			AdminTeam: person.adminTeam,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "email"}},
			DoUpdates: clause.AssignmentColumns([]string{"first_name", "last_name", "user_uuid", "user_id", "admin_team"}),
		}).Create(&profile).Error; err != nil {
			log.Printf("[dev seed] failed to upsert team member profile %s: %v", person.email, err)
			continue
		}

		for _, m := range person.entries {
			createMembership(user.ID, m)
		}
	}

	if created > 0 {
		log.Printf("[dev seed] seeded %d team member row(s)", created)
	}
}

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
		// IT so the seeded admin can exercise the IT-only Team Questions
		// template editor locally without extra setup.
		AdminTeam: "IT",
	}

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"first_name", "last_name", "user_uuid", "user_id", "admin_team"}),
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

	// No need to seed team_questions_settings — TeamQuestionsHandler.getSettings
	// already falls back to a sensible default template when none is saved,
	// in every environment, so there's nothing dev-specific to seed here.
	seedTeamQuestions(db)
	seedApplications(db, cfg, devUserID)
	seedDevTeamMembers(db)
}

func seedApplications(db *gorm.DB, cfg *config.Config, devUserID uuid.UUID) {
	seeded := 0
	for _, a := range devApplications {
		// Stable ID derived from email so restarts are idempotent.
		appID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("app:"+a.email))

		if a.issueUnusedInvite {
			issueDevTeamQuestionsInvite(db, cfg, appID, a.email)
		}

		// Skip if already present.
		var count int64
		db.Model(&models.GeneralApplication{}).Where("id = ?", appID).Count(&count)
		if count > 0 {
			continue
		}

		effectiveTeams := a.teams
		if a.teamQuestions != nil {
			effectiveTeams = withdrawTeams(a.teams, a.teamQuestions.withdrawnTeams)
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
			Teams:                 pq.StringArray(effectiveTeams),
			TeamPreferencesRanked: true,
			TeamInterestReason:    "",
			Interests:             pq.StringArray(a.interests),
			Availability:          a.availability,
			Contribution:          a.contribution,
			DataRetentionConsent:  true,
			Status:                a.status,
			CreatedAt:             time.Now(),
		}

		if a.claimedByDevAdmin {
			app.InterviewingByUserID = &devUserID
			app.InterviewingByEmail = devAdminEmail
		}

		if err := db.Create(&app).Error; err != nil {
			log.Printf("[dev seed] failed to create application for %s: %v", a.email, err)
			continue
		}

		if a.teamQuestions != nil {
			answersJSON, err := json.Marshal(a.teamQuestions.answers)
			if err != nil {
				log.Printf("[dev seed] failed to encode team questions answers for %s: %v", a.email, err)
			} else {
				submission := models.TeamQuestionsSubmission{
					ApplicationID:  appID,
					Answers:        string(answersJSON),
					WithdrawnTeams: pq.StringArray(a.teamQuestions.withdrawnTeams),
					SubmittedAt:    time.Now(),
				}
				if err := db.Create(&submission).Error; err != nil {
					log.Printf("[dev seed] failed to create team questions submission for %s: %v", a.email, err)
				}
			}
		}

		seeded++
	}

	if seeded > 0 {
		log.Printf("[dev seed] seeded %d placeholder application(s)", seeded)
	}
}

func withdrawTeams(teams []string, withdrawn []string) []string {
	withdrawnSet := make(map[string]struct{}, len(withdrawn))
	for _, team := range withdrawn {
		withdrawnSet[team] = struct{}{}
	}
	// Not nil: a nil slice serializes as SQL NULL via pq.StringArray, which
	// violates teams' NOT NULL constraint when every team is withdrawn (as
	// with the Viktor seed fixture, who withdraws from all his teams).
	remaining := []string{}
	for _, team := range teams {
		if _, ok := withdrawnSet[team]; !ok {
			remaining = append(remaining, team)
		}
	}
	return remaining
}

// issueDevTeamQuestionsInvite mints a fresh token and logs the link every time
// the seed runs — a resend naturally supersedes whatever token existed
// before, so this is safe to repeat on every restart and always gives you a
// live link to open locally, even for an application seeded long ago.
func issueDevTeamQuestionsInvite(db *gorm.DB, cfg *config.Config, appID uuid.UUID, email string) {
	raw, hash, err := utils.GenerateToken()
	if err != nil {
		log.Printf("[dev seed] failed to generate team questions token for %s: %v", email, err)
		return
	}

	token := models.TeamQuestionsToken{
		ApplicationID: appID,
		TokenHash:     hash,
		ExpiresAt:     time.Now().Add(30 * 24 * time.Hour),
	}
	if err := db.Create(&token).Error; err != nil {
		log.Printf("[dev seed] failed to create team questions token for %s: %v", email, err)
		return
	}

	log.Printf("[dev seed] team questions link for %s (valid until restart supersedes it):", email)
	log.Printf("  %s/apply/team-questions/%s", cfg.FrontendURL, raw)
}
