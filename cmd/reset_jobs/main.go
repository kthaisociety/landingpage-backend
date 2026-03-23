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

	// Delete all existing job listings and companies
	log.Println("🗑️  Clearing all existing job listings and companies...")
	db.Unscoped().Where("1 = 1").Delete(&models.JobListing{})
	db.Unscoped().Where("1 = 1").Delete(&models.Company{})
	log.Println("✅ Cleared all existing data")

	// Create new companies
	companies := []models.Company{
		{
			Id:          uuid.New(),
			Name:        "Vantir AB",
			Description: "Vantir Foundry gives you the chance to join a team, build from idea to product to market, and earn equity. Experience from Amazon, Avanza, and Netlight.",
		},
		{
			Id:          uuid.New(),
			Name:        "Blenda Labs",
			Description: "Reimagining how movies are made, combining cutting-edge AI tools with creative talent to produce high-quality, cost-effective video at scale.",
		},
		{
			Id:          uuid.New(),
			Name:        "Andon Labs",
			Description: "Collaborating with Google DeepMind on AI research projects.",
		},
		{
			Id:          uuid.New(),
			Name:        "Altaal Advisory",
			Description: "Altaal Advisory / Nordic Credit Partners (NCP) - Transforming modern portfolio management with AI-driven infrastructure.",
		},
	}

	log.Println("Creating companies...")
	for _, company := range companies {
		if err := db.Create(&company).Error; err != nil {
			log.Printf("Failed to create company %s: %v", company.Name, err)
		} else {
			log.Printf("Created company: %s", company.Name)
		}
	}

	// Create job listings
	now := time.Now()

	jobListings := []models.JobListing{
		// Vantir
		{
			Id:   uuid.New(),
			Name: "Forging founders from KTH talent",
			Description: `Do you dream of building a start-up? Vantir Foundry gives you the chance to join a team, build from idea to product to market, and earn equity.

Our team brings experience from Amazon, Avanza, and Netlight, private-equity evaluations of 15+ BSEK, and 500+ MSEK raised in venture capital.

Join our founding team for a live case:
• CTO – responsible for architecture & stack
• 2 × Fullstack Developers – React Native + backend
• Data Scientists – MLOps & AI Focus
• Data Engineers – Focus on Data Warehouse and pipelines
• UX/UI Designer – creates intuitive interfaces
• Growth Hacker – drives TikTok, community & viral growth

Apply here: https://lnkd.in/d2pqU5fv`,
			Salary:      "Equity",
			Location:    "Remote",
			JobType:     "Other",
			CompanyId:   companies[0].Id,
			StartDate:   now,
			EndDate:     now.AddDate(1, 0, 0), // 1 year from now
			AppUrl:      "https://lnkd.in/d2pqU5fv",
			ContactInfo: "https://lnkd.in/d2pqU5fv",
		},
		// Blenda Labs - Role 1
		{
			Id:   uuid.New(),
			Name: "Fullstack TypeScript Developer: AI Movies",
			Description: `Are you passionate about building products at the intersection of technology and creativity? At Blenda Labs, we're reimagining how movies are made, combining cutting-edge AI tools with creative talent to produce high-quality, cost-effective video at scale.

Who: You're a hungry, ambitious Fullstack Developer who thrives in a startup environment

Duration: Start with a 3 month consultant role to see if we're a good fit.

Apply Here: https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0`,
			Salary:      "Competitive compensation",
			Location:    "Stockholm (Hybrid)",
			JobType:     "Full-time",
			CompanyId:   companies[1].Id,
			StartDate:   now,
			EndDate:     time.Date(2026, 1, 1, 23, 59, 59, 0, time.UTC),
			AppUrl:      "https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0",
			ContactInfo: "https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0",
		},
		// Blenda Labs - Role 2
		{
			Id:   uuid.New(),
			Name: "Fullstack Product + Data Lead (Analytics & Dashboard)",
			Description: `Are you passionate about building products at the intersection of technology and creativity? At Blenda Labs, we're reimagining how movies are made, combining cutting-edge AI tools with creative talent to produce high-quality, cost-effective video at scale.

Who: You're a hungry, ambitious Fullstack Developer who thrives in a startup environment

Duration: Start with a 3 month consultant role to see if we're a good fit.

Apply Here: https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0`,
			Salary:      "Competitive compensation",
			Location:    "Stockholm (Hybrid)",
			JobType:     "Full-time",
			CompanyId:   companies[1].Id,
			StartDate:   now,
			EndDate:     time.Date(2026, 1, 1, 23, 59, 59, 0, time.UTC),
			AppUrl:      "https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0",
			ContactInfo: "https://blendalabs.notion.site/Job-Ads-Blenda-Labs-27248bf3a0928045a482e6e9ace4a7d0",
		},
		// Andon Labs
		{
			Id:   uuid.New(),
			Name: "Paid Research Study with Google DeepMind",
			Description: `Andon Labs and Google DeepMind are collaborating on a project exploring how intelligent AI is compared to some of the smartest people around. We're looking for students to represent humanity in this experiment!

Details:
• The study consists of 3 separate tests, each taking about 1 hour to complete
• You can do them remotely, whenever it suits you, within a limited time window
• Compensation: 200 SEK/hour

Requirements:
• Master's or PhD student (any field)
• Basic knowledge of command line usage

Questions? Reach out to hanna@andonlabs.com`,
			Salary:      "200 SEK/hour",
			Location:    "Remote",
			JobType:     "Other",
			CompanyId:   companies[2].Id,
			StartDate:   now,
			EndDate:     time.Date(2026, 1, 1, 23, 59, 59, 0, time.UTC),
			AppUrl:      "https://docs.google.com/forms/d/1moSWzGseg4h6rJUmRSoK1U9b192QNczLUVgbZELHhS4/viewform?edit_requested=true",
			ContactInfo: "hanna@andonlabs.com",
		},
		// Altaal
		{
			Id:   uuid.New(),
			Name: "AI/Data Investment Manager",
			Description: `Do you want to build products at the intersection of AI, data, and real-world decision-making? At Altaal / Nordic Credit Partners (NCP), we're transforming how modern portfolio management works, building an AI-driven infrastructure that elevates analysis, automates workflows, and empowers an investment team with over 40 years of experience.

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
			CompanyId:   companies[3].Id,
			StartDate:   now,
			EndDate:     time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			AppUrl:      "mailto:hr@altaal.com",
			ContactInfo: "hr@altaal.com",
		},
	}

	log.Println("Creating job listings...")
	for _, job := range jobListings {
		if err := db.Create(&job).Error; err != nil {
			log.Printf("Failed to create job listing %s: %v", job.Name, err)
		} else {
			log.Printf("✅ Created job listing: %s at %s", job.Name, getCompanyName(companies, job.CompanyId))
		}
	}

	log.Println("\n🎉 Successfully reset job board with 4 companies and 5 real job listings!")
	log.Printf("   • Vantir AB - 1 position")
	log.Printf("   • Blenda Labs - 2 positions")
	log.Printf("   • Andon Labs - 1 position")
	log.Printf("   • Altaal Advisory - 1 position")
}

func getCompanyName(companies []models.Company, id uuid.UUID) string {
	for _, c := range companies {
		if c.Id == id {
			return c.Name
		}
	}
	return "Unknown"
}
