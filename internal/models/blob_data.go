package models

import (
	"backend/internal/utils"
	"log"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BlobId is unique to the blob, and the index it is stored at in R2
// AssociationId is the thing it is associated with, such as an event, project, or user
// Name - just a string, may be useful
// FType - what is it? .png, .jpeg, .zip, etc
type BlobData struct {
	gorm.Model
	BlobId        uuid.UUID `gorm:"uniqueIndex" json:"blob_id"`
	AssociationId uuid.UUID `json:"association_id"`
	Name          string    `json:"name"`
	FType         string    `json:"ftype"`
}

// Get the bd from the database, then call this to get a bytearray that represents the data.
func (bd BlobData) GetData(r2 utils.R2Client) ([]byte, error) {
	///fetch bd.uri from bucket
	return r2.GetObject(bd.BlobId.String())
}

// input name, type, and association id (who it belongs to), actual data as a byte array, the db client and the r2 client
func NewBlobData(name string, ftype string, association_id uuid.UUID, blob []byte, db *gorm.DB, r2 utils.R2Client) (BlobData, error) {
	// create struct
	id, _ := uuid.NewUUID()
	bd := BlobData{
		BlobId:        id,
		AssociationId: association_id,
		Name:          name,
		FType:         ftype,
	}
	// push data to R2
	err := r2.PutObject(id.String(), blob)
	if err != nil {
		return bd, err
	}
	// push to postgresql
	if err := db.Create(&bd).Error; err != nil {
		return bd, err
	}
	return bd, nil
}

func (bd BlobData) DeleteData(blobId *uuid.UUID, db *gorm.DB, r2 utils.R2Client) error {
	if err := r2.DeleteObject(bd.BlobId.String()); err != nil {
		log.Printf("Warning: Failed to delete physical file from R2: %s\n", err)
	}

	if err := db.Where("blob_id=?", blobId).Delete(&bd).Error; err != nil {
		return err
	}

	return nil
}
