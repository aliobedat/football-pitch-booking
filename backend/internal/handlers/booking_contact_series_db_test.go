package handlers

// WO-CONTACT-SERIES — T2: DB-backed end-to-end tests for the apply_to_series
// extension of PATCH /bookings/:id/contact, driving the REAL gin handlers +
// repositories + SQL against a live database. Gated on
// PITCH_SCOPING_TEST_DATABASE_URL; a skipped run is a failed gate.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingContactSeriesDB -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// decodeTop decodes a response body with no "data" wrapper (the series
// success shape: {"updated_count":N,"updated_ids":[...]}), unlike
// decodeContact which unwraps "data" for the single-row shape.
func decodeTop(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return resp
}

func TestBookingContactSeriesDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	ownerA := e.router(e.ownerA, "owner", 0)
	staffR := e.router(e.staffUser, "staff", e.pitchStaff)

	// mkSeries seeds N manual bookings sharing one recurrence_group_id on the
	// given pitch, distinct non-overlapping spans. Returns their ids.
	mkSeries := func(pitch int64, n int, groupID string) []int64 {
		ids := make([]int64, 0, n)
		for i := 0; i < n; i++ {
			s, en := e.span(10, 11)
			id := e.mkBooking(t, pitch, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
			e.setRecurrenceGroup(t, id, groupID)
			ids = append(ids, id)
		}
		return ids
	}

	// ── T1: series update → every row in the group changed, rows outside untouched ──
	t.Run("series_update_group_only", func(t *testing.T) {
		group := "aaaaaaaa-0001-0001-0001-000000000001"
		ids := mkSeries(e.pitchA, 3, group)

		// A row NOT in the group (own group, different id) must be untouched.
		s, en := e.span(10, 11)
		outsideID := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		e.setRecurrenceGroup(t, outsideID, "bbbbbbbb-0002-0002-0002-000000000002")

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", ids[0]), gin.H{
			"guest_name": "منى", "guest_phone": "0781111111", "apply_to_series": true,
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}

		for _, id := range ids {
			st := e.readContact(t, id)
			if st.guestName == nil || *st.guestName != "منى" {
				t.Fatalf("id=%d guest_name=%v want منى", id, st.guestName)
			}
			if st.guestPhone == nil || *st.guestPhone != "+962781111111" {
				t.Fatalf("id=%d guest_phone=%v want +962781111111", id, st.guestPhone)
			}
		}
		outside := e.readContact(t, outsideID)
		if outside.guestName == nil || *outside.guestName == "منى" {
			t.Fatalf("booking outside the group was touched: guest_name=%v", outside.guestName)
		}
	})

	// ── T2: apply_to_series on a non-recurring booking → 422 ──
	t.Run("non_recurring_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "manual", "confirmed", nil, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", id), gin.H{
			"guest_name": "x", "apply_to_series": true,
		})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 not_recurring (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T3: cross-tenant series → 404, zero rows changed ──
	t.Run("cross_tenant_404_unchanged", func(t *testing.T) {
		group := "cccccccc-0003-0003-0003-000000000003"
		ids := mkSeries(e.pitchB, 2, group) // owner B's pitch
		before := make([]string, len(ids))
		for i, id := range ids {
			st := e.readContact(t, id)
			before[i] = *st.guestName
		}

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", ids[0]), gin.H{
			"guest_name": "hacked", "apply_to_series": true,
		})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		for i, id := range ids {
			st := e.readContact(t, id)
			if *st.guestName != before[i] {
				t.Fatalf("id=%d guest_name changed on cross-tenant 404: %v want %v", id, *st.guestName, before[i])
			}
		}
	})

	// ── T4: staff → 403 ──
	t.Run("staff_403", func(t *testing.T) {
		group := "dddddddd-0004-0004-0004-000000000004"
		ids := mkSeries(e.pitchStaff, 2, group)
		rec := bsDo(staffR, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", ids[0]), gin.H{
			"guest_name": "x", "apply_to_series": true,
		})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── T5: cancelled members are skipped, active ones updated ──
	t.Run("cancelled_members_skipped", func(t *testing.T) {
		group := "eeeeeeee-0005-0005-0005-000000000005"
		ids := mkSeries(e.pitchA, 3, group)
		// Cancel one member directly.
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE bookings SET status = 'cancelled' WHERE id = $1`, ids[1]); err != nil {
			t.Fatalf("cancel member: %v", err)
		}
		beforeCancelled := e.readContact(t, ids[1])

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", ids[0]), gin.H{
			"guest_name": "سامي", "apply_to_series": true,
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeTop(t, rec.Body.Bytes())
		if data["updated_count"] != float64(2) {
			t.Fatalf("updated_count=%v want 2 (cancelled member excluded)", data["updated_count"])
		}

		// Active members updated.
		for _, id := range []int64{ids[0], ids[2]} {
			st := e.readContact(t, id)
			if st.guestName == nil || *st.guestName != "سامي" {
				t.Fatalf("id=%d guest_name=%v want سامي", id, st.guestName)
			}
		}
		// Cancelled member untouched.
		afterCancelled := e.readContact(t, ids[1])
		if *afterCancelled.guestName != *beforeCancelled.guestName {
			t.Fatalf("cancelled member changed: before=%v after=%v", *beforeCancelled.guestName, *afterCancelled.guestName)
		}
	})

	// ── T6: apply_to_series omitted → single-row behaviour unchanged (regression guard) ──
	t.Run("omitted_single_row_unchanged", func(t *testing.T) {
		group := "ffffffff-0006-0006-0006-000000000006"
		ids := mkSeries(e.pitchA, 2, group)

		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/contact", ids[0]), gin.H{
			"guest_name": "فقط-هذا",
		})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		data := decodeContact(t, rec.Body.Bytes())
		if _, hasUpdatedCount := data["updated_count"]; hasUpdatedCount {
			t.Fatalf("single-row response unexpectedly carries updated_count: %v", data)
		}
		if data["guest_name"] != "فقط-هذا" {
			t.Fatalf("guest_name=%v want single-row response shape {data:{guest_name,...}}", data["guest_name"])
		}

		st0 := e.readContact(t, ids[0])
		if st0.guestName == nil || *st0.guestName != "فقط-هذا" {
			t.Fatalf("target row not updated: %v", st0.guestName)
		}
		st1 := e.readContact(t, ids[1])
		if st1.guestName == nil || *st1.guestName == "فقط-هذا" {
			t.Fatalf("sibling in the same group was updated despite apply_to_series being omitted: %v", st1.guestName)
		}
	})
}
