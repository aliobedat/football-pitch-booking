package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// CRMHandler serves the owner CRM (players who booked at the owner's pitches,
// with derived visit/no-show counts). Owner/admin only; scoped to owned pitches.
type CRMHandler struct {
	repo repository.CRMRepository
}

// NewCRMHandler constructs a CRMHandler.
func NewCRMHandler(repo repository.CRMRepository) *CRMHandler {
	return &CRMHandler{repo: repo}
}

// GetCRM returns the scoped player CRM. GET /owner/crm.
func (h *CRMHandler) GetCRM(c *gin.Context) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "restricted to pitch owners"})
		return
	}
	rows, err := h.repo.OwnerCRM(c.Request.Context(), actor)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load CRM"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}
