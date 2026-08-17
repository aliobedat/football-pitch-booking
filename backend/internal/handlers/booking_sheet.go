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
	"github.com/ali/football-pitch-api/internal/timeutil"
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

// ── Contact (WO-BOOKING-EDIT-CONTACT, WO-CONTACT-SERIES) ─────────────────────
//
// Owner/admin correct the guest name/phone on an existing manual/academy
// booking. Scope, source, and status are enforced entirely in the repository's
// WHERE clause (single atomic statement, no pre-check SELECT); pgx.ErrNoRows
// deliberately conflates nonexistent / cross-tenant / source='player' /
// source='block' / cancelled into one 404 (existence not leaked). No
// notification, no status_transitions row — this is not a status change.
//
// apply_to_series (WO-CONTACT-SERIES) extends the same edit to every active
// manual/academy member of the booking's recurrence_group_id, still name/phone
// only — booking_range and pitch_id remain untouched; series rescheduling is a
// separate, out-of-scope WO.

type contactRequest struct {
	GuestName     *string `json:"guest_name"`
	GuestPhone    *string `json:"guest_phone"`
	ApplyToSeries bool    `json:"apply_to_series"` // WO-CONTACT-SERIES: default false = single booking, unchanged
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

	// WO-CONTACT-SERIES: apply_to_series branches to the group-wide write. The
	// distinction between "not found" (404) and "not recurring" (422) can only
	// be made from a pre-read (the atomic UPDATE's WHERE can't tell "no such
	// booking" apart from "booking has no group" — both yield zero rows), so
	// this reuses the existing LoadContact pre-read rather than adding a
	// second one.
	if req.ApplyToSeries {
		target, err := h.repo.LoadContact(c.Request.Context(), actor, bookingID)
		if err != nil {
			if errors.Is(err, repository.ErrSheetNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
				return
			}
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load booking contact"})
			return
		}
		if target.RecurrenceGroupID == nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "not_recurring", "message": "هذا الحجز ليس جزءًا من سلسلة متكررة"})
			return
		}

		ids, err := h.repo.ApplySeriesContact(c.Request.Context(), actor, bookingID, namePtr, phonePtr)
		if err != nil {
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not update the series"})
			return
		}
		if len(ids) == 0 {
			// Guards matched no row — a state raced between the pre-read and the
			// atomic UPDATE. Fail closed as not-found, same as the single-booking path.
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}

		// Fail-open CRM re-association per updated row: never lets a linkage
		// hiccup fail the edit.
		if phonePtr != nil && h.customers != nil {
			for _, id := range ids {
				if err := h.customers.AssociateBookingCustomer(c.Request.Context(), id); err != nil {
					c.Error(err)
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{"updated_count": len(ids), "updated_ids": ids})
		return
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

// ── Reschedule (WO-BOOKING-RESCHEDULE) ───────────────────────────────────────
//
// Owner/admin move an existing manual/academy/player booking to a new start
// time and/or a new pitch owned by the same owner, SAME DURATION ONLY (a
// duration change stays with /extend). The atomic UPDATE is the sole conflict
// referee (GIST EXCLUDE, 23P01 → 409 slot_conflict); recurrence and operating
// hours are checked from a pre-read snapshot, mirroring ExtendBooking's
// pre-check/apply split — NOT an availability pre-check (that stays banned).
// total_price / amount_paid are never recomputed, even across a price_per_hour
// difference between pitches.

type rescheduleRequest struct {
	StartTime     *time.Time `json:"start_time"`
	PitchID       *int64     `json:"pitch_id"`
	ApplyToSeries bool       `json:"apply_to_series"` // WO-SERIES-RESCHEDULE
	TimeOfDay     *string    `json:"time_of_day"`     // WO-SERIES-RESCHEDULE: "HH:MM", series mode only
}

// RescheduleBooking — PATCH /bookings/:id/reschedule  body { start_time?, pitch_id? }.
func (h *BookingSheetHandler) RescheduleBooking(c *gin.Context) {
	actor, ok := h.requireOwnerOrAdmin(c)
	if !ok {
		return
	}

	bookingID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || bookingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_booking_id", "message": "invalid booking id"})
		return
	}

	var req rescheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "malformed JSON body"})
		return
	}

	if req.ApplyToSeries {
		h.rescheduleSeries(c, actor, bookingID, req)
		return
	}

	if req.StartTime == nil && req.PitchID == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no_fields", "message": "لم يتم إرسال أي حقل للتعديل"})
		return
	}

	target, err := h.repo.LoadRescheduleTarget(c.Request.Context(), actor, bookingID)
	if err != nil {
		if errors.Is(err, repository.ErrSheetNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load the booking"})
		return
	}
	if target.RecurrenceGroupID != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "recurring_not_supported", "message": "لا يمكن نقل حجز متكرر — عدّل هذا الموعد يدويًا"})
		return
	}

	newPitchID := target.PitchID
	if req.PitchID != nil {
		newPitchID = *req.PitchID
	}
	newStart := target.Start
	if req.StartTime != nil {
		newStart = *req.StartTime
	}
	newEnd := newStart.Add(target.End.Sub(target.Start))

	// The target pitch must resolve inside the caller's own scope BEFORE it is
	// handed to the hours resolver — otherwise an attacker-supplied pitch_id for
	// a pitch they don't own becomes an hours-configuration oracle. Not the
	// booking's own pitch scope (already proven by LoadRescheduleTarget) when
	// pitch_id is unset, but cheap to re-check unconditionally for uniformity.
	if err := h.repo.ResolveOwnedPitch(c.Request.Context(), actor, newPitchID); err != nil {
		if errors.Is(err, repository.ErrSheetNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve target pitch"})
		return
	}

	// Operating-hours gate on the candidate range, against the TARGET pitch —
	// reuses the same resolver ExtendBooking uses (fail-open when unconfigured).
	contained, _, err := h.hours.SlotWithinOpenHours(c.Request.Context(), int(newPitchID), newStart, newEnd)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve operating hours"})
		return
	}
	if !contained {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "outside_operating_hours", "message": "الموعد الجديد خارج ساعات عمل الملعب"})
		return
	}

	// Single atomic UPDATE. EXCLUDE (23P01) is the sole conflict referee → 409.
	sheet, err := h.repo.ApplyReschedule(c.Request.Context(), actor, bookingID, newStart, newPitchID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrSheetConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "slot_conflict", "message": "الموعد الجديد يتعارض مع حجز آخر"})
		case errors.Is(err, repository.ErrSheetNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not reschedule the booking"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sheet})
}

// ── Series reschedule (WO-SERIES-RESCHEDULE) ─────────────────────────────────
//
// apply_to_series shifts TIME-OF-DAY and/or PITCH uniformly across every active
// future occurrence of the group; each occurrence KEEPS ITS OWN DATE. This is a
// documented deviation from "single atomic UPDATE": best-effort partial success
// requires a per-row loop. What is NOT relaxed — zero pre-check SELECT before
// any individual row's UPDATE attempt; GIST EXCLUDE referees each row
// independently (see CLAUDE.md, "Series reschedule — documented deviation").

// parseTimeOfDay validates "HH:MM" and returns hour/minute. time.Parse with
// layout "15:04" already rejects an out-of-range hour/minute.
func parseTimeOfDay(raw string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", raw)
	if err != nil {
		return 0, 0, err
	}
	return t.Hour(), t.Minute(), nil
}

func rescheduleRowDate(t time.Time) string { return timeutil.InAmman(t).Format("2006-01-02") }
func rescheduleRowTime(t time.Time) string { return timeutil.InAmman(t).Format("15:04") }

// rescheduleSeries handles apply_to_series=true — a distinct code path from the
// single-booking reschedule above, kept separate rather than interleaved so
// neither reader has to hold both response shapes in mind at once.
func (h *BookingSheetHandler) rescheduleSeries(c *gin.Context, actor auth.Actor, bookingID int64, req rescheduleRequest) {
	if req.StartTime != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "date_not_allowed_in_series_mode", "message": "لا يمكن تحديد تاريخ عند التطبيق على السلسلة — كل موعد يحتفظ بتاريخه",
		})
		return
	}
	if req.TimeOfDay == nil && req.PitchID == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no_fields", "message": "لم يتم إرسال أي حقل للتعديل"})
		return
	}

	var hour, minute int
	haveTimeOfDay := req.TimeOfDay != nil
	if haveTimeOfDay {
		h_, m_, err := parseTimeOfDay(*req.TimeOfDay)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_time_of_day", "message": "صيغة الوقت غير صالحة (HH:MM)"})
			return
		}
		hour, minute = h_, m_
	}

	ctx := c.Request.Context()

	group, err := h.repo.LoadSeriesRescheduleGroup(ctx, actor, bookingID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrSheetNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
		case errors.Is(err, repository.ErrNotRecurring):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "not_recurring", "message": "هذا الحجز ليس جزءًا من سلسلة متكررة"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load the series"})
		}
		return
	}

	// Target pitch resolved ONCE (not per-row) — it's the same explicit target
	// for the whole series, or each row keeps its own. Resolving under scope
	// here (not just trusting the request) prevents an attacker-supplied
	// pitch_id from becoming a cross-tenant hours-configuration oracle, same
	// reasoning as the single-booking path.
	var explicitPitch *int64
	if req.PitchID != nil {
		if err := h.repo.ResolveOwnedPitch(ctx, actor, *req.PitchID); err != nil {
			if errors.Is(err, repository.ErrSheetNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الحجز غير موجود أو لا تملك صلاحية تعديله"})
				return
			}
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve target pitch"})
			return
		}
		explicitPitch = req.PitchID
	}

	now := time.Now()
	moved := []gin.H{}
	conflicts := []gin.H{}
	skipped := []gin.H{}

	for _, row := range group {
		if row.Start.Before(now) {
			skipped = append(skipped, gin.H{"id": row.ID, "date": rescheduleRowDate(row.Start), "reason": "already_started"})
			continue
		}

		targetPitch := row.PitchID
		if explicitPitch != nil {
			targetPitch = *explicitPitch
		}
		newStart := row.Start
		if haveTimeOfDay {
			ammanDate := timeutil.InAmman(row.Start)
			newStart = time.Date(ammanDate.Year(), ammanDate.Month(), ammanDate.Day(), hour, minute, 0, 0, timeutil.Amman())
		}
		newEnd := newStart.Add(row.End.Sub(row.Start))

		// Hours check is a business-rule call against the resolver, not a raw
		// SQL pre-check SELECT on bookings — same distinction the single-row
		// path relies on. A resolver ERROR is infrastructural, not a
		// business-logic conflict, so it aborts the whole operation.
		contained, _, err := h.hours.SlotWithinOpenHours(ctx, int(targetPitch), newStart, newEnd)
		if err != nil {
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not resolve operating hours"})
			return
		}
		if !contained {
			conflicts = append(conflicts, gin.H{"id": row.ID, "date": rescheduleRowDate(row.Start), "reason": "outside_hours"})
			continue
		}

		// ONE atomic UPDATE per row. No pre-check SELECT. GIST EXCLUDE is the
		// sole conflict referee for this row.
		if err := h.repo.ApplySeriesRescheduleRow(ctx, actor, row.ID, newStart, targetPitch); err != nil {
			if errors.Is(err, repository.ErrSheetConflict) || errors.Is(err, repository.ErrSheetNotFound) {
				conflicts = append(conflicts, gin.H{"id": row.ID, "date": rescheduleRowDate(row.Start), "reason": "slot_conflict"})
				continue
			}
			// A real bug, not a business-logic conflict — abort the whole
			// operation. Rows already moved in earlier loop iterations stay
			// moved (each was its own committed statement); this response
			// carries no partial report, matching the WO's "return 500" call.
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not reschedule the series"})
			return
		}

		moved = append(moved, gin.H{
			"id": row.ID, "date": rescheduleRowDate(row.Start),
			"old_time": rescheduleRowTime(row.Start), "new_time": rescheduleRowTime(newStart),
		})
	}

	status := http.StatusOK
	if len(moved) == 0 && len(conflicts) > 0 {
		// Nothing happened at all — every attempted row failed.
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"moved": moved, "conflicts": conflicts, "skipped": skipped})
}
