package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

type BookingHandler struct {
	repo repository.BookingRepository
}

func NewBookingHandler(db *pgxpool.Pool) *BookingHandler {
	return &BookingHandler{
		repo: repository.NewBookingRepository(db),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/bookings
// ─────────────────────────────────────────────────────────────────────────────

func (h *BookingHandler) CreateBooking(c *gin.Context) {
	var req models.CreateBookingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	userID := int64(middleware.GetUserID(c))
	
	// إضافة رقم المستخدم للطلب قبل ما نبعثه للداتا بيس
	req.UserID = userID 

	now := time.Now().UTC()

	if !req.StartTime.After(now) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "start_time must be in the future",
		})
		return
	}
	if !req.EndTime.After(req.StartTime) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "end_time must be after start_time",
		})
		return
	}
	if req.EndTime.Sub(req.StartTime) < time.Hour {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_duration", "message": "minimum booking duration is 1 hour",
		})
		return
	}

	booking, err := h.repo.CreateBooking(c.Request.Context(), req)
	if err != nil {
		h.handleBookingError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "تم تأكيد طلب الحجز بنجاح",
		"data":    booking,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/bookings
// ─────────────────────────────────────────────────────────────────────────────

func (h *BookingHandler) GetUserBookings(c *gin.Context) {
	userID := int64(middleware.GetUserID(c))

	bookings, err := h.repo.GetUserBookings(c.Request.Context(), userID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "failed to retrieve bookings",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  bookings,
		"count": len(bookings),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/admin/bookings
// ─────────────────────────────────────────────────────────────────────────────

func (h *BookingHandler) GetAllBookings(c *gin.Context) {
	ownerID := int64(middleware.GetUserID(c))
	bookings, err := h.repo.GetAllBookings(c.Request.Context(), ownerID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "failed to retrieve bookings",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  bookings,
		"count": len(bookings),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/pitches/:id/availability  (unchanged)
// ─────────────────────────────────────────────────────────────────────────────

func (h *BookingHandler) GetPitchAvailability(c *gin.Context) {
	pitchID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	dateStr := c.Query("date")
	if dateStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "missing_param", "message": "query parameter 'date' is required (format: YYYY-MM-DD)",
		})
		return
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_date", "message": "date must be in YYYY-MM-DD format",
		})
		return
	}

	slots, err := h.repo.GetBookedSlots(c.Request.Context(), pitchID, date)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "failed to retrieve availability data",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pitch_id":     pitchID,
		"date":         dateStr,
		"booked_slots": slots,
		"count":        len(slots),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/bookings/:id/confirm                                   ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// ConfirmBooking transitions a booking from 'pending' → 'confirmed'.
// In production this endpoint will be protected by owner-role middleware.
// A confirmed booking is a binding commitment from the pitch owner.
func (h *BookingHandler) ConfirmBooking(c *gin.Context) {
	bookingID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	booking, err := h.repo.UpdateBookingStatus(
		c.Request.Context(),
		bookingID,
		models.StatusConfirmed,
		[]models.BookingStatus{models.StatusPending}, // only pending → confirmed is legal
	)
	if err != nil {
		h.handleBookingError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "booking confirmed",
		"data":    booking,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/bookings/:id/cancel                                    ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CancelBooking transitions a booking from 'pending' or 'confirmed' → 'cancelled'.
// Cancelling a 'cancelled' booking is rejected as an invalid transition.
// In production this endpoint will be protected to allow only the booking's
// owner player or the pitch owner to cancel.
func (h *BookingHandler) CancelBooking(c *gin.Context) {
	bookingID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	booking, err := h.repo.UpdateBookingStatus(
		c.Request.Context(),
		bookingID,
		models.StatusCancelled,
		[]models.BookingStatus{models.StatusPending, models.StatusConfirmed},
	)
	if err != nil {
		h.handleBookingError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "booking cancelled",
		"data":    booking,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseIDParam extracts a positive integer URL parameter by name.
// It writes a 400 response and returns false on failure, so callers
// can guard with a single `if !ok { return }`.
func parseIDParam(c *gin.Context, param string) (int, bool) {
	raw := c.Param(param)
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_param",
			"message": fmt.Sprintf("'%s' must be a positive integer", param),
		})
		return 0, false
	}
	return id, true
}

// handleBookingError is a single, centralised error-to-HTTP-response mapper
// for all booking operations. Keeping this logic in one place means that
// adding a new sentinel error updates every handler simultaneously.
func (h *BookingHandler) handleBookingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrDoubleBooking):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "slot_unavailable",
			"message": "the requested time slot is already booked for this pitch",
		})
	case errors.Is(err, repository.ErrPitchNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "pitch_not_found",
			"message": "the requested pitch does not exist or is not currently active",
		})
	case errors.Is(err, repository.ErrBookingNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "booking_not_found",
			"message": "no booking exists with the provided id",
		})
	case errors.Is(err, repository.ErrInvalidStatusTransition):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "invalid_transition",
			"message": "the booking's current status does not permit this operation",
		})
	default:
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "an unexpected error occurred, please try again",
		})
	}
}