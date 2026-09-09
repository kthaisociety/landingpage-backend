package models

import (
	"fmt"

	"backend/internal/utils"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type StudyProgram string

const (
	StudyProgramMachineLearning                 StudyProgram = "Machine Learning"
	StudyProgramAppliedMathematics              StudyProgram = "Applied Mathematics"
	StudyProgramBioTechnology                   StudyProgram = "Bio Technology"
	StudyProgramEngineeringPhysics              StudyProgram = "Engineering Physics"
	StudyProgramComputerScience                 StudyProgram = "Computer Science"
	StudyProgramElectricalEngineering           StudyProgram = "Electrical Engineering"
	StudyProgramIndustrialManagement            StudyProgram = "Industrial Management"
	StudyProgramInformationAndCommunicationTech StudyProgram = "Information and Communication Technology"
	StudyProgramChemicalScienceAndEngineering   StudyProgram = "Chemical Science and Engineering"
	StudyProgramMechanicalEngineering           StudyProgram = "Mechanical Engineering"
	StudyProgramMathematics                     StudyProgram = "Mathematics"
	StudyProgramMaterialScienceAndEngineering   StudyProgram = "Material Science and Engineering"
	StudyProgramMedicalEngineering              StudyProgram = "Medical Engineering"
	StudyProgramEnvironmentalEngineering        StudyProgram = "Environmental Engineering"
	StudyProgramTheBuiltEnvironment             StudyProgram = "The Built Environment"
	StudyProgramTechnologyAndEconomics          StudyProgram = "Technology and Economics"
	StudyProgramTechnologyAndHealth             StudyProgram = "Technology and Health"
	StudyProgramTechnologyAndLearning           StudyProgram = "Technology and Learning"
	StudyProgramTechnologyAndManagement         StudyProgram = "Technology and Management"
)

type Profile struct {
	gorm.Model
	Id uuid.UUID `gorm:"uniqueIndex;default:gen_random_uuid()" json:"id"`
	// Slug is the public, human-readable identifier used in profile URLs
	// (e.g. /members/timothy-lindblom) instead of Id. It's nullable at the DB
	// level — despite the uniqueIndex, Postgres allows any number of NULLs in
	// a unique column — so existing rows stay valid until
	// database.BackfillProfileSlugs assigns one on the next boot; every new
	// profile gets one immediately via BeforeCreate below.
	Slug                   string         `gorm:"uniqueIndex" json:"slug,omitempty"`
	UserUUID               uuid.UUID      `gorm:"not null" json:"user_id"`
	UserId                 uint           `gorm:"not null" json:"-"`
	User                   User           `json:"user,omitempty"`
	Email                  string         `gorm:"uniqueIndex;not null" json:"email"`
	FirstName              string         `gorm:"not null" json:"first_name"`
	LastName               string         `gorm:"not null" json:"last_name"`
	Registered             bool           `gorm:"default:false;not null" json:"registered"`
	University             string         `gorm:"not null" json:"university"`
	Programme              StudyProgram   `gorm:"not null" json:"programme"`
	GraduationYear         int            `gorm:"not null" json:"graduation_year"`
	GitHubLink             string         `json:"github_link,omitempty"`
	LinkedInLink           string         `json:"linkedin_link,omitempty"`
	ProfilePicture         string         `json:"profile_picture,omitempty"`
	AboutMe                string         `json:"about_me,omitempty"`
	Skills                 pq.StringArray `gorm:"type:text[]" json:"skills,omitempty"`
	BookingPageURL         string         `gorm:"default:''" json:"booking_page_url,omitempty"`
	InterviewEmailTemplate string         `gorm:"type:text;default:''" json:"interview_email_template,omitempty"`
	AdminTeam              string         `gorm:"default:''" json:"admin_team,omitempty"`
	// IsHeadOfIT gates the single most destructive class of admin action
	// (permanently deleting a member's real Google Workspace + Mattermost
	// account). Deliberately separate from the self-editable AdminTeam field
	// above and NOT settable via UpdateInterviewSettings or any other
	// self-service endpoint — the only way it changes hands is
	// OffboardingHandler.TransferHeadOfIT, which flips it on the new holder
	// and off the old one in the same transaction, so there is always
	// exactly one Head of IT. The very first holder has to be set with a
	// one-off manual database update; every handover after that goes
	// through the app.
	IsHeadOfIT bool `gorm:"default:false" json:"is_head_of_it,omitempty"`
}

// BeforeCreate assigns a URL-safe slug derived from the profile's name if
// one wasn't already set, resolving collisions by appending "-2", "-3", ....
// This runs for every insertion path — the profile handlers, test fixtures,
// admin tooling, anything that calls db.Create — not just call sites that
// remember to set Slug explicitly, which a bare uniqueIndex column can't
// enforce on its own (a second empty/duplicate slug would otherwise 500 on
// the unique constraint instead of getting a real one).
func (p *Profile) BeforeCreate(tx *gorm.DB) error {
	if p.Slug != "" {
		return nil
	}

	base := utils.Slugify(p.FirstName + " " + p.LastName)
	if base == "" {
		base = "member"
	}

	slug := base
	for suffix := 2; ; suffix++ {
		var count int64
		if err := tx.Model(&Profile{}).Where("slug = ?", slug).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, suffix)
	}

	p.Slug = slug
	return nil
}
