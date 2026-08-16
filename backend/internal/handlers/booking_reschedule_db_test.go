package handlers

// WO-BOOKING-RESCHEDULE — T3: DB-backed end-to-end tests for
// PATCH /bookings/:id/reschedule, driving the REAL gin handlers +
// repositories + SQL against a live database. Gated on
// PITCH_SCOPING_TEST_DATABASE_URL; a skipped run is a failed gate.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingRescheduleDB -v

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/repository"
)

// setRecurrenceGroup stamps recurrence_group_id directly (mkBooking has no
// recurrence param; this WO is the first caller that needs a non-NULL one).
func (e *bsEnv) setRecurrenceGroup(t *testing.T, id int64, groupID string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE bookings SET recurrence_group_id = $1 WHERE id = $2`, groupID, id); err != nil {
		t.Fatalf("setRecurrenceGroup: %v", err)
	}
}

func TestBookingRescheduleDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	ownerA := e.router(e.ownerA, "owner", 0)
	adminR := e.router(0, "admin", 0)
	staffR := e.router(e.staffUser, "staff", e.pitchStaff)
	pitchA2 := e.mkPitch(t, e.ownerA) // second pitch, same owner — the reschedule target for pitch-move tests

	// ── T1: move to a free slot → 200, duration identical, total_price UNCHANGED ──
	t.Run("move_free_slot_ok", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		newS, _ := e.span(14, 15)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if !b.start.Equal(newS) {
			t.Fatalf("start=%v want %v", b.start, newS)
		}
		wantDuration := en.Sub(s)
		if b.end.Sub(b.start) != wantDuration {
			t.Fatalf("duration=%v want %v (must be preserved)", b.end.Sub(b.start), wantDuration)
		}
		if fmt.Sprintf("%.3f", b.total) != "25.000" {
			t.Fatalf("total_price=%.3f want 25.000 (untouched)", b.total)
		}
	})

	// ── T2: move onto an occupied slot → 409, row unchanged ──
	t.Run("move_onto_occupied_409", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		occS, occEn := e.span(14, 15)
		e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, occS, occEn, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": occS})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 slot_conflict (body=%s)", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.start.Equal(s) {
			t.Fatalf("row changed on conflict: start=%v want %v", b.start, s)
		}
	})

	// ── T3: move to a pitch owned by someone else → 404, row unchanged ──
	t.Run("move_to_foreign_pitch_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"pitch_id": e.pitchB})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if !b.start.Equal(s) || !b.end.Equal(en) {
			t.Fatalf("row changed on 404: start=%v end=%v want %v/%v", b.start, b.end, s, en)
		}
	})

	// ── T4: move outside the target pitch's operating hours → 422 ──
	t.Run("move_outside_target_hours_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		// pitchHrs is open 08:00–22:00; 23:00 start is outside close.
		badS, _ := e.span(23, 24)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{
			"start_time": badS, "pitch_id": e.pitchHrs,
		})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 outside_operating_hours (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T5: move a booking that already started → 404 ──
	t.Run("move_already_started_404", func(t *testing.T) {
		start := time.Now().Add(-30 * time.Minute)
		end := time.Now().Add(30 * time.Minute)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, start, end, 25, nil, "unpaid")
		newS, _ := e.span(14, 15)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (in-progress booking cannot be moved) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── T6: recurring booking → 422, row unchanged ──
	t.Run("recurring_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setRecurrenceGroup(t, id, "11111111-1111-1111-1111-111111111111")
		newS, _ := e.span(14, 15)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 recurring_not_supported (body=%s)", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.start.Equal(s) {
			t.Fatalf("row changed on 422: start=%v want %v", b.start, s)
		}
	})

	// ── T7: staff → 403 (route-guard bar, mirroring extend) ──
	t.Run("staff_403", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		newS, _ := e.span(14, 15)
		rec := bsDo(staffR, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T8: cross-tenant → 404, row unchanged ──
	t.Run("cross_tenant_404_unchanged", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "manual", "confirmed", nil, s, en, 25, nil, "unpaid") // owner B's pitch
		newS, _ := e.span(14, 15)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.start.Equal(s) {
			t.Fatalf("row changed on cross-tenant 404: start=%v want %v", b.start, s)
		}
	})

	// ── T9: pitch-only move (no time change) → duration and start unchanged ──
	t.Run("pitch_only_move", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "academy", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"pitch_id": pitchA2})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		sheet := decodeSheet(t, rec)
		if sheet.PitchID != pitchA2 {
			t.Fatalf("pitch_id=%d want %d", sheet.PitchID, pitchA2)
		}
		b := e.readBooking(t, id)
		if !b.start.Equal(s) || !b.end.Equal(en) {
			t.Fatalf("start/end changed on pitch-only move: start=%v end=%v want %v/%v", b.start, b.end, s, en)
		}
	})

	// ── T10: cross-midnight booking moved → range stays contiguous ──
	t.Run("cross_midnight_move_contiguous", func(t *testing.T) {
		e.dayCounter++
		base := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, e.dayCounter)
		s := base.Add(23*time.Hour + 30*time.Minute) // 23:30
		en := s.Add(90 * time.Minute)                // 01:00 next day
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid") // pitchA: no operating_hours rows → fail-open

		e.dayCounter++
		newBase := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, e.dayCounter)
		newS := newBase.Add(23*time.Hour + 45*time.Minute) // also crosses midnight
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if !b.start.Equal(newS) {
			t.Fatalf("start=%v want %v", b.start, newS)
		}
		wantDuration := 90 * time.Minute
		if b.end.Sub(b.start) != wantDuration {
			t.Fatalf("duration=%v want %v (range must stay contiguous across midnight)", b.end.Sub(b.start), wantDuration)
		}
	})

	// ── T11 (T3-tier requirement): concurrency — two parallel moves onto the
	// same destination, only one fits → exactly one 200, one 409 ──
	t.Run("concurrency_only_one_wins", func(t *testing.T) {
		s1, en1 := e.span(10, 11)
		id1 := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s1, en1, 25, nil, "unpaid")
		s2, en2 := e.span(11, 12)
		id2 := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s2, en2, 25, nil, "unpaid")
		dest, _ := e.span(16, 17) // both bookings race to move here (same 1h duration)

		var wg sync.WaitGroup
		codes := make([]int, 2)
		ids := []int64{id1, id2}
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", ids[i]), gin.H{"start_time": dest})
				codes[i] = rec.Code
			}(i)
		}
		wg.Wait()
		ok, conflict := 0, 0
		for _, c := range codes {
			switch c {
			case 200:
				ok++
			case 409:
				conflict++
			}
		}
		if ok != 1 || conflict != 1 {
			t.Fatalf("codes=%v want exactly one 200 and one 409", codes)
		}
	})

	// ── T13: recurring row hits ApplyReschedule DIRECTLY (bypassing the handler's
	// pre-read 422) → ErrSheetNotFound, row unchanged. Proves the WHERE-clause
	// recurrence guard holds even when the application-layer check is skipped —
	// it is a query-layer invariant, not just a handler convenience. ──
	t.Run("apply_reschedule_direct_recurring_guard_holds", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setRecurrenceGroup(t, id, "22222222-2222-2222-2222-222222222222")

		repo := repository.NewBookingSheetRepository(e.pool)
		actor := auth.Actor{UserID: int(e.ownerA), Role: "owner"}
		newS, _ := e.span(14, 15)
		_, err := repo.ApplyReschedule(context.Background(), actor, id, newS, e.pitchA)
		if !errors.Is(err, repository.ErrSheetNotFound) {
			t.Fatalf("err=%v want ErrSheetNotFound (WHERE-clause recurrence guard should reject directly)", err)
		}
		if b := e.readBooking(t, id); !b.start.Equal(s) {
			t.Fatalf("row changed despite guard: start=%v want %v", b.start, s)
		}
	})

	// ── admin bypasses owner scope ──
	t.Run("admin_any_owner_ok", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "academy", "confirmed", nil, s, en, 25, nil, "unpaid")
		newS, _ := e.span(14, 15)
		rec := bsDo(adminR, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{"start_time": newS})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}
