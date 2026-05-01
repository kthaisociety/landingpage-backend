package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"log"
	"net/http"
	"strings"

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
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	{
		// Define company-related routes here
		_ = admin.POST("/addCompany", h.UploadCompany)
		_ = admin.DELETE("/delete", h.DeleteCompany)
		_ = admin.PUT("/update", h.UpdateCompany)
		_ = companies.GET("/getCompany", h.GetCompany)
		_ = companies.GET("/getAllCompanies", h.GetAllCompanies)
		_ = companies.GET("/logo", h.GetLogo)
	}
}

//	func (h *CompanyHandler) UploadCompany(c *gin.Context) {
//		var companyData models.Company
//		if err := c.BindJSON(&companyData); err != nil {
//			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
//			return
//		}
//		if err := h.db.Create(&companyData).Error; err != nil {
//			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
//			return
//		}
//		c.String(http.StatusAccepted, "Company added successfully")
//	}

func (h *CompanyHandler) UploadCompany(c *gin.Context) {
	name := c.PostForm("name")
	description := c.PostForm("description")
	websiteUrl := c.PostForm("websiteUrl")

	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Company name is required"})
		return
	}

	file, err := c.FormFile("logo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Logo file is required or invalid"})
		return
	}

	company, err := models.NewCompany(name, description, websiteUrl, file, h.db, h.cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Company added successfully",
		"company": company,
	})
}

func (h *CompanyHandler) GetCompany(c *gin.Context) {
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

//	func (h *CompanyHandler) GetAllCompanies(c *gin.Context) {
//		// Implementation for getting all companies
//		var companies []models.Company
//		h.db.Table("companies").Select(
//			"companies.id",
//			"companies.name").Scan(&companies)
//		c.JSON(http.StatusOK, companies)
//	}

func (h *CompanyHandler) GetAllCompanies(c *gin.Context) {
	var companies []models.Company

	if err := h.db.Find(&companies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, companies)
}

// func (h *CompanyHandler) DeleteCompany(c *gin.Context) {
// 	id := c.Query("id")
// 	if id == "" {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": "No id provided"})
// 		return
// 	}
// 	result := h.db.Unscoped().Where("id = ?", id).Delete(&models.Company{})
// 	if result.Error != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
// 		return
// 	}
// 	c.JSON(http.StatusOK, "ok")
// }

func (h *CompanyHandler) UpdateCompany(c *gin.Context) {
	id := c.PostForm("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Company ID is required"})
		return
	}

	var company models.Company
	if err := h.db.First(&company, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Company not found"})
		return
	}

	if name := c.PostForm("name"); name != "" {
		company.Name = name
	}
	if description := c.PostForm("description"); description != "" {
		company.Description = description
	}
	if websiteUrl := c.PostForm("websiteUrl"); websiteUrl != "" {
		company.WebsiteUrl = websiteUrl
	}

	r2, r2Err := utils.InitS3SDK(h.cfg)

	if c.PostForm("removeLogo") == "true" && company.Logo != uuid.Nil {
		var oldBlob models.BlobData
		if err := h.db.First(&oldBlob, "blob_id = ?", company.Logo).Error; err == nil && r2Err == nil {
			oldBlob.DeleteData(&company.Logo, h.db, r2) // <-- Cleanly calls your new model method!
		}
		company.Logo = uuid.Nil
	}

	file, err := c.FormFile("logo")
	if err == nil && file != nil {
		fdata := make([]byte, file.Size)
		f_reader, _ := file.Open()
		nread, readErr := f_reader.Read(fdata)

		if readErr == nil && nread == int(file.Size) {
			importParts := strings.Split(file.Filename, ".")
			extPart := ""
			namePart := file.Filename
			if len(importParts) > 1 {
				extPart = importParts[len(importParts)-1]
				namePart = strings.Join(importParts[:len(importParts)-1], ".")
			}

			if r2Err == nil {
				oldLogoID := company.Logo

				logoBlob, blobErr := models.NewBlobData(namePart, extPart, company.Id, fdata, h.db, r2)
				if blobErr == nil {
					company.Logo = logoBlob.BlobId

					// Delete old logo using the new method
					if oldLogoID != uuid.Nil {
						var oldBlob models.BlobData
						if err := h.db.First(&oldBlob, "blob_id = ?", oldLogoID).Error; err == nil {
							oldBlob.DeleteData(&oldLogoID, h.db, r2) // <-- Cleanly calls your new model method!
						}
					}
				}
			}
		}
	}

	if err := h.db.Save(&company).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save company updates"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Company updated successfully", "company": company})
}

func (h *CompanyHandler) DeleteCompany(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No id provided"})
		return
	}

	var company models.Company
	if err := h.db.First(&company, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Company not found"})
		return
	}

	// Delete the physical logo if the company has one
	if company.Logo != uuid.Nil {
		if r2, err := utils.InitS3SDK(h.cfg); err == nil {
			var logoBlob models.BlobData
			if err := h.db.First(&logoBlob, "blob_id = ?", company.Logo).Error; err == nil {
				logoBlob.DeleteData(&company.Logo, h.db, r2) // <-- Cleanly calls your new model method!
			}
		}
	}

	if result := h.db.Unscoped().Delete(&company); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, "ok")
}
