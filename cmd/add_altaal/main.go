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

	// Create Altaal company
	company := models.Company{
		Id:          uuid.New(),
		Name:        "Altaal Advisory",
		Description: "Altaal Advisory / Nordic Credit Partners (NCP) - Transforming modern portfolio management with AI-driven infrastructure.",
	}

	// Check if company already exists
	var existingCompany models.Company
	if err := db.Where("name = ?", company.Name).First(&existingCompany).Error; err == nil {
		log.Printf("Company %s already exists, using existing ID", company.Name)
		company.Id = existingCompany.Id
	} else {
		if err := db.Create(&company).Error; err != nil {
			log.Printf("Failed to create company %s: %v", company.Name, err)
		} else {
			log.Printf("Created company: %s", company.Name)
		}
	}

	// Create job listing
	endDate := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	now := time.Now()

	jobListing := models.JobListing{
		Id:   uuid.New(),
		Name: "AI/Data Investment Manager",
		Description: `Join Altaal: Future of AI-Driven Portfolio Management

Do you want to build products at the intersection of AI, data, and real-world decision-making? At Altaal / Nordic Credit Partners (NCP), we're transforming how modern portfolio management works, building an AI-driven infrastructure that elevates analysis, automates workflows, and empowers an investment team with over 40 years of experience.

We're now hiring for a key role on our growing technology and investment operations team.

What You'll Do:
• Investigate, design, and document a digital strategy for our investment and portfolio-management processes
• Test and implement AI tools and multi-agent systems (e.g., automated data collection, sentiment analysis, risk modeling)
• Identify bottlenecks and build solutions in close collaboration with the portfolio team
• Evaluate external systems and potential technology partners
• Document processes, insights, and results to support team adoption of new tools

Who You Are:
You're a curious and driven student, researcher, or early-career engineer who thrives in environments where you can design, test, and implement real, production-level solutions. An interest in asset management, finance, and company building is a plus, but not required.

Duration: Flexible part-time role (10–20 hours/week) that can easily be combined with studies. Start date by agreement.`,
		Salary:      "Competitive compensation",
		Location:    "Regeringsgatan 59, Stockholm (Hybrid)",
		JobType:     "Part-time",
		CompanyId:   company.Id,
		StartDate:   now,
		EndDate:     endDate,
		AppUrl:      "mailto:hr@altaal.com",
		ContactInfo: "hr@altaal.com",
	}

	// Check if job already exists
	var existingJob models.JobListing
	if err := db.Where("name = ? AND company_id = ?", jobListing.Name, jobListing.CompanyId).First(&existingJob).Error; err == nil {
		log.Printf("Job listing '%s' already exists, skipping", jobListing.Name)
	} else {
		if err := db.Create(&jobListing).Error; err != nil {
			log.Printf("Failed to create job listing %s: %v", jobListing.Name, err)
		} else {
			log.Printf("✅ Created job listing: %s at %s", jobListing.Name, company.Name)
		}
	}

	log.Println("✅ Altaal job posting added successfully!")
}
