package handlers

// WO-24H-CONTINUITY: the extend sheet consumes the MERGED operating-hours gate
// (data.PitchModel.SlotWithinOpenHours) — extending past midnight on a 24/7
// pitch must be accepted, while a pitch whose coverage stops at midnight keeps
// rejecting it. Reuses bsEnv from booking_sheet_db_test.go. SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingSheet24h -v

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// mk247Pitch seeds a pitch with the explicit full-day row 00:00→00:00 on all
// seven weekdays (the production «ملعب ١/٢» shape).
func (e *bsEnv) mk247Pitch(t *testing.T, owner int64) int64 {
	t.Helper()
	id := e.mkPitch(t, owner)
	for wd := range 7 {
		if _, err := e.pool.Exec(context.Background(),
			`INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time) VALUES ($1,$2,'00:00','00:00')`,
			id, wd); err != nil {
			t.Fatalf("seed 24/7 hours: %v", err)
		}
	}
	return id
}

func TestBookingSheet24h_ExtendAcrossMidnight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	pitch247 := e.mk247Pitch(t, e.ownerA)

	// Owner AND admin drive the same merged gate (role pair on the touched path).
	for _, role := range []string{"owner", "admin"} {
		t.Run("accept_24_7_"+role, func(t *testing.T) {
			s, en := e.span(23, 24) // booking ends exactly at local midnight
			id := e.mkBooking(t, pitch247, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
			r := e.router(e.ownerA, role, 0)
			rec := bsDo(r, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
			if rec.Code != 200 {
				t.Fatalf("role %s: extend past midnight on a 24/7 pitch: status=%d body=%s", role, rec.Code, rec.Body.String())
			}
			b := e.readBooking(t, id)
			if !b.end.Equal(en.Add(30 * time.Minute)) {
				t.Fatalf("role %s: end=%v want %v", role, b.end, en.Add(30*time.Minute))
			}
		})
	}

	// Gap pitch (08:00–22:00): coverage stops before midnight; a booking ending
	// at midnight (seeded raw) must NOT be extendable into the closed hours.
	t.Run("reject_gap_pitch", func(t *testing.T) {
		s, en := e.span(23, 24)
		id := e.mkBooking(t, e.pitchHrs, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		r := e.router(e.ownerA, "owner", 0)
		rec := bsDo(r, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("extend past midnight on a gap pitch: status=%d body=%s, want 400", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.end.Equal(en) {
			t.Fatalf("rejected extend must not move the end: %v", b.end)
		}
	})
}
