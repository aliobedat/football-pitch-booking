package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ScheduleHandler serves the staff/owner daily schedule + attendance toggle.
// Routes are RequireRole("staff","owner","admin"); players are barred. Scope is
// resolved by middleware.ResolveScope and enforced in the repository SQL.
type ScheduleHandler struct {
	repo repository.ScheduleRepository
}

// NewScheduleHandler constructs a ScheduleHandler.
func NewScheduleHandler(repo repository.ScheduleRepository) *ScheduleHandler {
	return &ScheduleHandler{repo: repo}
}

// GetDailySchedule lists today's (default) non-cancelled occupancy for the
// in-scope pitch(es), time-ordered. GET /schedule?date=YYYY-MM-DD&pitch_id=<n>
// "today" is the Asia/Amman civil day.
func (h *ScheduleHandler) GetDailySchedule(c *gin.Context) {
	actor := middleware.GetActor(c)
	scope := middleware.GetScope(c)

	day := time.Now()
	if raw := c.Query("date"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, timeutil.Amman())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date", "message": "date must be YYYY-MM-DD"})
			return
		}
		day = d
	}
	fromUTC, toUTC := timeutil.AmmanDayBoundsUTC(day)

	pitchFilter := 0
	if raw := c.Query("pitch_id"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pitch_id", "message": "pitch_id must be a positive integer"})
			return
		}
		pitchFilter = v
	}

	rows, err := h.repo.DailySchedule(c.Request.Context(), actor, scope.BoundPitchID, pitchFilter, fromUTC, toUTC)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load schedule"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

type attendanceRequest struct {
	Attendance string `json:"attendance"`
}

// PatchAttendance sets a booking's attendance (data-only: no notification, block,
// or penalty). PATCH /bookings/:id/attendance  body { attendance }.
// Out-of-scope booking → 403.
func (h *ScheduleHandler) PatchAttendance(c *gin.Context) {
	bookingID, err := strconv.Atoi(c.Param("id"))
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}
	var req attendanceRequest
	if err := c.ShouldBindJSON(&req); err != nil || !repository.IsValidAttendance(req.Attendance) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_attendance", "message": "attendance must be pending|checked_in|no_show"})
		return
	}

	actor := middleware.GetActor(c)
	scope := middleware.GetScope(c)

	row, err := h.repo.SetAttendance(c.Request.Context(), actor, scope.BoundPitchID, bookingID, req.Attendance)
	if err != nil {
		if errors.Is(err, repository.ErrBookingNotInScope) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not_in_scope", "message": "this booking is not on a pitch you manage"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not update attendance"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": row})
}

type paymentRequest struct {
	PaymentStatus string `json:"payment_status"`
}

// PatchPayment sets a booking's cash-settlement marker (WO-F1, data-only: no
// notification or side effects). PATCH /bookings/:id/payment  body { payment_status }.
// Settable on any non-cancelled, in-scope booking. Out-of-scope → 403.
func (h *ScheduleHandler) PatchPayment(c *gin.Context) {
	bookingID, err := strconv.Atoi(c.Param("id"))
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}
	var req paymentRequest
	if err := c.ShouldBindJSON(&req); err != nil || !repository.IsValidPayment(req.PaymentStatus) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_payment", "message": "payment_status must be unpaid|paid_cash"})
		return
	}

	actor := middleware.GetActor(c)
	scope := middleware.GetScope(c)

	row, err := h.repo.SetPayment(c.Request.Context(), actor, scope.BoundPitchID, bookingID, req.PaymentStatus)
	if err != nil {
		if errors.Is(err, repository.ErrBookingNotInScope) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not_in_scope", "message": "this booking is not on a pitch you manage"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not update payment"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": row})
}
