package handlers

import (
	"backend/internal/config"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/utils"
	"bytes"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	maxSponsorLogoBytes = 2 << 20 // 2 MiB
	// maxSponsorUploadBytes caps the whole Create request: the logo plus
	// headroom for the text fields and multipart framing.
	maxSponsorUploadBytes = maxSponsorLogoBytes + 1<<20
	maxSponsorNameLength  = 100
)

var errSponsorLogoTooLarge = errors.New("logo exceeds the size limit")

// sponsorLogoTypes maps the extension stored in BlobData.FType to the
// Content-Type the logo is served with.
var sponsorLogoTypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"webp": "image/webp",
	"svg":  "image/svg+xml",
}

type SponsorHandler struct {
	db  *gorm.DB
	cfg *config.Config
	// openBlobStore is swapped for an in-memory store in tests.
	openBlobStore func() (utils.BlobStore, error)
}

func NewSponsorHandler(db *gorm.DB, cfg *config.Config) *SponsorHandler {
	return &SponsorHandler{
		db:  db,
		cfg: cfg,
		openBlobStore: func() (utils.BlobStore, error) {
			return utils.InitS3SDK(cfg)
		},
	}
}

func (h *SponsorHandler) Register(r *gin.RouterGroup) {
	sponsors := r.Group("/sponsors")
	sponsors.GET("", h.List)
	sponsors.GET("/:id/logo", h.GetLogo)

	admin := r.Group("/admin/sponsors")
	admin.Use(middleware.AuthRequiredJWT(h.cfg))
	admin.Use(middleware.RoleRequired(h.cfg, "admin"))
	admin.POST("", h.Create)
	admin.PUT("/order", h.Reorder)
	admin.DELETE("/:id", h.Delete)
}

// List returns every sponsor in display order. It always returns a JSON
// array, never null, so the frontend can map over it directly.
func (h *SponsorHandler) List(c *gin.Context) {
	sponsors := []models.Sponsor{}
	if err := h.db.Order("sort_order ASC, created_at ASC").Find(&sponsors).Error; err != nil {
		log.Printf("Failed to list sponsors: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list sponsors"})
		return
	}
	c.JSON(http.StatusOK, sponsors)
}

// Create adds a sponsor from a multipart form: name (required), websiteUrl
// (optional, http/https), sortOrder (optional integer, defaults to the end of
// the list) and logo (required PNG, JPEG, WebP or SVG file, at most
// maxSponsorLogoBytes).
func (h *SponsorHandler) Create(c *gin.Context) {
	// Refuse oversized uploads while they're still arriving, instead of
	// letting the form parser buffer them to disk first. Parsing here, before
	// any PostForm call, turns an oversized body into a clear 413 rather than
	// a misleading "name is required".
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSponsorUploadBytes)
	if err := c.Request.ParseMultipartForm(maxSponsorUploadBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Logo must be at most 2 MB"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request must be a multipart form"})
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sponsor name is required"})
		return
	}
	if utf8.RuneCountInString(name) > maxSponsorNameLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sponsor name must be at most 100 characters"})
		return
	}

	websiteUrl := strings.TrimSpace(c.PostForm("websiteUrl"))
	if websiteUrl != "" && !isHTTPURL(websiteUrl) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Website URL must start with http:// or https://"})
		return
	}

	var requestedOrder *int
	if raw := strings.TrimSpace(c.PostForm("sortOrder")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Sort order must be a whole number"})
			return
		}
		requestedOrder = &n
	}

	file, err := c.FormFile("logo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Logo file is required"})
		return
	}
	data, err := readSponsorLogo(file)
	if errors.Is(err, errSponsorLogoTooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Logo must be at most 2 MB"})
		return
	}
	if err != nil {
		log.Printf("Failed to read sponsor logo: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Could not read logo file"})
		return
	}
	ext, ok := detectSponsorLogoType(data)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Logo must be a PNG, JPEG, WebP or SVG image"})
		return
	}

	// Without an explicit order, a new sponsor goes after everyone else.
	var sortOrder int
	if requestedOrder != nil {
		sortOrder = *requestedOrder
	} else if err := h.db.Model(&models.Sponsor{}).
		Select("COALESCE(MAX(sort_order) + 1, 0)").Scan(&sortOrder).Error; err != nil {
		log.Printf("Failed to find next sponsor position: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save sponsor"})
		return
	}

	store, err := h.openBlobStore()
	if err != nil {
		log.Printf("Failed to init blob store: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store logo"})
		return
	}

	sponsorID := uuid.New()
	logoBlob, err := models.NewBlobData("sponsor-logo", ext, sponsorID, data, h.db, store)
	if err != nil {
		log.Printf("Failed to store sponsor logo: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store logo"})
		return
	}

	sponsor := models.Sponsor{
		Id:         sponsorID,
		Name:       name,
		WebsiteUrl: websiteUrl,
		Logo:       logoBlob.BlobId,
		SortOrder:  sortOrder,
	}
	if err := h.db.Create(&sponsor).Error; err != nil {
		log.Printf("Failed to create sponsor: %v", err)
		// Don't leave an orphaned logo behind when the sponsor itself failed.
		if err := logoBlob.DeleteData(&logoBlob.BlobId, h.db, store); err != nil {
			log.Printf("Failed to clean up logo for unsaved sponsor: %v", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save sponsor"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Sponsor added", "sponsor": sponsor})
}

type reorderSponsorsRequest struct {
	IDs []uuid.UUID `json:"ids"`
}

var errSponsorListChanged = errors.New("sponsor list changed")

// Reorder sets every sponsor's position from a JSON body {"ids": [...]}
// listing all sponsors in their new order. The list must name each current
// sponsor exactly once, so an admin working from a stale list (someone else
// added or removed a sponsor meanwhile) gets a conflict instead of silently
// reshuffling a list they haven't seen.
func (h *SponsorHandler) Reorder(c *gin.Context) {
	var req reorderSponsorsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Body must be {\"ids\": [sponsor IDs in order]}"})
		return
	}
	seen := make(map[uuid.UUID]bool, len(req.IDs))
	for _, id := range req.IDs {
		if seen[id] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Each sponsor may appear only once"})
			return
		}
		seen[id] = true
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		var current []uuid.UUID
		if err := tx.Model(&models.Sponsor{}).Pluck("id", &current).Error; err != nil {
			return err
		}
		if len(current) != len(req.IDs) {
			return errSponsorListChanged
		}
		for _, id := range current {
			if !seen[id] {
				return errSponsorListChanged
			}
		}
		for position, id := range req.IDs {
			if err := tx.Model(&models.Sponsor{}).Where("id = ?", id).
				Update("sort_order", position).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errSponsorListChanged) {
		c.JSON(http.StatusConflict, gin.H{"error": "The sponsor list has changed since you loaded it. Reload and try again."})
		return
	}
	if err != nil {
		log.Printf("Failed to reorder sponsors: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save sponsor order"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Sponsor order saved"})
}

// Delete removes a sponsor and then, best effort, its logo. The sponsor row
// goes first so the page stops showing it even if storage cleanup fails.
func (h *SponsorHandler) Delete(c *gin.Context) {
	sponsor, ok := h.findSponsor(c)
	if !ok {
		return
	}

	if err := h.db.Unscoped().Where("id = ?", sponsor.Id).Delete(&models.Sponsor{}).Error; err != nil {
		log.Printf("Failed to delete sponsor %s: %v", sponsor.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove sponsor"})
		return
	}

	if sponsor.Logo != uuid.Nil {
		h.deleteLogo(sponsor.Logo)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Sponsor removed"})
}

// GetLogo serves a sponsor's logo with the Content-Type it was uploaded as.
func (h *SponsorHandler) GetLogo(c *gin.Context) {
	sponsor, ok := h.findSponsor(c)
	if !ok {
		return
	}
	if sponsor.Logo == uuid.Nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sponsor has no logo"})
		return
	}

	var logoBlob models.BlobData
	if err := h.db.Where("blob_id = ?", sponsor.Logo).First(&logoBlob).Error; err != nil {
		log.Printf("Failed to find logo blob for sponsor %s: %v", sponsor.Id, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Logo not found"})
		return
	}
	store, err := h.openBlobStore()
	if err != nil {
		log.Printf("Failed to init blob store: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load logo"})
		return
	}
	data, err := logoBlob.GetData(store)
	if err != nil {
		log.Printf("Failed to fetch logo for sponsor %s: %v", sponsor.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load logo"})
		return
	}

	contentType, known := sponsorLogoTypes[logoBlob.FType]
	if !known {
		contentType = "application/octet-stream"
	}
	// An SVG opened directly (not through <img>) runs as a document on this
	// origin; the sandbox CSP keeps any script inside it from executing.
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	// Safe because a sponsor's logo never changes. If replacing a logo is ever
	// added, put something that changes with it (e.g. the blob ID) in the URL,
	// or browsers may show the old logo for up to an hour.
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, contentType, data)
}

// findSponsor loads the sponsor named by the :id path parameter, writing the
// error response itself and returning ok=false when it can't.
func (h *SponsorHandler) findSponsor(c *gin.Context) (models.Sponsor, bool) {
	var sponsor models.Sponsor
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sponsor ID"})
		return sponsor, false
	}
	if err := h.db.Where("id = ?", id).First(&sponsor).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Sponsor not found"})
		} else {
			log.Printf("Failed to load sponsor %s: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load sponsor"})
		}
		return sponsor, false
	}
	return sponsor, true
}

func (h *SponsorHandler) deleteLogo(blobID uuid.UUID) {
	var logoBlob models.BlobData
	if err := h.db.Where("blob_id = ?", blobID).First(&logoBlob).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("Sponsor logo %s already gone", blobID)
		} else {
			log.Printf("Failed to look up sponsor logo %s, left in storage: %v", blobID, err)
		}
		return
	}
	store, err := h.openBlobStore()
	if err != nil {
		log.Printf("Failed to init blob store, logo %s left in storage: %v", blobID, err)
		return
	}
	if err := logoBlob.DeleteData(&blobID, h.db, store); err != nil {
		log.Printf("Failed to delete sponsor logo %s: %v", blobID, err)
	}
}

// readSponsorLogo reads the whole upload, refusing anything over
// maxSponsorLogoBytes even if the client under-reported its size.
func readSponsorLogo(file *multipart.FileHeader) ([]byte, error) {
	if file.Size > maxSponsorLogoBytes {
		return nil, errSponsorLogoTooLarge
	}
	f, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxSponsorLogoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSponsorLogoBytes {
		return nil, errSponsorLogoTooLarge
	}
	return data, nil
}

// detectSponsorLogoType identifies the logo from its bytes rather than its
// filename, returning the extension to store it under.
func detectSponsorLogoType(data []byte) (string, bool) {
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/webp":
		return "webp", true
	}
	// Go's sniffer has no SVG signature and reports it as XML or plain text.
	// Anything it calls HTML is refused even if it contains an <svg> tag.
	isXMLOrText := strings.HasPrefix(sniffed, "text/xml") || strings.HasPrefix(sniffed, "text/plain")
	if isXMLOrText && looksLikeSVG(data) {
		return "svg", true
	}
	return "", false
}

func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
