package repository

// Pure unit tests for the Day View slot-grid logic (buildGrid). No database — these
// exercise the classification rules directly and always run:
//   - 30-minute grid generation across the Amman day
//   - normal-booking overlap ⇒ booked, with `partial` on incomplete coverage
//   - a block row (source='block') occupies ⇒ blocked (keeps its booking object)
//   - inactive pitch ⇒ zero available slots (unoccupied cells render closed)
//   - active pitch, out-of-hours ⇒ closed; in-hours ⇒ available

import (
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

func ammanDay(t *testing.T) (date, fromUTC, toUTC time.Time) {
	t.Helper()
	loc := timeutil.Amman()
	date = time.Date(2026, 6, 28, 12, 0, 0, 0, loc) // no DST in Jordan → exact 24h day
	fromUTC, toUTC = timeutil.AmmanDayBoundsUTC(date)
	return
}

func at(t *testing.T, h, min int) time.Time {
	t.Helper()
	loc := timeutil.Amman()
	return time.Date(2026, 6, 28, h, min, 0, 0, loc)
}

func mkOcc(id int64, source, status string, price float64, start, end time.Time) dayBooking {
	return dayBooking{
		ref: DayViewBookingRef{
			ID: id, Source: source, Status: status,
			StartTime: start, EndTime: end,
		},
		totalPrice: price,
	}
}

// findSlot returns the slot whose Amman start hour:minute matches.
func findSlot(t *testing.T, slots []DayViewSlot, h, min int) DayViewSlot {
	t.Helper()
	target := at(t, h, min).UTC()
	for _, s := range slots {
		if s.Start.UTC().Equal(target) {
			return s
		}
	}
	t.Fatalf("no slot starting %02d:%02d Amman", h, min)
	return DayViewSlot{}
}

func TestDayViewGrid_Generates48SlotsAllAvailable_247(t *testing.T) {
	_, from, to := ammanDay(t)
	slots, summary := buildGrid(from, to, []data.ConcreteInterval{}, false /*hasSchedule*/, true /*active*/, nil)

	if len(slots) != 48 {
		t.Fatalf("expected 48 half-hour slots, got %d", len(slots))
	}
	for _, s := range slots {
		if s.Status != "available" {
			t.Fatalf("24/7 active pitch: slot %s should be available, got %s", s.Start, s.Status)
		}
		if s.Booking != nil || s.Partial {
			t.Fatalf("available slot must carry no booking and partial=false")
		}
	}
	if summary.AvailableSlots != 48 || summary.AvailableHours != 24 {
		t.Fatalf("summary available = %d slots / %v h, want 48 / 24", summary.AvailableSlots, summary.AvailableHours)
	}
	if summary.BookedSlots != 0 {
		t.Fatalf("expected 0 booked, got %d", summary.BookedSlots)
	}
}

func TestDayViewGrid_OverlapMarksWholeCellBooked_NoPartialWhenAligned(t *testing.T) {
	_, from, to := ammanDay(t)
	// Booking 06:00–07:30 → 06:00, 06:30, 07:00 cells booked, all fully covered.
	occ := []dayBooking{mkOcc(1, "manual", "confirmed", 45, at(t, 6, 0), at(t, 7, 30))}
	slots, summary := buildGrid(from, to, []data.ConcreteInterval{}, false, true, occ)

	for _, hm := range [][2]int{{6, 0}, {6, 30}, {7, 0}} {
		s := findSlot(t, slots, hm[0], hm[1])
		if s.Status != "booked" {
			t.Fatalf("%02d:%02d expected booked, got %s", hm[0], hm[1], s.Status)
		}
		if s.Partial {
			t.Fatalf("%02d:%02d should be fully covered (partial=false)", hm[0], hm[1])
		}
		if s.Booking == nil || s.Booking.ID != 1 {
			t.Fatalf("%02d:%02d must attach the occupying booking", hm[0], hm[1])
		}
	}
	// The 07:30 cell is free again.
	if s := findSlot(t, slots, 7, 30); s.Status != "available" {
		t.Fatalf("07:30 should be available again, got %s", s.Status)
	}
	if summary.BookedSlots != 3 || summary.BookedHours != 1.5 {
		t.Fatalf("summary booked = %d / %v h, want 3 / 1.5", summary.BookedSlots, summary.BookedHours)
	}
}

func TestDayViewGrid_PartialTrueWhenCoverageIncomplete(t *testing.T) {
	_, from, to := ammanDay(t)
	// Booking 06:00–06:45 → 06:00 cell fully covered; 06:30 cell only half covered.
	occ := []dayBooking{mkOcc(2, "player", "confirmed", 30, at(t, 6, 0), at(t, 6, 45))}
	slots, _ := buildGrid(from, to, []data.ConcreteInterval{}, false, true, occ)

	if s := findSlot(t, slots, 6, 0); s.Status != "booked" || s.Partial {
		t.Fatalf("06:00 want booked/full, got %s partial=%v", s.Status, s.Partial)
	}
	if s := findSlot(t, slots, 6, 30); s.Status != "booked" || !s.Partial {
		t.Fatalf("06:30 want booked/partial, got %s partial=%v", s.Status, s.Partial)
	}
}

func TestDayViewGrid_BlockRowIsBlockedNotBooked(t *testing.T) {
	_, from, to := ammanDay(t)
	occ := []dayBooking{mkOcc(3, "block", "confirmed", 0, at(t, 9, 0), at(t, 10, 0))}
	slots, summary := buildGrid(from, to, []data.ConcreteInterval{}, false, true, occ)

	s := findSlot(t, slots, 9, 0)
	if s.Status != "blocked" {
		t.Fatalf("a block-row cell must be blocked, got %s", s.Status)
	}
	if s.Booking == nil || s.Booking.Source != "block" {
		t.Fatalf("blocked cell must still attach the block row with source=block")
	}
	// A block is not a "booking": blocked cells do not count toward booked_slots.
	if summary.BookedSlots != 0 {
		t.Fatalf("block cells must not count as booked, got %d", summary.BookedSlots)
	}
}

func TestDayViewGrid_InactivePitch_ZeroAvailable(t *testing.T) {
	_, from, to := ammanDay(t)
	// Inactive pitch with one booking: the booked cells stay booked (history), every
	// unoccupied cell renders closed with no booking object, available_slots == 0.
	occ := []dayBooking{mkOcc(4, "manual", "confirmed", 20, at(t, 8, 0), at(t, 9, 0))}
	slots, summary := buildGrid(from, to, []data.ConcreteInterval{}, false /*hasSchedule*/, false /*active*/, occ)

	if summary.AvailableSlots != 0 || summary.AvailableHours != 0 {
		t.Fatalf("inactive pitch must expose 0 available, got %d slots", summary.AvailableSlots)
	}
	if s := findSlot(t, slots, 8, 0); s.Status != "booked" {
		t.Fatalf("inactive pitch still shows occupancy; 08:00 want booked, got %s", s.Status)
	}
	closed := findSlot(t, slots, 12, 0)
	if closed.Status != "closed" || closed.Booking != nil {
		t.Fatalf("inactive unoccupied cell must be closed with no booking, got %s booking=%v", closed.Status, closed.Booking)
	}
}

func TestDayViewGrid_OutOfHoursClosed_InHoursAvailable(t *testing.T) {
	date, from, to := ammanDay(t)
	_ = date
	// Operating window 09:00–12:00 Amman for this date.
	win := []data.ConcreteInterval{{Start: at(t, 9, 0).UTC(), End: at(t, 12, 0).UTC()}}
	slots, summary := buildGrid(from, to, win, true /*hasSchedule*/, true /*active*/, nil)

	if s := findSlot(t, slots, 9, 0); s.Status != "available" {
		t.Fatalf("09:00 in-hours want available, got %s", s.Status)
	}
	if s := findSlot(t, slots, 11, 30); s.Status != "available" {
		t.Fatalf("11:30 in-hours want available, got %s", s.Status)
	}
	if s := findSlot(t, slots, 12, 0); s.Status != "closed" || s.Booking != nil {
		t.Fatalf("12:00 out-of-hours want closed with no booking, got %s", s.Status)
	}
	if s := findSlot(t, slots, 3, 0); s.Status != "closed" {
		t.Fatalf("03:00 out-of-hours want closed, got %s", s.Status)
	}
	// 09:00–12:00 = 6 half-hour cells available.
	if summary.AvailableSlots != 6 || summary.AvailableHours != 3 {
		t.Fatalf("summary available = %d / %v h, want 6 / 3", summary.AvailableSlots, summary.AvailableHours)
	}
}
