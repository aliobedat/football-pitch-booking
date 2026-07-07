package handlers

// Booking extension endpoint (WO-BOOKING-SHEET / PR-A). Owner/admin only — staff
// are barred at the route (RequireRole) and re-asserted here. Grows an existing
// non-cancelled, non-block, not-yet-ended booking by 30 or 60 minutes, adding the
// SQL-computed additive price delta in one atomic UPDATE. The GIST EXCLUDE is the
// sole conflict referee (no availability pre-check); a violation → 409 slot_conflict.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// operatingHoursResolver is the slice of *data.PitchModel the extend handler
// needs: resolve a pitch's open windows for an Amman date (fail-open when
// unconfigured). Kept as an interface so the handler is unit-testable.
type operatingHoursResolver interface {
	ResolveOpenWindows(ctx context.Context, pitchID int, ammanDate time.Time) ([]data.ConcreteInterval, bool, error)
}

// BookingSheetHandler serves the owner/admin booking-extension endpoint.
type BookingSheetHandler struct {
	repo  repository.BookingSheetRepository
	hours operatingHoursResolver
}

// NewBookingSheetHandler constructs a BookingSheetHandler.
func NewBookingSheetHandler(repo repository.BookingSheetRepository, hours operatingHoursResolver) *BookingSheetHandler {
	return &BookingSheetHandler{repo: repo, hours: hours}
}

type extendRequest struct {
	Minutes int `json:"minutes"`
}

// ExtendBooking — PATCH /bookings/:id/extend  body { minutes: 30|60 }.
func (h *BookingSheetHandler) ExtendBooking(c *gin.Context) {
	actor := middleware.GetActor(c)
	// Owner/admin only (re-assert the route guard; staff cannot extend).
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "extending a booking is restricted to pitch owners",
		})
		return
	}

	bookingID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}

	var req extendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "malformed JSON body"})
		return
	}
	if req.Minutes != 30 && req.Minutes != 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_minutes", "message": "minutes must be 30 or 60"})
		return
	}

	// Pre-write snapshot for the block / cancelled / ended / hours checks.
	target, err := h.repo.LoadExtendTarget(c.Request.Context(), actor, bookingID)
	if err != nil {
		if errors.Is(err, repository.ErrSheetNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load the booking"})
		return
	}
	if target.Source == "block" {
		c.JSON(http.StatusConflict, gin.H{"error": "not_a_booking", "message": "الفترات المحجوبة ليست حجوزات"})
		return
	}
	if target.Status == "cancelled" {
		c.JSON(http.StatusConflict, gin.H{"error": "booking_cancelled", "message": "لا يمكن تمديد حجز ملغى"})
		return
	}
	if target.End.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "booking_ended", "message": "انتهى هذا الحجز ولا يمكن تمديده"})
		return
	}

	// Operating-hours gate on the extension interval [oldEnd, newEnd). Resolve the
	// windows for the Amman civil date of the extension START (oldEnd) — the date
	// SlotContained's candidate set (incl. previous-day cross-midnight spill) must
	// anchor on. Fail-open: an unconfigured pitch (hasSchedule=false) is unbounded.
	newEnd := target.End.Add(time.Duration(req.Minutes) * time.Minute)
	ammanDate := target.End.In(timeutil.Amman())
	intervals, hasSchedule, err := h.hours.ResolveOpenWindows(c.Request.Context(), int(target.PitchID), ammanDate)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve operating hours"})
		return
	}
	if hasSchedule && !data.SlotContained(target.End, newEnd, intervals) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "outside_operating_hours", "message": "التمديد خارج ساعات عمل الملعب"})
		return
	}

	// Single atomic UPDATE. EXCLUDE (23P01) is the sole conflict referee → 409.
	sheet, err := h.repo.ApplyExtend(c.Request.Context(), actor, bookingID, req.Minutes)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrSheetConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "slot_conflict", "message": "الوقت الجديد يتعارض مع حجز آخر"})
		case errors.Is(err, repository.ErrSheetNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not extend the booking"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sheet})
}
