package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/geo"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/pitches/availability  (public)
//
// Player enters a date + start time (+ optional browser coordinates); returns
// player-visible pitches open at that start and free from it for ≥ 60 minutes,
// each with the continuous duration available, sorted nearest-first when both the
// player and the pitch have usable coordinates. No identity required (browse
// funnel). Never cached — availability is live.
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) SearchAvailability(c *gin.Context) {
	// Availability is live, per-request state — never store it in any cache.
	c.Header("Cache-Control", "no-store")

	dateStr := c.Query("date")
	startStr := c.Query("start_time")
	if dateStr == "" || startStr == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "missing_param", "message": "date (YYYY-MM-DD) and start_time (HH:MM) are required",
		})
		return
	}

	// Parse the civil date in Amman, then place the wall-clock start on it. Building
	// the instant in Asia/Amman keeps the whole search in one timezone.
	loc := timeutil.Amman()
	day, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_date", "message": "date must be YYYY-MM-DD",
		})
		return
	}
	hh, mm, ok := parseHHMM(startStr)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "start_time must be HH:MM (24h)",
		})
		return
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)

	if !start.After(time.Now()) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "past_start", "message": "start time must be in the future",
		})
		return
	}

	// Optional player coordinates: both-or-neither. One alone is malformed input.
	player, ok := parsePlayerCoords(c)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_coords", "message": "lat and lng must be provided together as numbers",
		})
		return
	}

	results, err := h.Model.SearchAvailability(c.Request.Context(), data.AvailabilityQuery{
		AmmanDate: day,
		Start:     start,
		Player:    player,
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "failed to search availability",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"date":       dateStr,
		"start_time": startStr,
		"results":    results,
		"count":      len(results),
	})
}

// parseHHMM parses a 24-hour "HH:MM" string into hour/minute, validating ranges.
func parseHHMM(s string) (hour, min int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(s[0:2])
	mi, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || mi < 0 || mi > 59 {
		return 0, 0, false
	}
	return h, mi, true
}

// parsePlayerCoords reads optional lat/lng query params. Returns (zero, true) when
// neither is present (no location), the parsed pair when both are valid, and
// (zero, false) when exactly one is present or either fails to parse.
func parsePlayerCoords(c *gin.Context) (geo.Coordinates, bool) {
	latStr := c.Query("lat")
	lngStr := c.Query("lng")
	if latStr == "" && lngStr == "" {
		return geo.Coordinates{}, true // no location supplied — valid
	}
	if latStr == "" || lngStr == "" {
		return geo.Coordinates{}, false // incomplete pair
	}
	lat, err1 := strconv.ParseFloat(latStr, 64)
	lng, err2 := strconv.ParseFloat(lngStr, 64)
	if err1 != nil || err2 != nil {
		return geo.Coordinates{}, false
	}
	return geo.Coordinates{Lat: &lat, Lng: &lng}, true
}
