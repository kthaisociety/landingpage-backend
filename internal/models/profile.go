package models

import (
	"time"

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
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID      `gorm:"type:uuid;not null;unique" json:"user_id"`
	User           User           `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"`
	Email          string         `gorm:"uniqueIndex;not null" json:"email"`
	FirstName      string         `gorm:"not null" json:"first_name"`
	LastName       string         `gorm:"not null" json:"last_name"`
	Registered     bool           `gorm:"default:false;not null" json:"registered"`
	University     string         `gorm:"not null" json:"university"`
	Programme      StudyProgram   `gorm:"not null" json:"programme"`
	GraduationYear int            `gorm:"not null" json:"graduation_year"`
	GitHubLink     string         `json:"github_link,omitempty"`
	LinkedInLink   string         `json:"linkedin_link,omitempty"`
	ProfilePicture string         `json:"profile_picture,omitempty"`
	AboutMe        string         `json:"about_me,omitempty"`
	Skills         pq.StringArray `gorm:"type:text[]" json:"skills,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}
