package handlers

// Visual Calendar Command Center — read API (Cockpit WO2 Part 2). Owner/admin
// only (staff/players barred at the route + re-asserted here); owner scoping is
// enforced in the repository SQL. Returns one day's resource-timeline payload:
// pitches as rows, each with resolved operating windows + occupancy events.

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type CalendarHandler struct {
	repo repository.CalendarRepository
}

func NewCalendarHandler(repo repository.CalendarRepository) *CalendarHandler {
	return &CalendarHandler{repo: repo}
}

// GetDayCalendar — GET /owner/calendar?date=YYYY-MM-DD (default: today in Amman).
func (h *CalendarHandler) GetDayCalendar(c *gin.Context) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "the calendar is restricted to pitch owners",
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

	cal, err := h.repo.OwnerDayCalendar(c.Request.Context(), actor, day)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load calendar"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cal})
}
