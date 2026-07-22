package handlers

// WO-MONITORING-V1 Gate 1 — GET /admin/monitoring: the smallest read-only
// admin monitoring page backend. Admin-only (RequireRole("admin") at the
// route, re-asserted here as defence in depth, matching DeleteReview's
// pattern). Returns a backend-owned DTO — never raw DB models, never a raw
// phone number, never a raw notification_jobs.last_error string.

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

const (
	monitoringRecentBookingsLimit = 25
	monitoringRecentFailuresLimit = 10
)

// validMonitoringStatuses mirrors the booking_status enum (schema.sql).
var validMonitoringStatuses = map[string]bool{
	"pending": true, "confirmed": true, "rejected": true,
	"completed": true, "cancelled": true, "no_show": true,
}

type MonitoringHandler struct {
	repo   repository.MonitoringRepository
	wabaID string
}

func NewMonitoringHandler(repo repository.MonitoringRepository, wabaID string) *MonitoringHandler {
	return &MonitoringHandler{repo: repo, wabaID: wabaID}
}

// GetMonitoring — GET /admin/monitoring?date=&venue_id=&status=
func (h *MonitoringHandler) GetMonitoring(c *gin.Context) {
	// Defence in depth: the route is RequireRole("admin"); re-assert so this
	// cross-tenant, unscoped view can never run for a non-admin even if the
	// route were ever re-wired (mirrors DeleteReview's pattern).
	if middleware.GetActor(c).Role != auth.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "you do not have permission to access this resource",
		})
		return
	}

	selectedDate := time.Now()
	if raw := strings.TrimSpace(c.Query("date")); raw != "" {
		d, err := time.Parse("2006-01-02", raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "date must be YYYY-MM-DD"})
			return
		}
		selectedDate = d
	}
	dayStart, dayEnd := timeutil.AmmanDayBoundsUTC(selectedDate)

	var filter repository.MonitoringFilter
	if raw := strings.TrimSpace(c.Query("venue_id")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_venue_id", "message": "venue_id must be a positive integer"})
			return
		}
		filter.VenueID = v
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		if !validMonitoringStatuses[raw] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_status", "message": "status is not a recognised booking status"})
			return
		}
		filter.Status = raw
	}

	ctx := c.Request.Context()

	summary, err := h.repo.BookingSummary(ctx, dayStart, dayEnd, filter)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load monitoring data"})
		return
	}
	recentBookings, err := h.repo.RecentBookings(ctx, dayStart, dayEnd, filter, monitoringRecentBookingsLimit)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load monitoring data"})
		return
	}
	usage, err := h.repo.WhatsAppUsage(ctx, h.wabaID, time.Now())
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load monitoring data"})
		return
	}
	jobCounts, err := h.repo.NotificationJobCounts(ctx)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load monitoring data"})
		return
	}
	recentFailures, err := h.repo.RecentFailedJobs(ctx, monitoringRecentFailuresLimit)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load monitoring data"})
		return
	}

	bookingRows := make([]gin.H, 0, len(recentBookings))
	for _, b := range recentBookings {
		bookingRows = append(bookingRows, gin.H{
			"id":                   b.ID,
			"created_at":           b.CreatedAt,
			"contact_name":         b.ContactName,
			"contact_phone_masked": b.ContactPhoneMasked,
			"venue_id":             b.VenueID,
			"venue_name":           b.VenueName,
			"pitch_id":             b.PitchID,
			"pitch_name":           b.PitchName,
			"start_time":           b.StartTime,
			"end_time":             b.EndTime,
			"status":               b.Status,
		})
	}

	failureRows := make([]gin.H, 0, len(recentFailures))
	for _, j := range recentFailures {
		failureRows = append(failureRows, gin.H{
			"kind":             j.Kind,
			"status":           j.Status,
			"attempts":         j.Attempts,
			"failure_category": j.FailureCategory,
			"recipient_masked": j.RecipientMasked,
			"updated_at":       j.UpdatedAt,
		})
	}

	remaining := max(usage.Cap-usage.Count, 0)

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"selected_date": selectedDate.Format("2006-01-02"),
			"booking_summary": gin.H{
				"total":     summary.Total,
				"pending":   summary.Pending,
				"confirmed": summary.Confirmed,
				"rejected":  summary.Rejected,
				"completed": summary.Completed,
				"cancelled": summary.Cancelled,
				"no_show":   summary.NoShow,
			},
			"recent_bookings": bookingRows,
			"whatsapp_usage": gin.H{
				"count":     usage.Count,
				"cap":       usage.Cap,
				"remaining": remaining,
				"warning":   usage.Warning,
				"blocked":   usage.Count > usage.Cap,
			},
			"notification_jobs": gin.H{
				"pending":         jobCounts.Pending,
				"retrying":        jobCounts.Retrying,
				"processing":      jobCounts.Processing,
				"succeeded":       jobCounts.Succeeded,
				"dead_letter":     jobCounts.DeadLetter,
				"blocked":         jobCounts.Blocked,
				"recent_failures": failureRows,
			},
		},
	})
}
