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
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/phone"
	"github.com/ali/football-pitch-api/internal/repository"
)

// operatingHoursResolver is the slice of *data.PitchModel the extend handler
// needs: the MERGED operating-hours gate over a concrete interval (fail-open
// when unconfigured). Kept as an interface so the handler is unit-testable.
type operatingHoursResolver interface {
	SlotWithinOpenHours(ctx context.Context, pitchID int, slotStart, slotEnd time.Time) (contained bool, hasSchedule bool, err error)
}

// BookingSheetHandler serves the owner/admin booking-extension endpoint.
type BookingSheetHandler struct {
	repo      repository.BookingSheetRepository
	hours     operatingHoursResolver
	customers customerAssociator // WO-BOOKING-EDIT-CONTACT: fail-open CRM re-link on phone edit
}

// NewBookingSheetHandler constructs a BookingSheetHandler.
func NewBookingSheetHandler(repo repository.BookingSheetRepository, hours operatingHoursResolver) *BookingSheetHandler {
	return &BookingSheetHandler{repo: repo, hours: hours}
}

// WithCustomers enables go-forward CRM re-association after a contact edit.
// Mirrors BookingHandler.WithCustomers — kept separate from the constructor so
// existing call sites/tests are unaffected.
func (h *BookingSheetHandler) WithCustomers(c customerAssociator) *BookingSheetHandler {
	h.customers = c
	return h
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

	// Operating-hours gate on the extension interval [oldEnd, newEnd), via the
	// MERGED gate (WO-24H-CONTINUITY): anchors every day the interval touches
	// and coalesces abutting windows, so extending past midnight on a 24/7
	// pitch is accepted — same referee as the create paths. Fail-open: an
	// unconfigured pitch (hasSchedule=false) returns contained=true.
	newEnd := target.End.Add(time.Duration(req.Minutes) * time.Minute)
	contained, _, err := h.hours.SlotWithinOpenHours(c.Request.Context(), int(target.PitchID), target.End, newEnd)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve operating hours"})
		return
	}
	if !contained {
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

// ── Contact (WO-BOOKING-EDIT-CONTACT) ────────────────────────────────────────
//
// Owner/admin correct the guest name/phone on an existing manual/academy
// booking. Scope, source, and status are enforced entirely in the repository's
// WHERE clause (single atomic statement, no pre-check SELECT); pgx.ErrNoRows
// deliberately conflates nonexistent / cross-tenant / source='player' /
// source='block' / cancelled into one 404 (existence not leaked). No
// notification, no status_transitions row — this is not a status change.

type contactRequest struct {
	GuestName  *string `json:"guest_name"`
	GuestPhone *string `json:"guest_phone"`
}

// requireOwnerOrAdmin re-asserts the route guard in-handler, mirroring
// ExtendBooking's bar (staff cannot touch contact fields).
func (h *BookingSheetHandler) requireOwnerOrAdmin(c *gin.Context) (auth.Actor, bool) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "تعديل بيانات الزبون متاح فقط لمالك أو مدير الملعب",
		})
		return auth.Actor{}, false
	}
	return actor, true
}

// GetContact — GET /bookings/:id/contact.
func (h *BookingSheetHandler) GetContact(c *gin.Context) {
	actor, ok := h.requireOwnerOrAdmin(c)
	if !ok {
		return
	}

	bookingID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}

	ct, err := h.repo.LoadContact(c.Request.Context(), actor, bookingID)
	if err != nil {
		if errors.Is(err, repository.ErrSheetNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load booking contact"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ct})
}

// PatchContact — PATCH /bookings/:id/contact  body { guest_name?, guest_phone? }.
func (h *BookingSheetHandler) PatchContact(c *gin.Context) {
	actor, ok := h.requireOwnerOrAdmin(c)
	if !ok {
		return
	}

	bookingID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}

	var req contactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "malformed JSON body"})
		return
	}
	if req.GuestName == nil && req.GuestPhone == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no_fields", "message": "لم يتم إرسال أي حقل للتعديل"})
		return
	}

	var namePtr *string
	if req.GuestName != nil {
		trimmed := strings.TrimSpace(*req.GuestName)
		if trimmed == "" || len([]rune(trimmed)) > 120 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_name", "message": "اسم الزبون غير صالح"})
			return
		}
		namePtr = &trimmed
	}

	var phonePtr *string
	if req.GuestPhone != nil {
		normalized, err := phone.Normalize(*req.GuestPhone)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_phone", "message": "رقم الهاتف غير صالح"})
			return
		}
		phonePtr = &normalized
	}

	ct, err := h.repo.ApplyContact(c.Request.Context(), actor, bookingID, namePtr, phonePtr)
	if err != nil {
		if errors.Is(err, repository.ErrSheetNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not update booking contact"})
		return
	}

	// Fail-open CRM re-association: never lets a linkage hiccup fail the edit.
	if phonePtr != nil && h.customers != nil {
		if err := h.customers.AssociateBookingCustomer(c.Request.Context(), bookingID); err != nil {
			c.Error(err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": ct})
}
