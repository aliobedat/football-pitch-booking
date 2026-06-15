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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
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

	// Resolve the pitch's open windows for the requested date so the client renders
	// bookable / booked / CLOSED (closed ≠ booked). open_windows are absolute UTC
	// [start,end) intervals — the SAME referee the write-path gate uses, so the UI
	// can never offer a slot the server will reject. has_schedule=false means the
	// pitch is unconfigured → open 24/7 (the client shows the whole day as open).
	openWindows, hasSchedule, err := h.repo.GetOpenWindows(c.Request.Context(), pitchID, date)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "failed to retrieve availability data",
		})
		return
	}
	if openWindows == nil {
		openWindows = []data.ConcreteInterval{} // serialise [] not null
	}

	c.JSON(http.StatusOK, gin.H{
		"pitch_id":     pitchID,
		"date":         dateStr,
		"booked_slots": slots,
		"count":        len(slots),
		"open_windows": openWindows,
		"has_schedule": hasSchedule,
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
// POST /api/v1/pitches/:id/blocks                                       ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CreateBlock creates an owner/admin BLOCK on a pitch: held time with no player.
// It is owner/admin-scoped (RequireRole at the route) and resolves the pitch under
// a FOR UPDATE lock with the ownership predicate. The operating-hours gate is NOT
// applied (owner bypass, locked decision #2) — block creation goes through a
// distinct repository path (CreateBlock), not the player write-path. On overlap
// with any non-cancelled booking it returns 409 with the conflict detail so the
// dashboard can tell the owner exactly what to cancel first.
func (h *BookingHandler) CreateBlock(c *gin.Context) {
	pitchID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	var req struct {
		StartTime time.Time `json:"start_time" binding:"required"`
		EndTime   time.Time `json:"end_time"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	if !req.EndTime.After(req.StartTime) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "end_time must be after start_time",
		})
		return
	}
	if !req.EndTime.After(time.Now().UTC()) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "cannot block a time range entirely in the past",
		})
		return
	}

	block, err := h.repo.CreateBlock(c.Request.Context(), repository.CreateBlockParams{
		PitchID:   int64(pitchID),
		Actor:     middleware.GetActor(c),
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	})
	if err != nil {
		var conflict *repository.BlockConflictError
		switch {
		case errors.As(err, &conflict):
			c.JSON(http.StatusConflict, gin.H{
				"error":     "slot_conflict",
				"message":   "النطاق المطلوب يتعارض مع حجز قائم — يجب إلغاؤه أولاً",
				"conflicts": conflict.Conflicts,
			})
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_error", "message": "تعذّر إنشاء الحجب، حاول مجدداً",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "تم حجب الموعد", "data": block})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/pitches/:id/bookings/manual                              ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CreateManualBooking logs an owner/admin offline (walk-in / phone) booking:
// real occupancy with no platform player (player_id NULL) but a named guest. It is
// owner/admin-scoped (RequireRole) and reuses the Blocks locked-resolve + overlap
// pre-check. It HONOURS the operating-hours gate unless force_bypass_hours is true
// (the soft override: the UI first submits without it, catches the 422, confirms
// with the owner, then resubmits with it set). On overlap it returns 409 with the
// conflict detail (now null-safe for non-player conflicting rows).
func (h *BookingHandler) CreateManualBooking(c *gin.Context) {
	pitchID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	var req struct {
		StartTime         time.Time `json:"start_time"  binding:"required"`
		EndTime           time.Time `json:"end_time"    binding:"required"`
		GuestName         string    `json:"guest_name"  binding:"required"`
		GuestPhone        string    `json:"guest_phone"`
		ForceBypassHours  bool      `json:"force_bypass_hours"`
		RepeatWeeks       int       `json:"repeat_weeks"`
		RecurrenceGroupID string    `json:"recurrence_group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	req.GuestName = strings.TrimSpace(req.GuestName)
	req.GuestPhone = strings.TrimSpace(req.GuestPhone)
	req.RecurrenceGroupID = strings.TrimSpace(req.RecurrenceGroupID)
	if req.GuestName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_guest", "message": "اسم الضيف مطلوب",
		})
		return
	}
	if !req.EndTime.After(req.StartTime) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "end_time must be after start_time",
		})
		return
	}
	if !req.EndTime.After(time.Now().UTC()) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_time", "message": "cannot log a booking entirely in the past",
		})
		return
	}

	// Recurrence: default 1, cap at 52 occurrences — reject an oversize series
	// BEFORE acquiring the lock or writing anything.
	if req.RepeatWeeks == 0 {
		req.RepeatWeeks = 1
	}
	if req.RepeatWeeks < 1 || req.RepeatWeeks > 52 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_repeat", "message": "عدد الأسابيع يجب أن يكون بين 1 و 52",
		})
		return
	}
	// A multi-week series MUST carry a recurrence_group_id — it is the only handle
	// for bulk-cancelling future occurrences. Reject an orphan series up front (before
	// any lock or write) so we never materialise an un-cancellable group.
	if req.RepeatWeeks > 1 && req.RecurrenceGroupID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "missing_group_id",
			"message": "الحجز المتكرر يتطلب معرّف تكرار (recurrence_group_id)",
		})
		return
	}
	// A recurrence_group_id, when supplied, must be a valid UUID (it is the
	// idempotency key + bulk-cancel handle).
	if req.RecurrenceGroupID != "" {
		if _, err := uuid.Parse(req.RecurrenceGroupID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid_group_id", "message": "معرّف التكرار غير صالح",
			})
			return
		}
	}

	bookings, replayed, err := h.repo.CreateManualBooking(c.Request.Context(), repository.ManualBookingParams{
		PitchID:           int64(pitchID),
		Actor:             middleware.GetActor(c),
		StartTime:         req.StartTime,
		EndTime:           req.EndTime,
		GuestName:         req.GuestName,
		GuestPhone:        req.GuestPhone,
		BypassHours:       req.ForceBypassHours,
		RepeatWeeks:       req.RepeatWeeks,
		RecurrenceGroupID: req.RecurrenceGroupID,
	})
	if err != nil {
		var rec *repository.RecurrenceConflictError
		switch {
		case errors.As(err, &rec):
			// Name the failing week + occurrence so the UI can point the owner at it.
			occ := gin.H{"week": rec.Week, "start": rec.OccStart, "end": rec.OccEnd}
			if rec.Reason == "outside_hours" {
				// Keep the error code the soft-override interceptor keys on.
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error":      "outside_operating_hours",
					"message":    "الوقت المطلوب خارج ساعات عمل الملعب",
					"occurrence": occ,
				})
			} else {
				c.JSON(http.StatusConflict, gin.H{
					"error":      "slot_conflict",
					"message":    "النطاق المطلوب يتعارض مع حجز قائم — يجب إلغاؤه أولاً",
					"occurrence": occ,
					"conflicts":  rec.Conflicts,
				})
			}
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
		default:
			h.handleBookingError(c, err)
		}
		return
	}

	// Replay (idempotent resubmit) → 200; a fresh materialization → 201.
	status := http.StatusCreated
	msg := "تم تسجيل الحجز اليدوي"
	if replayed {
		status = http.StatusOK
		msg = "تم استرجاع الحجز الحالي"
	}
	c.JSON(status, gin.H{"message": msg, "data": bookings, "count": len(bookings)})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/v1/pitches/:id/bookings/group/:groupId                    ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CancelGroup bulk-cancels every NON-PAST occurrence of a recurring walk-in group
// on the pitch (owner/admin-scoped), auditing each in one set-based transaction.
// Past occurrences are preserved as history; already-cancelled rows are skipped
// (idempotent re-cancel). An empty match is a valid 200 with cancelled_count:0 —
// NOT a 404 — so the UI never has to special-case "nothing to cancel". Single-
// occurrence cancellation stays on the standard PATCH /bookings/:id/cancel path.
func (h *BookingHandler) CancelGroup(c *gin.Context) {
	pitchID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	groupID := strings.TrimSpace(c.Param("groupId"))
	if _, err := uuid.Parse(groupID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_group_id", "message": "معرّف التكرار غير صالح",
		})
		return
	}

	n, err := h.repo.CancelFutureGroup(c.Request.Context(), repository.CancelGroupParams{
		PitchID: int64(pitchID),
		GroupID: groupID,
		Actor:   middleware.GetActor(c),
		ActorID: int64(middleware.GetUserID(c)),
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "تعذّر إلغاء الحجوزات، حاول مجدداً",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "تم إلغاء الحجوزات القادمة",
		"cancelled_count": n,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/v1/pitches/:id/blocks/:bookingId                          ← NEW
// ─────────────────────────────────────────────────────────────────────────────

// CancelBlock removes (cancels) a block, releasing the slot. It reuses the
// source-aware cancellation service: RequireSource="block" means the scoped
// resolve refuses any non-block row (→ 404), and the service's notify guard skips
// dispatch for a non-player source. Owner/admin-scoped at the route + in the
// resolve's ownership predicate.
func (h *BookingHandler) CancelBlock(c *gin.Context) {
	if _, ok := parseIDParam(c, "id"); !ok {
		return
	}
	bookingID, ok := parseIDParam(c, "bookingId")
	if !ok {
		return
	}

	actorID := int64(middleware.GetUserID(c))
	if _, err := h.service.Cancel(c.Request.Context(), repository.CancelBookingParams{
		BookingID:     int64(bookingID),
		ActorID:       &actorID,
		ActorRole:     middleware.GetUserRole(c),
		RequireSource: string(models.SourceBlock),
	}); err != nil {
		h.handleBookingError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "تم رفع الحجب"})
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
	case errors.Is(err, repository.ErrSlotOutsideOperatingHours):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "outside_operating_hours",
			"message": "الوقت المطلوب خارج ساعات عمل الملعب",
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