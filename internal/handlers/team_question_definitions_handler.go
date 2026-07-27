package handlers

import (
	"net/http"
	"strings"

	"backend/internal/models"
	"backend/internal/validation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type teamQuestionsForTeam struct {
	CanEdit   bool                      `json:"can_edit"`
	Questions []validation.TeamQuestion `json:"questions"`
}

// AdminListTeamQuestions returns every team's configured questions, grouped
// by team, with a per-team can_edit flag. Every admin can see all teams' for
// context, but can_edit is only true for teams the requester actually
// belongs to — there is no IT-wide override here, unlike the shared invite
// email template, since each team's questions are genuinely owned by that
// team alone.
func (h *TeamQuestionsHandler) AdminListTeamQuestions(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var rows []models.TeamQuestion
	if err := h.db.Order("team, sort_order, created_at").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team questions"})
		return
	}

	result := make(map[string]*teamQuestionsForTeam, len(allowedApplicationTeams))
	for team := range allowedApplicationTeams {
		canEdit, err := requesterIsOnTeam(h.db, adminID, team)
		if err != nil {
			canEdit = false
		}
		result[team] = &teamQuestionsForTeam{CanEdit: canEdit, Questions: []validation.TeamQuestion{}}
	}
	for _, row := range rows {
		entry, ok := result[row.Team]
		if !ok {
			continue
		}
		entry.Questions = append(entry.Questions, validation.TeamQuestion{
			ID:       row.Id.String(),
			Text:     row.Text,
			Required: row.Required,
		})
	}

	c.JSON(http.StatusOK, gin.H{"teams": result})
}

// AdminCreateTeamQuestion adds a new question to the end of a team's list.
// Only that team's head (or anyone else on the team) may add to it.
func (h *TeamQuestionsHandler) AdminCreateTeamQuestion(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var body struct {
		Team     string `json:"team"`
		Text     string `json:"text"`
		Required bool   `json:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if _, ok := allowedApplicationTeams[body.Team]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team"})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "question text is required"})
		return
	}

	canEdit, err := requesterIsOnTeam(h.db, adminID, body.Team)
	if err != nil || !canEdit {
		c.JSON(http.StatusForbidden, gin.H{"error": "only members of this team can edit its questions"})
		return
	}

	var count int64
	if err := h.db.Model(&models.TeamQuestion{}).Where("team = ?", body.Team).Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load existing questions"})
		return
	}

	question := models.TeamQuestion{
		Id:        uuid.New(),
		Team:      body.Team,
		Text:      strings.TrimSpace(body.Text),
		Required:  body.Required,
		SortOrder: int(count),
	}
	if err := h.db.Create(&question).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create question"})
		return
	}

	c.JSON(http.StatusCreated, validation.TeamQuestion{
		ID:       question.Id.String(),
		Text:     question.Text,
		Required: question.Required,
	})
}

// AdminUpdateTeamQuestion edits a question's text/required flag. Only that
// team's members may edit it.
func (h *TeamQuestionsHandler) AdminUpdateTeamQuestion(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	questionID, err := uuid.Parse(c.Param("questionId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question id"})
		return
	}

	var question models.TeamQuestion
	if err := h.db.First(&question, "id = ?", questionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "question not found"})
		return
	}

	canEdit, err := requesterIsOnTeam(h.db, adminID, question.Team)
	if err != nil || !canEdit {
		c.JSON(http.StatusForbidden, gin.H{"error": "only members of this team can edit its questions"})
		return
	}

	var body struct {
		Text     string `json:"text"`
		Required bool   `json:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "question text is required"})
		return
	}

	question.Text = strings.TrimSpace(body.Text)
	question.Required = body.Required
	if err := h.db.Save(&question).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save question"})
		return
	}

	c.JSON(http.StatusOK, validation.TeamQuestion{
		ID:       question.Id.String(),
		Text:     question.Text,
		Required: question.Required,
	})
}

// AdminDeleteTeamQuestion removes a question. Only that team's members may
// delete it. Existing submitted answers referencing this question's id are
// left as-is — they just won't resolve to question text anymore.
func (h *TeamQuestionsHandler) AdminDeleteTeamQuestion(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	questionID, err := uuid.Parse(c.Param("questionId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question id"})
		return
	}

	var question models.TeamQuestion
	if err := h.db.First(&question, "id = ?", questionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "question not found"})
		return
	}

	canEdit, err := requesterIsOnTeam(h.db, adminID, question.Team)
	if err != nil || !canEdit {
		c.JSON(http.StatusForbidden, gin.H{"error": "only members of this team can edit its questions"})
		return
	}

	if err := h.db.Delete(&question).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete question"})
		return
	}

	c.Status(http.StatusNoContent)
}

// AdminReorderTeamQuestions sets the display order for one team's questions
// in a single call, avoiding partial-update races from doing it one swap at
// a time. Only that team's members may reorder it.
func (h *TeamQuestionsHandler) AdminReorderTeamQuestions(c *gin.Context) {
	adminID, _, ok := getAdminIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not determine admin identity"})
		return
	}

	var body struct {
		Team       string   `json:"team"`
		OrderedIds []string `json:"ordered_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if _, ok := allowedApplicationTeams[body.Team]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team"})
		return
	}

	canEdit, err := requesterIsOnTeam(h.db, adminID, body.Team)
	if err != nil || !canEdit {
		c.JSON(http.StatusForbidden, gin.H{"error": "only members of this team can edit its questions"})
		return
	}

	var existing []models.TeamQuestion
	if err := h.db.Where("team = ?", body.Team).Find(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load questions"})
		return
	}
	existingIDs := make(map[string]struct{}, len(existing))
	for _, q := range existing {
		existingIDs[q.Id.String()] = struct{}{}
	}
	if len(body.OrderedIds) != len(existing) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ordered_ids must include every question for this team, exactly once"})
		return
	}
	seen := make(map[string]struct{}, len(body.OrderedIds))
	for _, id := range body.OrderedIds {
		if _, ok := existingIDs[id]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ordered_ids contains a question not on this team"})
			return
		}
		if _, dup := seen[id]; dup {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ordered_ids contains a duplicate"})
			return
		}
		seen[id] = struct{}{}
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		for index, id := range body.OrderedIds {
			if err := tx.Model(&models.TeamQuestion{}).Where("id = ?", id).Update("sort_order", index).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reorder questions"})
		return
	}

	c.Status(http.StatusNoContent)
}
