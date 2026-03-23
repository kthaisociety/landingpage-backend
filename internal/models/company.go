package models

import (
	"backend/internal/config"
	"backend/internal/utils"
	"log"
	"mime/multipart"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Company struct {
	gorm.Model
	Id          uuid.UUID `gorm:"uniqueIndex" json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Logo        uuid.UUID `json:"logo"` // reference to a blob_data object
}

func NewCompany(cv string, description string, file *multipart.FileHeader, db *gorm.DB, cfg *config.Config) (*Company, error) {
	// read file here
	has_logo := true
	fdata := make([]byte, file.Size)
	f_reader, _ := file.Open()
	nread, err := f_reader.Read(fdata)
	fileparts := strings.Split(file.Filename, ".")
	if err != nil {
		log.Printf("Could not Read logo file: %s\n", err)
		has_logo = false
	}
	if nread != int(file.Size) {
		log.Printf("Read wrong number of bytes Read: %v -- File: %v\n", nread, file.Size)
		has_logo = false
	}
	var comp Company
	if err := db.Where("name = ?", cv).First(&comp).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}

		c_id, _ := uuid.NewUUID()
		var logo_id uuid.UUID
		if has_logo {
			// create logo blob here
			r2, err := utils.InitS3SDK(cfg)
			if err != nil {
				log.Printf("Failed to init r2: %s\n", err)
				return nil, err
			}
			logoBlob, err := NewBlobData(
				fileparts[0],
				fileparts[1],
				c_id,
				fdata,
				db,
				r2,
			)
			if err != nil {
				log.Printf("Failed to create blob data: %s\n", err)
			}
			logo_id = logoBlob.BlobId
		}
		// create new company here
		comp = Company{
			Id:          c_id,
			Name:        cv,
			Description: "",
			Logo:        logo_id,
		}
		if err = db.Create(&comp).Error; err != nil {
			log.Printf("Failed to create company: %s\n", err)
			return nil, err
		}

	} else {
		log.Printf("Found Company %v\n", comp)
	}
	return &comp, nil
}
