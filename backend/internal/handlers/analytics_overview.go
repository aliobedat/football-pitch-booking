package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// GetAnalyticsOverview returns the scoped analytics bundle for [from,to] (default
// last 30 Amman days): realized revenue by day & month, hour×weekday heatmap, and
// current-vs-previous totals incl. no-show rate. GET /owner/analytics/overview.
// Owner/admin only (RequireRole at route + this guard).
func (h *AnalyticsHandler) GetAnalyticsOverview(c *gin.Context) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "financial data is restricted to pitch owners"})
		return
	}

	loc := timeutil.Amman()
	toDate := time.Now().In(loc)
	fromDate := toDate.AddDate(0, 0, -29)
	if raw := c.Query("from"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_from", "message": "from must be YYYY-MM-DD"})
			return
		}
		fromDate = d
	}
	if raw := c.Query("to"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_to", "message": "to must be YYYY-MM-DD"})
			return
		}
		toDate = d
	}
	if toDate.Before(fromDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_range", "message": "to must be on/after from"})
		return
	}

	curFrom, _ := timeutil.AmmanDayBoundsUTC(fromDate)
	_, curTo := timeutil.AmmanDayBoundsUTC(toDate)
	spanDays := int(toDate.Truncate(24*time.Hour).Sub(fromDate.Truncate(24*time.Hour)).Hours()/24) + 1
	prevToDate := fromDate.AddDate(0, 0, -1)
	prevFromDate := prevToDate.AddDate(0, 0, -(spanDays - 1))
	prevFrom, _ := timeutil.AmmanDayBoundsUTC(prevFromDate)
	_, prevTo := timeutil.AmmanDayBoundsUTC(prevToDate)

	ctx := c.Request.Context()
	byDay, err := h.repo.RevenueByDay(ctx, actor, curFrom, curTo)
	if err == nil {
		var byMonth any
		byMonth, err = h.repo.RevenueByMonth(ctx, actor, curFrom, curTo)
		if err == nil {
			heat, e2 := h.repo.BookingHeatmap(ctx, actor, curFrom, curTo)
			cur, e3 := h.repo.Totals(ctx, actor, curFrom, curTo)
			prev, e4 := h.repo.Totals(ctx, actor, prevFrom, prevTo)
			if e2 == nil && e3 == nil && e4 == nil {
				c.JSON(http.StatusOK, gin.H{"data": gin.H{
					"from": fromDate.Format("2006-01-02"), "to": toDate.Format("2006-01-02"),
					"revenue_by_day": byDay, "revenue_by_month": byMonth, "heatmap": heat,
					"current": cur, "previous": prev,
				}})
				return
			}
			err = firstErr(e2, e3, e4)
		}
	}
	c.Error(err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load analytics"})
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
