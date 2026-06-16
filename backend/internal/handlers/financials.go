package handlers

// Financials — Net Profit engine (Cockpit WO-F2). Owner/admin only. Net is strictly
// cash-basis: Net = COLLECTED − Expenses, per Amman period. The COLLECTED leg is
// NOT re-derived here — it REUSES WO-F1's existing OwnerTimeSeries aggregation
// (paid_cash, same buckets, same Asia/Amman basis); this handler only subtracts the
// expense leg (bucketed identically) and assembles the equation.

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type FinancialsHandler struct {
	analytics repository.AnalyticsRepository
	expenses  repository.ExpenseRepository
}

func NewFinancialsHandler(a repository.AnalyticsRepository, e repository.ExpenseRepository) *FinancialsHandler {
	return &FinancialsHandler{analytics: a, expenses: e}
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// GetNetSummary — GET /owner/financials?from=&to=&granularity=day|week|month
// Collected − Expenses = Net, with a per-bucket series and per-category subtotals.
func (h *FinancialsHandler) GetNetSummary(c *gin.Context) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "financial data is restricted to pitch owners",
		})
		return
	}

	granularity := strings.TrimSpace(c.DefaultQuery("granularity", "day"))
	switch granularity {
	case "day", "week", "month":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_granularity", "message": "granularity must be day|week|month"})
		return
	}

	// Default window: trailing 30 Amman days through today (same convention as the
	// analytics timeseries), overridable by from/to.
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

	// COLLECTED leg — REUSE F1's exact aggregation (no re-derivation).
	series, err := h.analytics.OwnerTimeSeries(c.Request.Context(), actor, repository.TimeSeriesParams{
		Granularity: granularity, From: fromStart, To: toEnd,
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load financials"})
		return
	}
	// Expense leg — bucketed on the SAME Amman basis.
	expByBucket, err := h.expenses.ByBucket(c.Request.Context(), actor, granularity, fromStart, toEnd)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load financials"})
		return
	}
	byCategory, err := h.expenses.ByCategory(c.Request.Context(), actor, fromStart, toEnd)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load financials"})
		return
	}

	// Merge the two legs over the union of bucket keys.
	collectedByBucket := map[string]float64{}
	for _, b := range series {
		collectedByBucket[b.Bucket] = b.Collected
	}
	keys := map[string]struct{}{}
	for k := range collectedByBucket {
		keys[k] = struct{}{}
	}
	for k := range expByBucket {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var totalCollected, totalExpenses float64
	netSeries := make([]models.NetBucket, 0, len(ordered))
	for _, k := range ordered {
		col := round3(collectedByBucket[k])
		exp := round3(expByBucket[k])
		totalCollected += col
		totalExpenses += exp
		netSeries = append(netSeries, models.NetBucket{
			Bucket: k, Collected: col, Expenses: exp, Net: round3(col - exp),
		})
	}
	totalCollected = round3(totalCollected)
	totalExpenses = round3(totalExpenses)

	c.JSON(http.StatusOK, gin.H{"data": models.NetSummary{
		From:       toDateStr(fromStart),
		To:         toDateStr(toEnd.Add(-time.Second)), // exclusive end → inclusive last day
		Collected:  totalCollected,
		Expenses:   totalExpenses,
		Net:        round3(totalCollected - totalExpenses),
		ByCategory: byCategory,
		Series:     netSeries,
	}})
}

// toDateStr renders a UTC instant as the Amman calendar date it falls in.
func toDateStr(t time.Time) string { return timeutil.InAmman(t).Format("2006-01-02") }
