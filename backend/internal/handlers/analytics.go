package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
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

	// The repo scopes via actor.OwnerScopeFilter (admin unscoped; owner → own
	// pitches), so an owner can never read another owner's revenue even by passing
	// an arbitrary pitch_id.
	summary, err := h.repo.OwnerRevenueSummary(c.Request.Context(), actor, pitchID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load analytics",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": summary})
}

// requireOwnerOrAdmin re-asserts the finance boundary inside the handler (defence
// in depth — the route's RequireRole is the primary gate). Returns false and
// writes 403 for any non-owner/admin principal.
func requireOwnerOrAdmin(c *gin.Context, actor auth.Actor) bool {
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "financial data is restricted to pitch owners",
		})
		return false
	}
	return true
}

// GetKPIs returns the owner dashboard headline tiles (WO2 Part 3).
// GET /api/v1/owner/analytics/kpis — owner-scoped, Amman-anchored.
func (h *AnalyticsHandler) GetKPIs(c *gin.Context) {
	actor := middleware.GetActor(c)
	if !requireOwnerOrAdmin(c, actor) {
		return
	}

	kpis, err := h.repo.OwnerKPIs(c.Request.Context(), actor)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load analytics",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": kpis})
}

// GetTimeSeries returns confirmed revenue + non-block booking volume bucketed by
// the requested Amman calendar granularity (WO2 Part 3, charts).
// GET /api/v1/owner/analytics/timeseries?granularity=day|week|month&from=&to=&pitch_id=
//
// Defaults: granularity=day; when from/to are omitted, the trailing 30 Amman days
// up to and including today. Owner-scoped in SQL regardless of params.
func (h *AnalyticsHandler) GetTimeSeries(c *gin.Context) {
	actor := middleware.GetActor(c)
	if !requireOwnerOrAdmin(c, actor) {
		return
	}

	granularity := strings.TrimSpace(c.DefaultQuery("granularity", "day"))
	switch granularity {
	case "day", "week", "month":
		// ok — validated allow-list; safe to interpolate into date_trunc.
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_granularity", "message": "granularity must be one of: day, week, month",
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

	// Date range: Amman calendar days → half-open UTC [fromStart, toEnd).
	// Default window: trailing 30 days ending today (Amman).
	now := time.Now().UTC()
	_, toEnd := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now))
	fromStart, _ := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, -29))

	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		d, err := time.Parse("2006-01-02", raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "from must be YYYY-MM-DD"})
			return
		}
		fromStart, _ = timeutil.AmmanDayBoundsUTC(d)
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		d, err := time.Parse("2006-01-02", raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "to must be YYYY-MM-DD"})
			return
		}
		_, toEnd = timeutil.AmmanDayBoundsUTC(d)
	}

	series, err := h.repo.OwnerTimeSeries(c.Request.Context(), actor, repository.TimeSeriesParams{
		Granularity: granularity,
		From:        fromStart,
		To:          toEnd,
		PitchID:     pitchID,
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load analytics",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": series})
}
