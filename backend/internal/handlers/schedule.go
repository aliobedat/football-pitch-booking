package handlers

import (
	"encoding/json"
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

	rows, err := h.repo.DailySchedule(c.Request.Context(), actor, scope.BoundPitchIDs, pitchFilter, fromUTC, toUTC)
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

	row, err := h.repo.SetAttendance(c.Request.Context(), actor, scope.BoundPitchIDs, bookingID, req.Attendance)
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

// PatchPayment records a booking's cash payment (WO-BOOKING-SHEET; extends WO-F1,
// data-only: no notification/side effects). PATCH /bookings/:id/payment accepts
// EITHER the legacy body { payment_status } OR the new { amount_paid, total_price? }
// form; mixing the two → 400. amount_paid is the source of truth and
// payment_status is kept in sync (the bridge) atomically. Staff-aware scope:
// out-of-scope/unknown → 404, block → 409 not_a_booking, cancelled → 409
// booking_cancelled. Staff may NOT change total_price → 403.
func (h *ScheduleHandler) PatchPayment(c *gin.Context) {
	bookingID, err := strconv.Atoi(c.Param("id"))
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}

	// Parse into raw keys so an ABSENT field is distinguishable from an explicit
	// null (amount_paid: null is a meaningful "revert to untracked").
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "malformed JSON body"})
		return
	}
	_, hasStatus := raw["payment_status"]
	amtRaw, hasAmount := raw["amount_paid"]
	totRaw, hasTotal := raw["total_price"]

	// Mixed legacy + new → 400. Empty body → 400.
	if hasStatus && (hasAmount || hasTotal) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ambiguous_payment_body", "message": "send either payment_status or amount_paid/total_price, not both"})
		return
	}
	if !hasStatus && !hasAmount && !hasTotal {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "payment_status or amount_paid is required"})
		return
	}

	actor := middleware.GetActor(c)
	scope := middleware.GetScope(c)

	// Staff carve-out: staff may not change total_price. Reject BEFORE any write so
	// the row is untouched.
	if hasTotal && actor.Role == auth.RoleStaff {
		c.JSON(http.StatusForbidden, gin.H{"error": "price_change_forbidden", "message": "staff cannot change the booking price"})
		return
	}

	var intent repository.PaymentIntent
	if hasStatus {
		var ps string
		if err := json.Unmarshal(raw["payment_status"], &ps); err != nil || !repository.IsValidPayment(ps) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_payment", "message": "payment_status must be unpaid|paid_cash"})
			return
		}
		if ps == "paid_cash" {
			intent.Mode = "legacy_paid"
		} else {
			intent.Mode = "legacy_unpaid"
		}
	} else {
		intent.Mode = "new"
		if hasAmount {
			intent.AmountPaidProvided = true
			// amount_paid may be an explicit null (→ untracked) or a number.
			if string(amtRaw) != "null" {
				var v float64
				if err := json.Unmarshal(amtRaw, &v); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_amount_paid", "message": "amount_paid must be a number or null"})
					return
				}
				if v < 0 {
					c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_amount_paid", "message": "amount_paid must be >= 0"})
					return
				}
				intent.AmountPaid = &v
			}
		}
		if hasTotal {
			var v float64
			if err := json.Unmarshal(totRaw, &v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_total_price", "message": "total_price must be a number"})
				return
			}
			if v < 0 {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_total_price", "message": "total_price must be >= 0"})
				return
			}
			intent.TotalPriceProvided = true
			intent.TotalPrice = v
		}
	}

	sheet, err := h.repo.ApplyPayment(c.Request.Context(), actor, scope.BoundPitchIDs, bookingID, intent)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrSheetNotInScope):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو خارج نطاق صلاحيتك"})
		case errors.Is(err, repository.ErrSheetBlock):
			c.JSON(http.StatusConflict, gin.H{"error": "not_a_booking", "message": "الفترات المحجوبة ليست حجوزات"})
		case errors.Is(err, repository.ErrSheetCancelled):
			c.JSON(http.StatusConflict, gin.H{"error": "booking_cancelled", "message": "لا يمكن تعديل الدفع لحجز ملغى"})
		case errors.Is(err, repository.ErrSheetPaidExceedsTotal):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "paid_exceeds_total", "message": "المبلغ المدفوع يتجاوز إجمالي الحجز"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not update payment"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sheet})
}
