package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// AnalyticsHandler serves the finance/analytics surface. These endpoints are the
// canonical "owner/admin only" boundary: staff are barred at the route
// (RequireRole) AND re-asserted here (defence in depth).
type AnalyticsHandler struct {
	repo repository.AnalyticsRepository
}

// NewAnalyticsHandler constructs an AnalyticsHandler.
func NewAnalyticsHandler(repo repository.AnalyticsRepository) *AnalyticsHandler {
	return &AnalyticsHandler{repo: repo}
}

// GetRevenueSummary returns confirmed-booking revenue for the caller's scope.
// GET /owner/analytics?pitch_id=<optional>
//
// Owner → their own pitches (OwnerScope = their user id). Admin → all pitches
// (OwnerScope = 0). Staff are hard-rejected: the route uses RequireRole(
// "owner","admin"), and this guard repeats the check so the financial query can
// never run for a staff principal even if the route were ever misconfigured.
func (h *AnalyticsHandler) GetRevenueSummary(c *gin.Context) {
	actor := middleware.GetActor(c)

	// Defence in depth — finance is categorically off-limits to staff (and players).
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "financial data is restricted to pitch owners",
		})
		return
	}

	pitchID := 0
	if raw := c.Query("pitch_id"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid_pitch_id", "message": "pitch_id must be a positive integer",
			})
			return
		}
		pitchID = v
	}

	// OwnerScope() returns 0 for admin (unscoped) or the owner's id (scoped) — the
	// SQL filters to that owner's pitches, so an owner can never read another
	// owner's revenue even by passing an arbitrary pitch_id.
	summary, err := h.repo.OwnerRevenueSummary(c.Request.Context(), actor.OwnerScope(), pitchID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load analytics",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": summary})
}
