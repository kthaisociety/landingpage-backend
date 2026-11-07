package models

import (
	"backend/internal/config"
	"backend/internal/utils"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// this test requires infra that is not in the test env
func TestBlob(t *testing.T) {
	file := "../../.env"
	loaded := false
	if _, err := os.Stat(file); err == nil {
		if err := godotenv.Load(file); err != nil {
			t.Fatalf("Could not load env file: %s\n", err)
		}
		log.Println("Loaded .env file")
		loaded = true
	}
	if !loaded {
		return
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Error Loading Config: %s\n", err)
	}
	store, err := utils.InitS3SDK(cfg)
	if err != nil {
		t.Fatalf("Failed to init blob store: %s\n", err)
	}
	// Initialize DB
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	// Auto migrate the schema
	_ = db.AutoMigrate(
		&BlobData{},
	)
	name := "test_file"
	ftype := ".txt"
	auid, _ := uuid.NewUUID()
	bd, err := NewBlobData(name, ftype, auid, []byte("asdfasdfasdfasdf"), db, store)
	if err != nil {
		t.Fatalf("Error creating Blob Data: %s\n", err)
	}
	data, err := bd.GetData(store)
	if err != nil {
		t.Fatalf("Failed to load blob data: %s\n", err)
	}
	log.Printf("Blob Data: %s\n", data)

}
