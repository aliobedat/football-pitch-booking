package handlers

// Day View timeline — read API (PR-1). Owner/admin only (staff excluded from
// PR-1; RequireRole bars them at the route and this handler re-asserts). Returns
// ONE pitch's 30-minute occupancy grid + summary for one Amman calendar day. Owner
// scoping is enforced in the repository SQL; a cross-owner / unknown / soft-deleted
// pitch returns 404 (the project's "not found OR not owned" convention).

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type DayViewHandler struct {
	repo repository.DayViewRepository
}

func NewDayViewHandler(repo repository.DayViewRepository) *DayViewHandler {
	return &DayViewHandler{repo: repo}
}

// GetDayView — GET /owner/day-view?pitch_id={id}&date=YYYY-MM-DD
// (date optional; defaults to today in Amman).
func (h *DayViewHandler) GetDayView(c *gin.Context) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "the day view is restricted to pitch owners",
		})
		return
	}

	rawPitch := c.Query("pitch_id")
	if rawPitch == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "missing_param", "message": "query parameter 'pitch_id' is required",
		})
		return
	}
	pitchID, err := strconv.ParseInt(rawPitch, 10, 64)
	if err != nil || pitchID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_pitch_id", "message": "pitch_id must be a positive integer",
		})
		return
	}

	day := time.Now()
	if raw := c.Query("date"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, timeutil.Amman())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "date must be YYYY-MM-DD"})
			return
		}
		day = d
	}

	dv, err := h.repo.OwnerDayView(c.Request.Context(), actor, pitchID, day)
	if err != nil {
		if errors.Is(err, repository.ErrPitchNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية عرضه",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load day view"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": dv})
}
