package handlers

// Reports (R1) — two read-only owner statements over an explicit Amman civil-date
// window: GET /owner/reports/financial and GET /owner/reports/bookings. Owner/
// admin only (staff barred at the route AND re-asserted here). Revenue predicates
// are ratified to equal OwnerTimeSeries (dashboard parity); attribution is
// strictly by booking START within [from 00:00 Amman, to+1d 00:00 Amman) in UTC.
//
// Unlike analytics (which defaults to a trailing window), a statement requires an
// explicit period: from/to are REQUIRED, to >= from, and the range is capped at
// 92 days. An out-of-scope/unknown pitch_id → 404 (day-view convention — a
// statement must never render silently empty for a wrong pitch; intentional fork
// from analytics' zeros).

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// maxReportRangeDays caps the inclusive from..to span of one statement.
const maxReportRangeDays = 92

// maxReportRows caps the bookings statement's row count (422 above it).
const maxReportRows = 3000

type ReportsHandler struct {
	repo repository.ReportsRepository
}

func NewReportsHandler(repo repository.ReportsRepository) *ReportsHandler {
	return &ReportsHandler{repo: repo}
}

// reportParams is the validated query window shared by both endpoints.
type reportParams struct {
	pitchID   int64
	fromStr   string    // echo of ?from (YYYY-MM-DD, Amman)
	toStr     string    // echo of ?to
	fromStart time.Time // from 00:00 Amman, UTC
	toEnd     time.Time // (to+1day) 00:00 Amman, UTC — exclusive
}

// parseReportParams validates ?from&to&pitch_id and writes the 400 itself on
// failure (returns ok=false). from/to are required Amman civil dates.
func parseReportParams(c *gin.Context) (reportParams, bool) {
	var p reportParams

	p.fromStr = strings.TrimSpace(c.Query("from"))
	p.toStr = strings.TrimSpace(c.Query("to"))
	if p.fromStr == "" || p.toStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "missing_param", "message": "query parameters 'from' and 'to' are required (YYYY-MM-DD)",
		})
		return p, false
	}
	dFrom, err := time.Parse("2006-01-02", p.fromStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "from must be YYYY-MM-DD"})
		return p, false
	}
	dTo, err := time.Parse("2006-01-02", p.toStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "to must be YYYY-MM-DD"})
		return p, false
	}
	if dTo.Before(dFrom) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_range", "message": "'to' must be on or after 'from'"})
		return p, false
	}
	// Both parse to midnight UTC, so the difference is whole days; +1 = inclusive span.
	if int(dTo.Sub(dFrom).Hours()/24)+1 > maxReportRangeDays {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "range_too_large", "message": "date range must not exceed 92 days",
		})
		return p, false
	}

	// Amman civil dates → half-open UTC window (the analytics from/to pattern):
	// start of `from`'s Amman day .. start of the day AFTER `to`'s Amman day.
	p.fromStart, _ = timeutil.AmmanDayBoundsUTC(dFrom)
	_, p.toEnd = timeutil.AmmanDayBoundsUTC(dTo)

	if raw := c.Query("pitch_id"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid_pitch_id", "message": "pitch_id must be a positive integer",
			})
			return p, false
		}
		p.pitchID = v
	}
	return p, true
}

// resolveReportScope re-asserts the finance boundary and (when pitch_id is set)
// resolves the pitch under owner scope, writing 403/404/500 itself. Returns the
// pitch name ("" when unfiltered) and ok.
func (h *ReportsHandler) resolveReportScope(c *gin.Context, p reportParams) (string, bool) {
	actor := middleware.GetActor(c)
	if !requireOwnerOrAdmin(c, actor) {
		return "", false
	}
	if p.pitchID == 0 {
		return "", true
	}
	name, err := h.repo.ResolveReportPitch(c.Request.Context(), actor, p.pitchID)
	if err != nil {
		if errors.Is(err, repository.ErrPitchNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية عرضه",
			})
			return "", false
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load report"})
		return "", false
	}
	return name, true
}

// GetFinancialReport — GET /owner/reports/financial?from=&to=&pitch_id=
func (h *ReportsHandler) GetFinancialReport(c *gin.Context) {
	p, ok := parseReportParams(c)
	if !ok {
		return
	}
	pitchName, ok := h.resolveReportScope(c, p)
	if !ok {
		return
	}
	actor := middleware.GetActor(c)

	rep, err := h.repo.OwnerFinancialReport(c.Request.Context(), actor, p.pitchID, p.fromStart, p.toEnd)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load report"})
		return
	}

	// round3 every money leg at the boundary (the house financials pattern).
	rep.Summary.GrossRevenue = round3(rep.Summary.GrossRevenue)
	rep.Summary.Collected = round3(rep.Summary.Collected)
	rep.Summary.Outstanding = round3(rep.Summary.Outstanding)
	for i := range rep.ByDay {
		rep.ByDay[i].GrossRevenue = round3(rep.ByDay[i].GrossRevenue)
		rep.ByDay[i].Collected = round3(rep.ByDay[i].Collected)
	}
	for i := range rep.ByPitch {
		rep.ByPitch[i].GrossRevenue = round3(rep.ByPitch[i].GrossRevenue)
		rep.ByPitch[i].Collected = round3(rep.ByPitch[i].Collected)
	}

	resp := gin.H{
		"from":    p.fromStr,
		"to":      p.toStr,
		"summary": rep.Summary,
		"by_day":  rep.ByDay,
	}
	if p.pitchID > 0 {
		resp["pitch_id"] = p.pitchID
		resp["pitch_name"] = pitchName
	} else {
		resp["by_pitch"] = rep.ByPitch
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetBookingsReport — GET /owner/reports/bookings?from=&to=&pitch_id=
func (h *ReportsHandler) GetBookingsReport(c *gin.Context) {
	p, ok := parseReportParams(c)
	if !ok {
		return
	}
	pitchName, ok := h.resolveReportScope(c, p)
	if !ok {
		return
	}
	actor := middleware.GetActor(c)

	rep, err := h.repo.OwnerBookingsReport(c.Request.Context(), actor, p.pitchID, p.fromStart, p.toEnd, maxReportRows)
	if err != nil {
		if errors.Is(err, repository.ErrReportTooLarge) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   "too_many_rows",
				"message": "the requested range returns too many rows; narrow the date range",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load report"})
		return
	}

	for i := range rep.Rows {
		rep.Rows[i].TotalPrice = round3(rep.Rows[i].TotalPrice)
	}

	resp := gin.H{
		"from":    p.fromStr,
		"to":      p.toStr,
		"summary": rep.Summary,
		"rows":    rep.Rows,
	}
	if p.pitchID > 0 {
		resp["pitch_id"] = p.pitchID
		resp["pitch_name"] = pitchName
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}
