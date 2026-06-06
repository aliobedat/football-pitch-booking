package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// idempotencyHeader is the request header carrying the client's per-attempt UUID.
const idempotencyHeader = "Idempotency-Key"

// bookingEndpoint labels the idempotency record's origin (audit/debug only).
const bookingEndpoint = "POST /api/v1/bookings"

// bookingFingerprint hashes the SEMANTIC content of a booking request (pitch +
// time range) so the same idempotency key reused with a different booking is
// detected (→ 422). total_price is excluded: the server recomputes it, so it is
// not part of the request's identity. The user is scoped separately (per-user
// key), so it is not hashed here.
func bookingFingerprint(req models.CreateBookingRequest) string {
	canonical := fmt.Sprintf("pitch=%d;start=%d;end=%d",
		req.PitchID, req.StartTime.UTC().UnixNano(), req.EndTime.UTC().UnixNano())
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// BookingService is the orchestration seam the handler depends on for state
// transitions (PART 5). *booking.Service satisfies it: each call persists the
// transition with its audit row and dispatches the player notification. Reads
// stay on the repository — they neither mutate state nor notify. Defining the
// interface here (rather than importing the concrete type) keeps the handler
// testable with a recording fake.
type BookingService interface {
	Create(ctx context.Context, req models.CreateBookingRequest) (*models.Booking, error)
	Cancel(ctx context.Context, params repository.CancelBookingParams) (*models.Booking, error)
}

type BookingHandler struct {
	repo    repository.BookingRepository // read paths: list + availability
	service BookingService               // write paths: create + cancel (audited + notified)
}

func NewBookingHandler(db *pgxpool.Pool, service BookingService) *BookingHandler {
	return &BookingHandler{
		repo:    repository.NewBookingRepository(db),
		service: service,
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

	// إضافة رقم اللاعب للطلب قبل ما نبعثه للداتا بيس
	req.PlayerID = userID

	// Idempotency: when the client supplies an Idempotency-Key, attach it so a
	// double-tap / retry replays the original booking instead of creating a second
	// one. Absent header → legacy non-idempotent path (unchanged behaviour).
	if key := strings.TrimSpace(c.GetHeader(idempotencyHeader)); key != "" {
		req.Idempotency = &models.IdempotencyParams{
			Key:         key,
			Endpoint:    bookingEndpoint,
			Fingerprint: bookingFingerprint(req),
		}
	}

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

	// Route through the service so the confirmed booking is audited and the
	// player receives a booking_confirmed notification.
	booking, err := h.service.Create(c.Request.Context(), req)
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
	// Admin → all bookings; owner → only bookings for pitches they own. Scoping
	// is enforced in SQL by the repository via the Actor.
	bookings, err := h.repo.GetAllBookings(c.Request.Context(), middleware.GetActor(c))
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
		if errors.Is(err, repository.ErrPitchNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "pitch_not_found",
				"message": "الملعب غير موجود أو غير متاح",
			})
			return
		}
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
// PATCH /api/v1/bookings/:id/cancel                                    ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CancelBooking transitions a confirmed booking → 'cancelled', releasing the
// slot. Cancelling a non-confirmed booking is rejected as an invalid
// transition. The route is restricted to the player or pitch owner; the actor's
// id and role are captured in the audit trail and the player is notified via the
// service. An optional `reason` may be supplied in the request body; when
// omitted the service defaults it from the actor role.
func (h *BookingHandler) CancelBooking(c *gin.Context) {
	bookingID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	// The body is optional — a bare PATCH with no reason is valid.
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	actorID := int64(middleware.GetUserID(c))
	booking, err := h.service.Cancel(c.Request.Context(), repository.CancelBookingParams{
		BookingID: int64(bookingID),
		ActorID:   &actorID,
		ActorRole: middleware.GetUserRole(c),
		Reason:    body.Reason,
	})
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
	case errors.Is(err, repository.ErrPitchNotBookable):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "pitch_not_bookable",
			"message": "الملعب غير متاح للحجز",
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
	case errors.Is(err, repository.ErrIdempotencyInProgress):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "request_in_progress",
			"message": "a booking request with this idempotency key is already being processed",
		})
	case errors.Is(err, repository.ErrIdempotencyKeyConflict):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "idempotency_key_conflict",
			"message": "this idempotency key was already used for a different booking request",
		})
	default:
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "an unexpected error occurred, please try again",
		})
	}
}