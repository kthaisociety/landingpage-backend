package handlers

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "backend/internal/config"
    "backend/internal/models"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

// setupTestDB connects to your Docker database
// Returns nil, nil if database is not available (CI without DB)
func setupTestDB(t *testing.T) *gorm.DB {
    // Connection string matching your docker-compose setup
    dsn := "host=localhost user=postgres password=password dbname=kthais port=5432 sslmode=disable"
    
    db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
    if err != nil {
        t.Skip("Skipping test: database not available (run 'docker compose up -d' for local testing)")
        return nil
    }

    return db
}

// testUserID is the user ID used for authenticated test requests
var testUserID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// mockAuthMiddleware simulates JWT authentication by setting user_id in context
func mockAuthMiddleware(userID uuid.UUID) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Set("user_id", userID.String())
        c.Next()
    }
}

// seedTestUser ensures the test user exists within our transaction
func seedTestUser(tx *gorm.DB) error {
    user := models.User{
        ID:        testUserID,
        Email:     "test@example.com",
        Provider:  "local",
        Roles:     []string{"user"},
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
    }

    // Use FirstOrCreate so we don't fail if the user already exists in the DB
    if err := tx.FirstOrCreate(&user).Error; err != nil {
        return err
    }

    profile := models.Profile{
        ID:        uuid.New(),
        UserID:    testUserID,
        FirstName: "Test",
        LastName:  "User",
        Email:     "test@example.com",
    }

    // We only create the profile if it doesn't exist to avoid unique constraint errors
    var count int64
    tx.Model(&models.Profile{}).Where("user_id = ?", testUserID).Count(&count)
    if count == 0 {
        return tx.Create(&profile).Error
    }
    return nil
}

func TestCreateProject(t *testing.T) {
    // 1. Connect to real DB
    db := setupTestDB(t)
    if db == nil {
        return
    }

    // 2. Start a Transaction
    // This ensures all data created during this test is removed at the end
    tx := db.Begin()
    defer tx.Rollback()

    // 3. Setup
    gin.SetMode(gin.TestMode)
    if err := seedTestUser(tx); err != nil {
        t.Fatalf("Failed to seed user: %v", err)
    }

    cfg := &config.Config{}
    // Pass the TRANSACTION (tx), not the raw db connection
    handler := NewProjectHandler(tx, cfg)
    
    r := gin.Default()
    // Add mock auth middleware to simulate authenticated user
    r.Use(mockAuthMiddleware(testUserID))
    r.POST("/projects", handler.Create)

    // 4. Execute
    input := map[string]interface{}{
        "name":        "Integration Test Project",
        "description": "Testing with real Postgres",
        "skills":      []string{"Go", "Docker"},
        "status":      "planning",
    }
    jsonValue, _ := json.Marshal(input)
    req, _ := http.NewRequest("POST", "/projects", bytes.NewBuffer(jsonValue))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    t.Logf("Create Project Response: %s", w.Body.String())

    // 5. Assert
    if w.Code != http.StatusCreated {
        t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
    }


    var response ProjectResponse
    if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
        t.Fatalf("Failed to parse response: %v", err)
    }

    if response.Name != "Integration Test Project" {
        t.Errorf("Expected project name 'Integration Test Project', got '%s'", response.Name)
    }

    // Verify side effects inside the transaction
    var teamCount int64
    tx.Model(&models.Team{}).Where("team_name = ?", "Integration Test Project Team").Count(&teamCount)
    if teamCount != 1 {
        t.Errorf("Expected 1 team created, got %d", teamCount)
    }

}

func TestGetProject(t *testing.T) {
    db := setupTestDB(t)
    if db == nil {
        return
    }

    tx := db.Begin()
    defer tx.Rollback()

    gin.SetMode(gin.TestMode)
    
   // 1. Seed a project
    targetID := uuid.New()
    project := models.Project{
        ProjectID:   targetID,
        ProjectName: "Target Project",
        Status:      models.ProjectStatusPlanning,
    }
    tx.Create(&project)

    // 2. Seed a team (Required to avoid "record not found" in buildProjectResponse)
    teamID := uuid.New()
    team := models.Team{
        TeamID:   teamID,
        TeamName: "Target Project Team",
    }
    tx.Create(&team)

    // 3. Link them
    tx.Create(&models.TeamProjectPair{
        TeamID:    teamID,
        ProjectID: targetID,
    })

    handler := NewProjectHandler(tx, &config.Config{})
    r := gin.Default()
    r.GET("/projects/:id", handler.Get)

    req, _ := http.NewRequest("GET", "/projects/"+targetID.String(), nil)
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    t.Logf("Get Project Response: %s", w.Body.String())


    if w.Code != http.StatusOK {
        t.Errorf("Expected status 200, got %d", w.Code)
    }
}

func TestAddMember(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}

	tx := db.Begin()
	defer tx.Rollback()

	gin.SetMode(gin.TestMode)

	// 1. Seed a project
	projectID := uuid.New()
	project := models.Project{
		ProjectID:   projectID,
		ProjectName: "Member Test Project",
		Status:      models.ProjectStatusPlanning,
	}
	tx.Create(&project)

	// 2. Seed a team
	teamID := uuid.New()
	team := models.Team{
		TeamID:   teamID,
		TeamName: "Member Test Team",
	}
	tx.Create(&team)

	// 3. Link them
	tx.Create(&models.TeamProjectPair{
		TeamID:    teamID,
		ProjectID: projectID,
	})

	// 4. Create a new user to add
	newUserID := uuid.New()
	newUser := models.User{
		ID:        newUserID,
		Email:     "newmember@example.com",
		Provider:  "local",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	tx.Create(&newUser)

	handler := NewProjectHandler(tx, &config.Config{})
	r := gin.Default()
	r.POST("/projects/:id/members", handler.AddMember)

	// 5. Execute AddMember
	input := map[string]interface{}{
		"user_id": newUserID.String(),
	}
	jsonValue, _ := json.Marshal(input)
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/members", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	t.Logf("Add Member Response: %s", w.Body.String())

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// 6. Verify member added
	var count int64
	tx.Model(&models.TeamUserPair{}).Where("team_id = ? AND user_id = ?", teamID, newUserID).Count(&count)
	if count != 1 {
		t.Errorf("Expected user to be added to team, found %d records", count)
	}
}

func TestRemoveMember(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}

	tx := db.Begin()
	defer tx.Rollback()

	gin.SetMode(gin.TestMode)

	// 1. Seed a project
	projectID := uuid.New()
	project := models.Project{
		ProjectID:   projectID,
		ProjectName: "Remove Member Project",
		Status:      models.ProjectStatusPlanning,
	}
	tx.Create(&project)

	// 2. Seed a team
	teamID := uuid.New()
	team := models.Team{
		TeamID:   teamID,
		TeamName: "Remove Member Team",
	}
	tx.Create(&team)

	// 3. Link them
	tx.Create(&models.TeamProjectPair{
		TeamID:    teamID,
		ProjectID: projectID,
	})

	// 4. Create a user and add to team
	memberID := uuid.New()
	member := models.User{
		ID:        memberID,
		Email:     "toremove@example.com",
		Provider:  "local",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	tx.Create(&member)

	tx.Create(&models.TeamUserPair{
		TeamID: teamID,
		UserID: memberID,
	})

	handler := NewProjectHandler(tx, &config.Config{})
	r := gin.Default()
	r.DELETE("/projects/:id/members/:userId", handler.RemoveMember)

	// 5. Execute RemoveMember
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/members/"+memberID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	t.Logf("Remove Member Response: %s", w.Body.String())

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// 6. Verify member removed
	var count int64
	tx.Model(&models.TeamUserPair{}).Where("team_id = ? AND user_id = ?", teamID, memberID).Count(&count)
	if count != 0 {
		t.Errorf("Expected user to be removed from team, found %d records", count)
	}
}
