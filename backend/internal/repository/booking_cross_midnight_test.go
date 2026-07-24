package repository

// WO-CROSS-MIDNIGHT Gate 1A integration tests: a player booking that itself
// spans midnight (e.g. 23:00→01:00), the GIST EXCLUDE constraint as the sole
// conflict referee across that boundary, and the operating-window
// containment gate against a cross-midnight window. Reuses ohGateEnv from
// booking_operating_hours_test.go. SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run CrossMidnight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// crossMidnightWindow seeds a single weekly window on date's weekday that
// spans 22:00→02:00 (crosses midnight into date+1).
func (e *ohGateEnv) crossMidnightWindow(t *testing.T, date time.Time) {
	t.Helper()
	wd := int(date.Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: wd, OpenTime: "22:00", CloseTime: "02:00"}})
}

// at builds an absolute instant at the given Amman hour:minute on `date`
// (only Y/M/D of date is read).
func xmAt(date time.Time, hour, min int) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, hour, min, 0, 0, timeutil.Amman())
}

func TestCrossMidnight_BookingSpanningMidnightSucceeds(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	start := xmAt(date, 23, 0)
	end := xmAt(date.AddDate(0, 0, 1), 1, 0)
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("23:00->01:00 cross-midnight booking should succeed, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
	if !b.EndTime.After(b.StartTime) || b.EndTime.Sub(b.StartTime) != 2*time.Hour {
		t.Fatalf("persisted range = [%s,%s), want a 2h span", b.StartTime, b.EndTime)
	}
}

func TestCrossMidnight_OverlapAcrossMidnightRejectedByConstraint(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	// Existing booking: 00:30->01:30 on date+1 (inside the window's tail).
	existingStart := xmAt(date.AddDate(0, 0, 1), 0, 30)
	existingEnd := xmAt(date.AddDate(0, 0, 1), 1, 30)
	if _, err := e.book(existingStart, existingEnd, false); err != nil {
		t.Fatalf("seed existing 00:30-01:30 booking: %v", err)
	}

	// New booking: 23:30(date)->01:00(date+1) overlaps the existing one.
	newStart := xmAt(date, 23, 30)
	newEnd := xmAt(date.AddDate(0, 0, 1), 1, 0)
	_, err := e.book(newStart, newEnd, false)
	if !errors.Is(err, ErrDoubleBooking) {
		t.Fatalf("overlapping cross-midnight booking: err = %v, want ErrDoubleBooking (constraint-enforced)", err)
	}
}

func TestCrossMidnight_EndsExactlyAtCloseAccepted(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	start := xmAt(date.AddDate(0, 0, 1), 1, 0)
	end := xmAt(date.AddDate(0, 0, 1), 2, 0) // exactly the 02:00 close
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("booking ending exactly at close should succeed, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}

func TestCrossMidnight_OneMinutePastCloseRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	start := xmAt(date.AddDate(0, 0, 1), 1, 0)
	end := xmAt(date.AddDate(0, 0, 1), 2, 1) // one minute past the 02:00 close
	_, err := e.book(start, end, false)
	if !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("booking one minute past close: err = %v, want ErrSlotOutsideOperatingHours", err)
	}
}

func TestCrossMidnight_StartsBeforeOpenRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	start := xmAt(date, 21, 0) // before the 22:00 open
	end := xmAt(date, 22, 30)
	_, err := e.book(start, end, false)
	if !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("booking starting before open: err = %v, want ErrSlotOutsideOperatingHours", err)
	}
}

func TestCrossMidnight_EndsAfterCloseRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.crossMidnightWindow(t, date)

	start := xmAt(date.AddDate(0, 0, 1), 1, 30)
	end := xmAt(date.AddDate(0, 0, 1), 2, 30) // runs past the 02:00 close
	_, err := e.book(start, end, false)
	if !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("booking ending after close: err = %v, want ErrSlotOutsideOperatingHours", err)
	}
}

// TestCrossMidnight_SchemaBaselineUnchanged proves Gate 1A made no schema
// edits: the scratch DB must still match the CURRENT main schema.sql.
func TestCrossMidnight_SchemaBaselineUnchanged(t *testing.T) {
	e := newOHGateEnv(t)
	testutil.AssertSchemaBaseline(t, e.pool)
}

// ─────────────────────────────────────────────────────────────────────────────
// Role-differentiated lead-time / max-duration (Gate 1A follow-up)
//
// CreateBooking (player, backend/internal/handlers/bookings.go:126-160) gates
// on a 15-minute lead time and a 120-minute max duration. CreateManualBooking
// (owner/admin walk-in log, handlers/bookings.go:432-472) and CreateBlock
// (owner/admin hold, handlers/bookings.go:363-419) carry NEITHER check —
// only "must not be entirely in the past". This is intentional, not a miss:
// an owner logging a walk-in needs to record occupancy starting right now,
// and a maintenance/tournament block can legitimately run any length. Both
// paths already bypass the operating-hours gate too (CreateBlock always;
// CreateManualBooking via the pre-existing force_bypass_hours soft
// override) — the lead-time/max-duration omission follows the same
// established owner/admin-authority pattern, not a new exemption invented
// here. This test proves the SAME start+duration the player-side handler
// test (TestCreateBooking_PlayerCannotBookNow2MinFor150Min) rejects at
// start+2min/150min is accepted end-to-end through the real repository for
// an owner-initiated manual booking.
// ─────────────────────────────────────────────────────────────────────────────

func TestManualBooking_OwnerBypassesLeadTimeAndDuration(t *testing.T) {
	e := newOHGateEnv(t)
	// No schedule configured → fail-open 24/7, isolating this test to the
	// lead-time/max-duration question (not a containment interaction).
	start := time.Now().UTC().Add(2 * time.Minute)
	end := start.Add(150 * time.Minute) // > the player path's 120-minute cap

	bookings, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID:     e.pitchID,
		Actor:       auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner},
		StartTime:   start,
		EndTime:     end,
		GuestName:   "Walk-in Guest",
		RepeatWeeks: 1,
	})
	if err != nil {
		t.Fatalf("owner manual booking at start+2min/150min should succeed (no lead-time/max-duration gate on this path), got %v", err)
	}
	if replayed {
		t.Fatalf("expected a fresh insert, got replayed=true")
	}
	if len(bookings) != 1 {
		t.Fatalf("len(bookings) = %d, want 1", len(bookings))
	}
	if got := bookings[0].EndTime.Sub(bookings[0].StartTime); got != 150*time.Minute {
		t.Fatalf("persisted duration = %s, want 150m", got)
	}
}
