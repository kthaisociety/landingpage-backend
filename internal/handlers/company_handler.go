package handlers

import (
	"backend/internal/config"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CompanyHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewCompanyHandler(db *gorm.DB, cfg *config.Config) *CompanyHandler {
	return &CompanyHandler{db: db, cfg: cfg}
}

func (h *CompanyHandler) Register(r *gin.RouterGroup) {
	companies := r.Group("/company")
	admin := companies.Group("/admin")
	{
		// Define company-related routes here
		_ = admin.POST("/addCompany", h.UploadCompany)
		_ = admin.DELETE("/delete", h.DeleteCompany)
		// upload.Use(middleware.RoleRequired(h.cfg, "admin"))
		_ = companies.GET("/getCompany", h.GetCompany)
		_ = companies.GET("/getAllCompanies", h.GetAllCompanies)
		_ = companies.GET("/logo", h.GetLogo)
	}
}

func (h *CompanyHandler) UploadCompany(c *gin.Context) {
	var companyData models.Company
	if err := c.BindJSON(&companyData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Create(&companyData).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.String(http.StatusAccepted, "Company added successfully")
}

func (h *CompanyHandler) GetCompany(c *gin.Context) {
	// Implementation for getting a single company
	id := c.Query("id")
	var company models.Company
	if id != "" {
		if err := h.db.First(&company, "id = ?", id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Company not found"})
			return
		}
		c.JSON(http.StatusOK, company)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Company ID is required"})
	}
}

// finish later if needed. For not just expose a way to get the blob
// func (h *CompanyHandler) GetCompanyWithLogo(c *gin.Context) {
// 	// Implementation for getting a single company
// 	id := c.Query("id")
// 	var company models.Company
// 	if id != "" {
// 		if err := h.db.First(&company, "id = ?", id).Error; err != nil {
// 			c.JSON(http.StatusNotFound, gin.H{"error": "Company not found"})
// 			return
// 		}
// 		// get logo blob
// 		var logoBlob models.BlobData
// 		if err := h.db.First(&logoBlob, "id = ?", company.Logo).Error; err != nil {
// 			log.Printf("Failed to Fetch Blob Data: %s\n", err)
// 			c.JSON(http.StatusOK, company)
// 			return
// 		}
// 		r2, err := utils.InitS3SDK(h.cfg)
// 		logo := logoBlob.GetData(r2)
// 		c.JSON(http.StatusOK, company)
// 	} else {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": "Company ID is required"})
// 	}
// }

func (h *CompanyHandler) GetLogo(c *gin.Context) {
	logo_id := c.Query("id")
	luuid, err := uuid.Parse(logo_id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err, "note": "uuid not valid"})
		return
	}
	// get logo blob
	var logoBlob models.BlobData
	if err := h.db.First(&logoBlob, "blob_id = ?", luuid).Error; err != nil {
		log.Printf("Failed to Fetch Blob Data: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err})
		return
	}
	r2, err := utils.InitS3SDK(h.cfg)
	if err != nil {
		log.Printf("Failed to Init Blob Store: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err})
		return
	}
	logo, err := logoBlob.GetData(r2)
	if err != nil {
		log.Printf("Failed to Fetch Blob: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err})
		return
	}
	c.Data(http.StatusOK, "image/png", logo)
}

func (h *CompanyHandler) GetAllCompanies(c *gin.Context) {
	// Implementation for getting all companies
	var companies []models.Company
	h.db.Table("companies").Select(
		"companies.id",
		"companies.name").Scan(&companies)
	c.JSON(http.StatusOK, companies)
}

func (h *CompanyHandler) DeleteCompany(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No id provided"})
		return
	}
	result := h.db.Unscoped().Where("id = ?", id).Delete(&models.Company{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, "ok")
}
