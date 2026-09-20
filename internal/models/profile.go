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
	// Team is the recruitment team (see AllBoardRoles' sibling teams list)
	// this member joined through — best-effort auto-populated from their
	// accepted GeneralApplication when their Profile is first created (see
	// resolveTeamFromAcceptedApplication), and freely admin-editable
	// afterward via POST /admin/team/set. Superseded by BoardRole as the
	// member's dashboard label the moment BoardRole is non-empty.
	Team string `gorm:"default:''" json:"team,omitempty"`
	// BoardRole is one of the AllBoardRoles values, or "" for none. Eight of
	// the nine values (everything except BoardRoleBoardAdvisor) are held by
	// exactly one person at a time; the *only* way one of those eight
	// changes hands is a self-service transfer by its current holder
	// (POST /admin/board-role/transfer) — there is no admin grant/revoke/
	// set-for-anyone, deliberately, since BoardRoleHeadOfIT is one of the
	// eight and gates real permissions (see requesterIsHeadOfIT,
	// requesterIsHeadOfTeam, deleteUserAndProfile). A transfer is one
	// atomic operation that clears the sender's BoardRole and sets the
	// recipient's, so holding a non-empty value here structurally *means*
	// "I am the sole holder" — there's no separate headcount invariant to
	// maintain. BoardRoleBoardAdvisor is the one exception: any number of
	// people (including zero) may hold it, managed by plain admin add/
	// remove (POST /admin/board-role/board-advisor/add|remove), same as
	// Team. The very first holder of any of the eight exactly-one roles
	// (including, historically, Head of IT) has to be set with a one-off
	// manual database update — deliberately no bootstrap API shortcut, to
	// avoid a bypass that would only need to exist for the other seven but
	// would be a standing temptation to also (mis)use for Head of IT.
	BoardRole string `gorm:"default:''" json:"board_role,omitempty"`
}

// BoardRole* enumerates every value Profile.BoardRole may hold. See its
// field doc comment for which are exactly-one/transfer-only vs. the one
// multi-holder exception (BoardRoleBoardAdvisor).
const (
	BoardRoleChairperson       = "chairperson"
	BoardRoleViceChairperson   = "vice_chairperson"
	BoardRoleHeadOfIT          = "head_of_it"
	BoardRoleHeadOfBusiness    = "head_of_business"
	BoardRoleHeadOfDevelopment = "head_of_development"
	BoardRoleHeadOfResearch    = "head_of_research"
	BoardRoleHeadOfGrowth      = "head_of_growth"
	BoardRoleBoardAdvisor      = "board_advisor"
	BoardRoleTreasurer         = "treasurer"
)

// AllBoardRoles lists every valid Profile.BoardRole value, for request
// validation.
var AllBoardRoles = []string{
	BoardRoleChairperson,
	BoardRoleViceChairperson,
	BoardRoleHeadOfIT,
	BoardRoleHeadOfBusiness,
	BoardRoleHeadOfDevelopment,
	BoardRoleHeadOfResearch,
	BoardRoleHeadOfGrowth,
	BoardRoleBoardAdvisor,
	BoardRoleTreasurer,
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
