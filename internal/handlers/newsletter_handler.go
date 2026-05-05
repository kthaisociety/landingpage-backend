package handlers

import (
	"backend/internal/mailchimp"
	"backend/internal/middleware"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type NewsletterHandler struct {
	mailchimp *mailchimp.MailchimpAPI
}

func NewNewsletterHandler(mc *mailchimp.MailchimpAPI) *NewsletterHandler {
	return &NewsletterHandler{mailchimp: mc}
}

func (h *NewsletterHandler) Register(r *gin.RouterGroup) {
	nl := r.Group("/newsletter")
	nl.POST("/subscribe", middleware.RateLimit(), h.Subscribe)
}

type newsletterSubscribeBody struct {
	Email string `json:"email" binding:"required,email"`
}

func (h *NewsletterHandler) Subscribe(c *gin.Context) {
	var body newsletterSubscribeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.mailchimp.SubscribeNewsletter(body.Email); err != nil {
		log.Printf("newsletter subscribe: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not subscribe"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
