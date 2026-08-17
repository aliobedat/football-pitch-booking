package handlers

// WO-SERIES-RESCHEDULE — T3: DB-backed end-to-end tests for the apply_to_series
// extension of PATCH /bookings/:id/reschedule, driving the REAL gin handlers +
// repositories + SQL against a live database. Gated on
// PITCH_SCOPING_TEST_DATABASE_URL; a skipped run is a failed gate.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingSeriesRescheduleDB -v

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/timeutil"
)

// mkPitchPriced mirrors bsEnv.mkPitch but with a caller-chosen price_per_hour
// (this WO is the first caller that needs a non-25 price for a cross-pitch move).
func (e *bsEnv) mkPitchPriced(t *testing.T, owner int64, price int) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO pitches (owner_id, name, price_per_hour, neighborhood, surface, format, venue_id)
		 VALUES ($1,$2,$3,'Amman','artificial_grass','خماسي',$4) RETURNING id`,
		owner, "P-priced", price, e.mkVenue(t, owner, "P-priced")).Scan(&id); err != nil {
		t.Fatalf("mkPitchPriced: %v", err)
	}
	return id
}

// atAmmanTime rebuilds t's Amman civil date at hour:minute Amman time — the
// exact computation the handler performs for a series row, used here to
// predict/seed collisions deterministically rather than guessing.
func atAmmanTime(t time.Time, hour, minute int) time.Time {
	a := timeutil.InAmman(t)
	return time.Date(a.Year(), a.Month(), a.Day(), hour, minute, 0, 0, timeutil.Amman())
}

func TestBookingSeriesRescheduleDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	ownerA := e.router(e.ownerA, "owner", 0)
	staffR := e.router(e.staffUser, "staff", e.pitchStaff)

	// mkSeries seeds n manual bookings sharing one recurrence_group_id on the
	// given pitch, each on a distinct (unique, future) day via e.span, all
	// [10,11) local-offset spans. Returns their start/end pairs alongside ids.
	type seeded struct {
		id         int64
		start, end time.Time
	}
	mkSeries := func(pitch int64, n int, groupID string) []seeded {
		out := make([]seeded, 0, n)
		for i := 0; i < n; i++ {
			s, en := e.span(10, 11)
			id := e.mkBooking(t, pitch, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
			e.setRecurrenceGroup(t, id, groupID)
			out = append(out, seeded{id, s, en})
		}
		return out
	}

	// ── T1: full series moves cleanly → all "moved", empty conflicts/skipped ──
	t.Run("full_series_moves_cleanly", func(t *testing.T) {
		group := "10000000-0001-0001-0001-000000000001"
		rows := mkSeries(e.pitchA, 3, group)

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		moved, _ := data["moved"].([]any)
		conflicts, _ := data["conflicts"].([]any)
		skipped, _ := data["skipped"].([]any)
		if len(moved) != 3 {
			t.Fatalf("moved=%d want 3 (body=%s)", len(moved), rec.Body.String())
		}
		if len(conflicts) != 0 || len(skipped) != 0 {
			t.Fatalf("conflicts=%d skipped=%d want 0/0 (body=%s)", len(conflicts), len(skipped), rec.Body.String())
		}
		for _, r := range rows {
			wantStart := atAmmanTime(r.start, 16, 0)
			b := e.readBooking(t, r.id)
			if !b.start.Equal(wantStart) {
				t.Fatalf("id=%d start=%v want %v (date preserved, time moved)", r.id, b.start, wantStart)
			}
		}
	})

	// ── T2: one occurrence's target slot occupied → that one in conflicts, rest moved ──
	t.Run("one_occupied_rest_moved", func(t *testing.T) {
		group := "10000000-0002-0002-0002-000000000002"
		rows := mkSeries(e.pitchA, 3, group)

		// Pre-occupy row[1]'s TARGET slot (16:00 on its own date) with another booking.
		blockStart := atAmmanTime(rows[1].start, 16, 0)
		e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, blockStart, blockStart.Add(time.Hour), 25, nil, "unpaid")

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (partial success, body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		moved, _ := data["moved"].([]any)
		conflicts, _ := data["conflicts"].([]any)
		if len(moved) != 2 {
			t.Fatalf("moved=%d want 2 (body=%s)", len(moved), rec.Body.String())
		}
		if len(conflicts) != 1 {
			t.Fatalf("conflicts=%d want 1 (body=%s)", len(conflicts), rec.Body.String())
		}
		c0 := conflicts[0].(map[string]any)
		if int64(c0["id"].(float64)) != rows[1].id {
			t.Fatalf("conflict id=%v want %d", c0["id"], rows[1].id)
		}
		if c0["reason"] != "slot_conflict" {
			t.Fatalf("conflict reason=%v want slot_conflict", c0["reason"])
		}
		// The conflicting row is untouched (original time).
		b1 := e.readBooking(t, rows[1].id)
		if !b1.start.Equal(rows[1].start) {
			t.Fatalf("conflicting row moved anyway: start=%v want %v", b1.start, rows[1].start)
		}
	})

	// ── T3: an already-started occurrence → skipped, untouched, not a failure ──
	t.Run("already_started_skipped", func(t *testing.T) {
		group := "10000000-0003-0003-0003-000000000003"
		rows := mkSeries(e.pitchA, 2, group)
		pastStart := time.Now().Add(-30 * time.Minute)
		pastEnd := time.Now().Add(30 * time.Minute)
		pastID := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, pastStart, pastEnd, 25, nil, "unpaid")
		e.setRecurrenceGroup(t, pastID, group)

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		moved, _ := data["moved"].([]any)
		skipped, _ := data["skipped"].([]any)
		if len(moved) != 2 {
			t.Fatalf("moved=%d want 2 (future rows) body=%s", len(moved), rec.Body.String())
		}
		if len(skipped) != 1 {
			t.Fatalf("skipped=%d want 1 body=%s", len(skipped), rec.Body.String())
		}
		s0 := skipped[0].(map[string]any)
		if int64(s0["id"].(float64)) != pastID || s0["reason"] != "already_started" {
			t.Fatalf("skipped entry=%v want id=%d reason=already_started", s0, pastID)
		}
		bp := e.readBooking(t, pastID)
		if diff := bp.start.Sub(pastStart); diff < -time.Millisecond || diff > time.Millisecond {
			// Postgres timestamptz is microsecond-precision; pastStart carries
			// Go's full nanosecond clock reading, so compare with a tolerance
			// rather than Equal (a true move would differ by ~30min, not µs).
			t.Fatalf("already-started row moved: start=%v want ~%v", bp.start, pastStart)
		}
	})

	// ── T4: pitch change applied to series → pitch_id updated, dates/times untouched ──
	t.Run("pitch_only_change_applied_to_series", func(t *testing.T) {
		group := "10000000-0004-0004-0004-000000000004"
		rows := mkSeries(e.pitchA, 3, group)
		pitchA2 := e.mkPitch(t, e.ownerA)

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "pitch_id": pitchA2,
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		moved, _ := data["moved"].([]any)
		if len(moved) != 3 {
			t.Fatalf("moved=%d want 3 (body=%s)", len(moved), rec.Body.String())
		}
		for _, r := range rows {
			var pitchID int64
			var start, end time.Time
			if err := e.pool.QueryRow(context.Background(),
				`SELECT pitch_id, lower(booking_range), upper(booking_range) FROM bookings WHERE id=$1`, r.id).
				Scan(&pitchID, &start, &end); err != nil {
				t.Fatalf("read: %v", err)
			}
			if pitchID != pitchA2 {
				t.Fatalf("id=%d pitch_id=%d want %d", r.id, pitchID, pitchA2)
			}
			if !start.Equal(r.start) || !end.Equal(r.end) {
				t.Fatalf("id=%d start/end changed: %v/%v want %v/%v", r.id, start, end, r.start, r.end)
			}
		}
	})

	// ── T5: apply_to_series=true with start_time present → 422 ──
	t.Run("date_not_allowed_in_series_mode", func(t *testing.T) {
		group := "10000000-0005-0005-0005-000000000005"
		rows := mkSeries(e.pitchA, 1, group)
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "start_time": time.Now().Add(48 * time.Hour), "time_of_day": "16:00",
		})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 date_not_allowed_in_series_mode (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T6: non-recurring booking → 422 not_recurring ──
	t.Run("non_recurring_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 not_recurring (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T7: cross-tenant → 404, zero rows touched ──
	t.Run("cross_tenant_404_unchanged", func(t *testing.T) {
		group := "10000000-0007-0007-0007-000000000007"
		rows := mkSeries(e.pitchB, 2, group) // owner B's pitch
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		for _, r := range rows {
			b := e.readBooking(t, r.id)
			if !b.start.Equal(r.start) {
				t.Fatalf("id=%d row changed on cross-tenant 404: start=%v want %v", r.id, b.start, r.start)
			}
		}
	})

	// ── T8: staff → 403 ──
	t.Run("staff_403", func(t *testing.T) {
		group := "10000000-0008-0008-0008-000000000008"
		rows := mkSeries(e.pitchStaff, 2, group)
		rec := bsDo(staffR, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T9: every row conflicts → 409, moved empty, all listed in conflicts ──
	t.Run("all_conflict_409", func(t *testing.T) {
		group := "10000000-0009-0009-0009-000000000009"
		rows := mkSeries(e.pitchA, 2, group)
		for _, r := range rows {
			blockStart := atAmmanTime(r.start, 16, 0)
			e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, blockStart, blockStart.Add(time.Hour), 25, nil, "unpaid")
		}
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "time_of_day": "16:00",
		})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 (nothing happened) body=%s", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		moved, _ := data["moved"].([]any)
		conflicts, _ := data["conflicts"].([]any)
		if len(moved) != 0 {
			t.Fatalf("moved=%d want 0 (body=%s)", len(moved), rec.Body.String())
		}
		if len(conflicts) != 2 {
			t.Fatalf("conflicts=%d want 2 (body=%s)", len(conflicts), rec.Body.String())
		}
	})

	// ── T10: total_price unchanged on every moved row, incl. a pitch price change ──
	t.Run("total_price_unchanged_across_pitch_change", func(t *testing.T) {
		group := "10000000-0010-0010-0010-000000000010"
		rows := mkSeries(e.pitchA, 2, group) // seeded at total_price=25 (mkBooking's fixed arg)
		pricier := e.mkPitchPriced(t, e.ownerA, 999)

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/reschedule", rows[0].id), gin.H{
			"apply_to_series": true, "pitch_id": pricier,
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		for _, r := range rows {
			b := e.readBooking(t, r.id)
			if fmt.Sprintf("%.3f", b.total) != "25.000" {
				t.Fatalf("id=%d total_price=%.3f want 25.000 (must not be recomputed from the new pitch's price)", r.id, b.total)
			}
		}
	})
}
