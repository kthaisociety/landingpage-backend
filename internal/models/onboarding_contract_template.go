package models

import (
	"fmt"

	"backend/internal/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OnboardingContractTemplate is a singleton row (mirrors the singleton
// pattern onboarding-service's own OnboardingEmailSettings uses) holding
// whichever membership-contract file an admin has most recently uploaded —
// the file onboarding-service's contract email links members to (see
// OnboardingHandler.GetContractTemplate). Same dual-storage split as
// GeneralApplication's resume upload (see shouldStoreResumeInDatabase in
// general_application_handler.go): Data is only set in DevelopmentMode, so
// local dev never needs real R2 credentials just to exercise this feature;
// everywhere else the bytes live in R2 via BlobID, and Data stays empty.
type OnboardingContractTemplate struct {
	gorm.Model
	FileName       string     `gorm:"not null;default:''"`
	ContentType    string     `gorm:"not null;default:''"`
	Data           []byte     `gorm:"type:bytea"`
	BlobID         *uuid.UUID `json:"blob_id"`
	UpdatedByEmail string     `gorm:"not null;default:''"`
}

// LoadOnboardingContractTemplate returns the current singleton row, or a
// zero-valued (ID == 0) struct if none has ever been uploaded — same
// no-row-yet convention as onboarding-service's own emailcontent.Load,
// callers check ID == 0 rather than treating "not found" as an error.
func LoadOnboardingContractTemplate(db *gorm.DB) (OnboardingContractTemplate, error) {
	var t OnboardingContractTemplate
	err := db.Order("id desc").First(&t).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return t, err
	}
	return t, nil
}

// GetData returns the uploaded file's bytes, from Postgres (DevelopmentMode
// uploads) or R2 (everywhere else) depending on which one it was stored in
// — see the struct's own doc comment.
func (t OnboardingContractTemplate) GetData(r2 utils.R2Client) ([]byte, error) {
	if len(t.Data) > 0 {
		return t.Data, nil
	}
	if t.BlobID == nil {
		return nil, fmt.Errorf("contract template has no stored data")
	}
	return r2.GetObject(t.BlobID.String())
}
