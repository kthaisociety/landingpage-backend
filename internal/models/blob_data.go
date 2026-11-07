package models

import (
	"backend/internal/utils"
	"log"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BlobId is unique to the blob, and the key it is stored under in the bucket
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
func (bd BlobData) GetData(store utils.BlobStore) ([]byte, error) {
	///fetch bd.uri from bucket
	return store.GetObject(bd.BlobId.String())
}

// input name, type, and association id (who it belongs to), actual data as a byte array, the db client and the blob store
func NewBlobData(name string, ftype string, association_id uuid.UUID, blob []byte, db *gorm.DB, store utils.BlobStore) (BlobData, error) {
	// create struct
	id, _ := uuid.NewUUID()
	bd := BlobData{
		BlobId:        id,
		AssociationId: association_id,
		Name:          name,
		FType:         ftype,
	}
	// push data to the bucket
	err := store.PutObject(id.String(), blob)
	if err != nil {
		return bd, err
	}
	// push to postgresql
	if err := db.Create(&bd).Error; err != nil {
		return bd, err
	}
	return bd, nil
}

func (bd BlobData) DeleteData(blobId *uuid.UUID, db *gorm.DB, store utils.BlobStore) error {
	if err := store.DeleteObject(bd.BlobId.String()); err != nil {
		log.Printf("Warning: Failed to delete physical file from the bucket: %s\n", err)
	}

	if err := db.Where("blob_id=?", blobId).Delete(&bd).Error; err != nil {
		return err
	}

	return nil
}
