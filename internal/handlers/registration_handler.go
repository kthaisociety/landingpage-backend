//go:build ignore

package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type RegistrationHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewRegistrationHandler(db *gorm.DB, cfg *config.Config) *RegistrationHandler {
	return &RegistrationHandler{db: db, cfg: cfg}
}

func (h *RegistrationHandler) Register(r *gin.RouterGroup) {
	registrations := r.Group("/registrations")
	{
		// Public endpoints (require auth)
		registrations.Use(middleware.AuthRequiredJWT(h.cfg))
		registrations.Use(middleware.RegisteredUserRequired(h.db))
		registrations.GET("", h.List)
		registrations.POST("", h.Create)
		registrations.GET("/:id", h.Get)
		registrations.GET("/my", h.GetUserRegistrations)
		registrations.GET("/event/:eventId", h.GetEventRegistrations)
		registrations.POST("/register/:eventId", h.RegisterForEvent)
		registrations.PUT("/cancel/:eventId", h.CancelRegistration)

		// Admin-only endpoints
		admin := registrations.Group("/admin")
		admin.Use(middleware.RoleRequired(h.cfg, "admin"))
		admin.PUT("/:userId/:eventId", h.Update)
		admin.DELETE("/:userId/:eventId", h.Delete)
		admin.PUT("/:userId/:eventId/status", h.UpdateStatus)
		admin.PUT("/:userId/:eventId/attended", h.MarkAttendance)
	}
}

func (h *RegistrationHandler) List(c *gin.Context) {
	var registrations []models.Registration
	if err := h.db.Preload("User").Preload("Event").Find(&registrations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, registrations)
}

func (h *RegistrationHandler) Create(c *gin.Context) {
	var registration models.Registration
	if err := c.ShouldBindJSON(&registration); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.db.Create(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, registration)
}

func (h *RegistrationHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var registration models.Registration
	if err := h.db.First(&registration, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Registration not found"})
		return
	}
	c.JSON(http.StatusOK, registration)
}

func (h *RegistrationHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var registration models.Registration
	if err := h.db.First(&registration, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Registration not found"})
		return
	}

	if err := c.ShouldBindJSON(&registration); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.db.Save(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, registration)
}

func (h *RegistrationHandler) Delete(c *gin.Context) {
	userIDParam := c.Param("userId")
	eventIDParam := c.Param("eventId")

	userID, err := uuid.Parse(userIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	eventID, err := uuid.Parse(eventIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}

	if err := h.db.Where("user_id = ? AND event_id = ?", userID, eventID).
		Delete(&models.Registration{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Registration deleted"})
}

// RegisterForEvent allows a user to register for an event
func (h *RegistrationHandler) RegisterForEvent(c *gin.Context) {
	eventID := c.Param("eventId")

	userID, _, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user data"})
		return
	}

	// Check if event exists
	var event models.Event
	eventUUID, err := uuid.Parse(eventID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}
	if err := h.db.First(&event, "id = ?", eventUUID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}

	// Check if registration exists already
	var existingReg models.Registration
	result := h.db.Where("user_id = ? AND event_id = ?", userID, eventUUID).First(&existingReg)
	if result.Error == nil {
		if existingReg.Status == models.RegistrationStatusCancelled {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "You have previously cancelled this registration and cannot re-register",
			})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "You are already registered for this event"})
		return
	}

	// Check if event has reached max capacity
	var count int64
	h.db.Model(&models.Registration{}).Where("event_id = ? AND status NOT IN (?)",
		eventUUID, []models.RegistrationStatus{
			models.RegistrationStatusRejected,
			models.RegistrationStatusCancelled,
		}).Count(&count)

	if event.RegistrationMax > 0 && int(count) >= event.RegistrationMax {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Event has reached maximum capacity"})
		return
	}

	// Create new registration
	registration := models.Registration{
		EventID:  eventUUID,
		UserID:   userID,
		Status:   models.RegistrationStatusPending,
		Attended: false,
	}

	if err := h.db.Create(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, registration)
}

// GetUserRegistrations returns all registrations for the current user
func (h *RegistrationHandler) GetUserRegistrations(c *gin.Context) {
	userID, user, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user data"})
		return
	}

	// Optionally get user profile data
	var profile models.Profile
	h.db.Where("user_id = ?", userID).First(&profile)

	var registrations []models.Registration
	if err := h.db.Where("user_id = ?", userID).
		Preload("Event").Find(&registrations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"registrations": registrations,
		"user": gin.H{
			"id":    userID,
			"email": user.Email,
			"name":  profile.FirstName + " " + profile.LastName,
		},
	})
}

// GetEventRegistrations returns all registrations for a specific event
func (h *RegistrationHandler) GetEventRegistrations(c *gin.Context) {
	eventIDParam := c.Param("eventId")
	eventID, err := uuid.Parse(eventIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}

	var registrations []models.Registration
	if err := h.db.Where("event_id = ?", eventID).
		Preload("User").Find(&registrations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, registrations)
}

// CancelRegistration allows a user to cancel their own registration
func (h *RegistrationHandler) CancelRegistration(c *gin.Context) {
	eventIDParam := c.Param("eventId")

	currentUserID, _, err := h.getUserData(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user data"})
		return
	}

	eventID, err := uuid.Parse(eventIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}

	var registration models.Registration
	if err := h.db.Where("user_id = ? AND event_id = ?", currentUserID, eventID).
		First(&registration).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Registration not found"})
		return
	}

	// Cancel by setting status to cancelled
	now := time.Now()
	registration.Status = models.RegistrationStatusCancelled
	registration.CancelledAt = &now

	if err := h.db.Save(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Registration cancelled successfully"})

}

// UpdateStatus allows admins to update the status of a registration
func (h *RegistrationHandler) UpdateStatus(c *gin.Context) {
	userIDParam := c.Param("userId")
	eventIDParam := c.Param("eventId")

	var input struct {
		Status models.RegistrationStatus `json:"status" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(userIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	eventID, err := uuid.Parse(eventIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}

	var registration models.Registration
	if err := h.db.Where("user_id = ? AND event_id = ?", userID, eventID).
		First(&registration).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Registration not found"})
		return
	}

	registration.Status = input.Status

	if err := h.db.Save(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, registration)
}

// MarkAttendance allows admins to mark attendance for a registration
func (h *RegistrationHandler) MarkAttendance(c *gin.Context) {
	userIDParam := c.Param("userId")
	eventIDParam := c.Param("eventId")

	var input struct {
		Attended bool `json:"attended"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(userIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	eventID, err := uuid.Parse(eventIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}

	var registration models.Registration
	if err := h.db.Where("user_id = ? AND event_id = ?", userID, eventID).
		First(&registration).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Registration not found"})
		return
	}

	registration.Attended = input.Attended

	if err := h.db.Save(&registration).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, registration)
}

// getUserData retrieves user data from the session and database
func (h *RegistrationHandler) getUserData(c *gin.Context) (uuid.UUID, *models.User, error) {
	userIDStr := c.GetString("user_id")

	if userIDStr == "" {
		return uuid.Nil, nil, fmt.Errorf("user_id not found in context")
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("invalíd user_id format: %v", err)
	}

	var user models.User
	if err := h.db.First(&user, "id = ?", userID).Error; err != nil {
		return uuid.Nil, nil, err
	}

	return userID, &user, nil
}
