package main

import (
	"fmt"
	"log"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// Load environment variables
	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
	}

	// Load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// Initialize DB
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.Port, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	log.Println("Connected to database successfully")

	// Create test companies
	companies := []models.Company{
		{
			Id:          uuid.New(),
			Name:        "Spotify",
			Description: "Spotify is a digital music service that gives you access to millions of songs.",
		},
		{
			Id:          uuid.New(),
			Name:        "Klarna",
			Description: "Klarna is a Swedish fintech company that provides online financial services.",
		},
		{
			Id:          uuid.New(),
			Name:        "Ericsson",
			Description: "Ericsson is a multinational networking and telecommunications company.",
		},
		{
			Id:          uuid.New(),
			Name:        "King",
			Description: "King is a leading interactive entertainment company for the mobile world, with a focus on mobile games.",
		},
		{
			Id:          uuid.New(),
			Name:        "Volvo",
			Description: "Volvo is a Swedish multinational manufacturing company known for cars, trucks, buses and construction equipment.",
		},
	}

	log.Println("Creating companies...")
	for i, company := range companies {
		// Check if company already exists
		var existingCompany models.Company
		if err := db.Where("name = ?", company.Name).First(&existingCompany).Error; err == nil {
			log.Printf("Company %s already exists, using existing ID", company.Name)
			companies[i].Id = existingCompany.Id
			continue
		}

		if err := db.Create(&company).Error; err != nil {
			log.Printf("Failed to create company %s: %v", company.Name, err)
			continue
		}
		log.Printf("Created company: %s", company.Name)
	}

	// Create test job listings
	now := time.Now()
	jobListings := []models.JobListing{
		{
			Id:          uuid.New(),
			Name:        "Senior Software Engineer - Backend",
			Description: "We're looking for an experienced backend engineer to join our Platform team. You'll work on building scalable microservices using Go and Kubernetes.",
			Salary:      "60,000 - 75,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Full-time",
			CompanyId:   companies[0].Id, // Spotify
			StartDate:   now,
			EndDate:     now.AddDate(0, 2, 0), // 2 months from now
			AppUrl:      "https://www.spotify.com/careers",
			ContactInfo: "careers@spotify.com",
		},
		{
			Id:          uuid.New(),
			Name:        "Frontend Developer Intern",
			Description: "Join our design system team and help build beautiful, accessible UI components using React and TypeScript.",
			Salary:      "25,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Internship",
			CompanyId:   companies[1].Id,      // Klarna
			StartDate:   now.AddDate(0, 1, 0), // 1 month from now
			EndDate:     now.AddDate(0, 6, 0), // 6 months from now
			AppUrl:      "https://www.klarna.com/careers",
			ContactInfo: "recruitment@klarna.com",
		},
		{
			Id:          uuid.New(),
			Name:        "Machine Learning Engineer",
			Description: "Work on cutting-edge AI/ML projects in telecommunications. Experience with TensorFlow or PyTorch required.",
			Salary:      "65,000 - 80,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Full-time",
			CompanyId:   companies[2].Id, // Ericsson
			StartDate:   now,
			EndDate:     now.AddDate(0, 3, 0), // 3 months from now
			AppUrl:      "https://www.ericsson.com/en/careers",
			ContactInfo: "talent@ericsson.com",
		},
		{
			Id:          uuid.New(),
			Name:        "Game Developer - Unity",
			Description: "Create engaging mobile games played by millions worldwide. Experience with Unity and C# is essential.",
			Salary:      "55,000 - 70,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Full-time",
			CompanyId:   companies[3].Id, // King
			StartDate:   now,
			EndDate:     now.AddDate(0, 2, 0),
			AppUrl:      "https://careers.king.com",
			ContactInfo: "jobs@king.com",
		},
		{
			Id:          uuid.New(),
			Name:        "Summer Internship - Data Science",
			Description: "Work with our data science team on autonomous driving projects. Perfect for students passionate about AI and automotive technology.",
			Salary:      "28,000 SEK/month",
			Location:    "Gothenburg, Sweden",
			JobType:     "Internship",
			CompanyId:   companies[4].Id, // Volvo
			StartDate:   time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			EndDate:     time.Date(2025, 8, 31, 0, 0, 0, 0, time.UTC),
			AppUrl:      "https://www.volvogroup.com/careers",
			ContactInfo: "hr@volvo.com",
		},
		{
			Id:          uuid.New(),
			Name:        "DevOps Engineer",
			Description: "Help us build and maintain our cloud infrastructure on AWS. Experience with Terraform and Kubernetes is a plus.",
			Salary:      "60,000 - 75,000 SEK/month",
			Location:    "Stockholm, Sweden (Hybrid)",
			JobType:     "Full-time",
			CompanyId:   companies[0].Id, // Spotify
			StartDate:   now,
			EndDate:     now.AddDate(0, 3, 0),
			AppUrl:      "https://www.spotify.com/careers",
			ContactInfo: "careers@spotify.com",
		},
		{
			Id:          uuid.New(),
			Name:        "Product Manager - Payments",
			Description: "Lead product strategy for our payment solutions used by millions of merchants worldwide.",
			Salary:      "70,000 - 90,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Full-time",
			CompanyId:   companies[1].Id, // Klarna
			StartDate:   now,
			EndDate:     now.AddDate(0, 2, 0),
			AppUrl:      "https://www.klarna.com/careers",
			ContactInfo: "recruitment@klarna.com",
		},
		{
			Id:          uuid.New(),
			Name:        "iOS Developer",
			Description: "Build features for our award-winning mobile game apps. Swift and SwiftUI experience required.",
			Salary:      "58,000 - 72,000 SEK/month",
			Location:    "Stockholm, Sweden",
			JobType:     "Full-time",
			CompanyId:   companies[3].Id, // King
			StartDate:   now,
			EndDate:     now.AddDate(0, 2, 0),
			AppUrl:      "https://careers.king.com",
			ContactInfo: "jobs@king.com",
		},
	}

	log.Println("Creating job listings...")
	for _, job := range jobListings {
		// Check if job already exists by title and company
		var existingJob models.JobListing
		if err := db.Where("name = ? AND company_id = ?", job.Name, job.CompanyId).First(&existingJob).Error; err == nil {
			log.Printf("Job listing '%s' already exists, skipping", job.Name)
			continue
		}

		if err := db.Create(&job).Error; err != nil {
			log.Printf("Failed to create job listing %s: %v", job.Name, err)
			continue
		}
		log.Printf("Created job listing: %s at %s", job.Name, getCompanyName(companies, job.CompanyId))
	}

	log.Println("✅ Seed data created successfully!")
	log.Printf("Created %d companies and %d job listings", len(companies), len(jobListings))
}

func getCompanyName(companies []models.Company, id uuid.UUID) string {
	for _, c := range companies {
		if c.Id == id {
			return c.Name
		}
	}
	return "Unknown"
}
