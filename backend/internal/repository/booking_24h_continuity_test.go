package repository

// WO-24H-CONTINUITY Gate 1A integration tests: the merged operating-hours gate
// (data.SlotWithinHours) on the real write paths, plus the D-B regression
// proof that every READ path (GetOpenWindows, SearchAvailability, day view,
// calendar) still serves DAY-shaped windows — never merged multi-day runs.
// Reuses ohGateEnv from booking_operating_hours_test.go. SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Continuity24h -v

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

// fullWeekSchedule seeds the same open/close on all seven weekdays.
func (e *ohGateEnv) fullWeekSchedule(t *testing.T, open, close string) {
	t.Helper()
	w := make([]data.OperatingWindow, 0, 7)
	for wd := range 7 {
		w = append(w, data.OperatingWindow{Weekday: wd, OpenTime: open, CloseTime: close})
	}
	e.setSchedule(t, w)
}

// weekScheduleExcept seeds the row on every weekday except `skip`.
func (e *ohGateEnv) weekScheduleExcept(t *testing.T, open, close string, skip int) {
	t.Helper()
	w := make([]data.OperatingWindow, 0, 6)
	for wd := range 7 {
		if wd != skip {
			w = append(w, data.OperatingWindow{Weekday: wd, OpenTime: open, CloseTime: close})
		}
	}
	e.setSchedule(t, w)
}

// ── The reported production bug: player cross-midnight on a 24/7 pitch ───────

func TestContinuity24h_PlayerCrossMidnightAccepted(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "00:00", "00:00")

	start := xmAt(date, 23, 0)
	end := xmAt(date.AddDate(0, 0, 1), 0, 30)
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("23:00->00:30 on a 24/7 pitch should succeed via the merged gate, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}

// ── Full day followed by a CLOSED day: no abutment → no merge ────────────────

func TestContinuity24h_ClosedNextDayRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	nextWD := int(date.AddDate(0, 0, 1).Weekday())
	e.weekScheduleExcept(t, "00:00", "00:00", nextWD)

	if _, err := e.book(xmAt(date, 23, 0), xmAt(date.AddDate(0, 0, 1), 0, 30), false); !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("23:00->00:30 into a CLOSED day: err = %v, want ErrSlotOutsideOperatingHours", err)
	}
	// Ending exactly at midnight stays inside the full-day window and is kept.
	if _, err := e.book(xmAt(date, 23, 0), xmAt(date.AddDate(0, 0, 1), 0, 0), false); err != nil {
		t.Fatalf("23:00->00:00 (exact midnight end) should stay accepted, got %v", err)
	}
}

// ── Spill pitch 16:00→01:00: behavior unchanged by the merge ─────────────────

func TestContinuity24h_SpillPitchUnchanged(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "16:00", "01:00")

	if _, err := e.book(xmAt(date.AddDate(0, 0, 1), 0, 0), xmAt(date.AddDate(0, 0, 1), 1, 0), false); err != nil {
		t.Fatalf("00:00->01:00 in the spill tail should stay accepted, got %v", err)
	}
	if _, err := e.book(xmAt(date, 23, 30), xmAt(date.AddDate(0, 0, 1), 1, 30), false); !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("23:30->01:30 (past the 01:00 close): err = %v, want ErrSlotOutsideOperatingHours", err)
	}
}

// ── A range spanning a real closing gap must not merge ───────────────────────

func TestContinuity24h_GapNotMerged(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "16:00", "01:00")

	// 00:30 (inside date's tail) → 16:30 (inside date+1's window) spans the
	// 01:00→16:00 closed gap. The owner manual path exercises the gate without
	// the player handler's 120-minute cap.
	_, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID:     e.pitchID,
		Actor:       auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner},
		StartTime:   xmAt(date.AddDate(0, 0, 1), 0, 30).UTC(),
		EndTime:     xmAt(date.AddDate(0, 0, 1), 16, 30).UTC(),
		GuestName:   "Gap Span",
		RepeatWeeks: 1,
	})
	var rc *RecurrenceConflictError
	if !errors.As(err, &rc) || rc.Reason != "outside_hours" {
		t.Fatalf("gap-spanning range: err = %v, want RecurrenceConflictError{outside_hours}", err)
	}
}

// ── Multi-day owner span on a 24/7 pitch: proves RANGE anchoring, not D+1 ────
// Owner and admin pair on the manual path (both roles hit the same gate).

func TestContinuity24h_MultiDayOwnerSpanAccepted(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "00:00", "00:00")

	actors := []auth.Actor{
		{UserID: int(e.ownerID), Role: auth.RoleOwner},
		{UserID: int(e.ownerID), Role: auth.RoleAdmin}, // admin unscoped, same gate
	}
	for i, actor := range actors {
		// Distinct 3+ day spans (Mon-ish 23:00 → +3d 05:00) so the EXCLUDE
		// constraint never collides between the two role runs.
		s := xmAt(date.AddDate(0, 0, i*7), 23, 0).UTC()
		en := xmAt(date.AddDate(0, 0, i*7+3), 5, 0).UTC()
		bookings, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
			PitchID:     e.pitchID,
			Actor:       actor,
			StartTime:   s,
			EndTime:     en,
			GuestName:   "Tournament Hold",
			RepeatWeeks: 1,
		})
		if err != nil {
			t.Fatalf("role %s: 3-day span on a 24/7 pitch should be accepted, got %v", actor.Role, err)
		}
		if got := bookings[0].EndTime.Sub(bookings[0].StartTime); got != 54*time.Hour {
			t.Fatalf("role %s: persisted span = %s, want 54h (23:00 D → 05:00 D+3)", actor.Role, got)
		}
	}
}

// ── Academy path (owner × admin): cross-midnight session on a 24/7 pitch ─────

func TestContinuity24h_AcademyCrossMidnight(t *testing.T) {
	groupIDs := map[string]string{
		auth.RoleOwner: "0b24c0f1-1111-4a6e-9c1a-240000000001",
		auth.RoleAdmin: "0b24c0f1-2222-4a6e-9c1a-240000000002",
	}
	for _, role := range []string{auth.RoleOwner, auth.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			e := newOHGateEnv(t)
			date := e.futureDate()
			e.fullWeekSchedule(t, "00:00", "00:00")

			bookings, _, err := e.repo.CreateAcademyBookings(context.Background(), AcademyBookingParams{
				PitchID:           e.pitchID,
				Actor:             auth.Actor{UserID: int(e.ownerID), Role: role},
				AcademyName:       "أكاديمية الاختبار",
				DaysOfWeek:        []int{int(date.Weekday())},
				StartClock:        "23:00",
				EndClock:          "00:30", // ≤ start → cross-midnight session
				StartDate:         xmAt(date, 0, 0),
				EndDate:           xmAt(date, 0, 0),
				BypassHours:       false,
				RecurrenceGroupID: groupIDs[role],
			})
			if err != nil {
				t.Fatalf("role %s: academy 23:00->00:30 on a 24/7 pitch should succeed, got %v", role, err)
			}
			if len(bookings) != 1 {
				t.Fatalf("role %s: len(bookings) = %d, want 1", role, len(bookings))
			}
		})
	}
}

// ── D-B regression proof: READ paths stay day-shaped (never merged) ──────────

func TestContinuity24h_ReadPathsStayDayShaped(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	ctx := context.Background()
	owner := auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}
	loc := timeutil.Amman()
	y, m, d := date.In(loc).Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, loc).UTC()
	nextMidnight := time.Date(y, m, d+1, 0, 0, 0, 0, loc).UTC()

	// Shape 1: 24/7 (production pitches 1+2). GetOpenWindows must serve exactly
	// [D 00:00, D+1 00:00) — the merge must NOT leak into the payload.
	e.fullWeekSchedule(t, "00:00", "00:00")
	ivs, hasSchedule, err := e.repo.GetOpenWindows(ctx, int(e.pitchID), date)
	if err != nil || !hasSchedule {
		t.Fatalf("GetOpenWindows(24/7): err=%v hasSchedule=%v", err, hasSchedule)
	}
	if len(ivs) != 1 || !ivs[0].Start.Equal(dayStart) || !ivs[0].End.Equal(nextMidnight) {
		t.Fatalf("GetOpenWindows(24/7) must stay day-shaped [%s,%s), got %+v", dayStart, nextMidnight, ivs)
	}
	// Shape signal: continuous for the 24/7 production shape, with the full
	// 120-minute continuation (the next day is also a full 24h day).
	meta, err := e.repo.HoursMeta(ctx, int(e.pitchID), date, 120*time.Minute)
	if err != nil || meta.Shape != data.ShapeContinuous {
		t.Fatalf("HoursMeta(24/7): got %q err=%v, want %q", meta.Shape, err, data.ShapeContinuous)
	}
	if meta.ContinuationMinutes != 120 {
		t.Fatalf("HoursMeta(24/7) continuation = %d, want 120", meta.ContinuationMinutes)
	}

	// SearchAvailability: the closing cap on the 24/7 shape must remain the
	// anchored day's End (next midnight) — a merged window would inflate it.
	startAt := xmAt(date, 20, 0).UTC()
	results, err := e.model.SearchAvailability(ctx, data.AvailabilityQuery{AmmanDate: date, Start: startAt})
	if err != nil {
		t.Fatalf("SearchAvailability: %v", err)
	}
	found := false
	for _, r := range results {
		if int64(r.ID) == e.pitchID {
			found = true
			if !r.AvailableUntil.Equal(nextMidnight) {
				t.Fatalf("SearchAvailability(24/7) available_until = %s, want next midnight %s", r.AvailableUntil, nextMidnight)
			}
			if r.AvailableMinutes != 240 {
				t.Fatalf("SearchAvailability(24/7) available_minutes = %d, want 240 (20:00→24:00)", r.AvailableMinutes)
			}
		}
	}
	if !found {
		t.Fatalf("SearchAvailability did not return the test pitch")
	}

	// Day view + calendar: OpenWindows day-shaped for the 24/7 shape.
	dv, err := NewDayViewRepository(e.pool).OwnerDayView(ctx, owner, e.pitchID, date)
	if err != nil {
		t.Fatalf("OwnerDayView: %v", err)
	}
	if len(dv.OpenWindows) != 1 || !dv.OpenWindows[0].End.Equal(nextMidnight) {
		t.Fatalf("OwnerDayView(24/7) OpenWindows must stay day-shaped, got %+v", dv.OpenWindows)
	}
	cal, err := NewCalendarRepository(e.pool).OwnerDayCalendar(ctx, owner, date)
	if err != nil {
		t.Fatalf("OwnerDayCalendar: %v", err)
	}
	calFound := false
	for _, row := range cal.Pitches {
		if row.PitchID == e.pitchID {
			calFound = true
			if len(row.OpenWindows) != 1 || !row.OpenWindows[0].End.Equal(nextMidnight) {
				t.Fatalf("OwnerDayCalendar(24/7) OpenWindows must stay day-shaped, got %+v", row.OpenWindows)
			}
		}
	}
	if !calFound {
		t.Fatalf("OwnerDayCalendar did not include the test pitch")
	}

	// Shape 2: spill 16:00→01:00 (production pitch 3). Window keeps its own
	// past-midnight End; shape = spill (NOT day_bounded — the distinction a
	// single continuity boolean could not carry).
	e.fullWeekSchedule(t, "16:00", "01:00")
	spillEnd := time.Date(y, m, d+1, 1, 0, 0, 0, loc).UTC()
	ivs, _, err = e.repo.GetOpenWindows(ctx, int(e.pitchID), date)
	if err != nil {
		t.Fatalf("GetOpenWindows(spill): %v", err)
	}
	// Candidate set = target day's window + previous day's spill tail (2 rows).
	var target *data.ConcreteInterval
	for i := range ivs {
		if ivs[i].End.Equal(spillEnd) {
			target = &ivs[i]
		}
	}
	if target == nil || len(ivs) != 2 {
		t.Fatalf("GetOpenWindows(spill) must serve the unmerged 2-window candidate set incl. [16:00,%s), got %+v", spillEnd, ivs)
	}
	meta, err = e.repo.HoursMeta(ctx, int(e.pitchID), date, 120*time.Minute)
	if err != nil || meta.Shape != data.ShapeSpill {
		t.Fatalf("HoursMeta(spill): got %q err=%v, want %q", meta.Shape, err, data.ShapeSpill)
	}
	if meta.ContinuationMinutes != 0 {
		t.Fatalf("HoursMeta(spill) continuation = %d, want 0 (tail already in windows)", meta.ContinuationMinutes)
	}

	// Shape 3: full day followed by a CLOSED day → day_bounded (no abutment).
	nextWD := int(date.AddDate(0, 0, 1).Weekday())
	e.weekScheduleExcept(t, "00:00", "00:00", nextWD)
	meta, err = e.repo.HoursMeta(ctx, int(e.pitchID), date, 120*time.Minute)
	if err != nil || meta.Shape != data.ShapeDayBounded {
		t.Fatalf("HoursMeta(full day + closed next): got %q err=%v, want %q", meta.Shape, err, data.ShapeDayBounded)
	}
	if meta.ContinuationMinutes != 0 {
		t.Fatalf("HoursMeta(full day + closed next) continuation = %d, want 0", meta.ContinuationMinutes)
	}
}

// ── Booked-slots lookahead past the civil day ────────────────────────────────
//
// On a continuously-open pitch a booking STARTING on day D may end after
// midnight, so D's availability payload must reveal any booking such a start
// could collide with — including ones that START on D+1. The plain day window
// [dayStart, dayEnd) excludes a booking beginning exactly at dayEnd, which let
// the client offer 23:30+120 against an existing 00:30 booking and take a 409
// from the EXCLUDE constraint. Reproduced live before the fix.

func TestContinuity24h_BookedSlotsLookahead(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "00:00", "00:00") // 24/7 — production pitches 1 & 2
	ctx := context.Background()

	// A booking that starts AFTER the next midnight, i.e. outside the civil day.
	nextDay := date.AddDate(0, 0, 1)
	bStart := xmAt(nextDay, 0, 30)
	bEnd := xmAt(nextDay, 1, 30)
	if _, _, err := e.repo.CreateManualBooking(ctx, ManualBookingParams{
		PitchID:     e.pitchID,
		Actor:       auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner},
		StartTime:   bStart.UTC(),
		EndTime:     bEnd.UTC(),
		GuestName:   "Next-day early booking",
		RepeatWeeks: 1,
	}); err != nil {
		t.Fatalf("seed next-day 00:30 booking: %v", err)
	}

	slots, err := e.repo.GetBookedSlots(ctx, int(e.pitchID), date)
	if err != nil {
		t.Fatalf("GetBookedSlots: %v", err)
	}
	var found bool
	for _, s := range slots {
		if s.StartTime.Equal(bStart.UTC()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("day %s payload must reveal the next-day 00:30 booking a 23:30 start could collide with; got %d slots: %+v",
			date.Format("2006-01-02"), len(slots), slots)
	}

	// And the collision the client would otherwise offer is genuinely a
	// conflict — proving the lookahead guards a REAL rejection, not a
	// hypothetical one.
	if _, err := e.book(xmAt(date, 23, 30), xmAt(nextDay, 1, 30), false); !errors.Is(err, ErrDoubleBooking) {
		t.Fatalf("23:30→01:30 overlapping the next-day booking: err = %v, want ErrDoubleBooking", err)
	}
}

// TestContinuity24h_LookaheadCoversSpillOverhang is the regression test for the
// reviewed defect in the FIRST version of this lookahead. That version bounded
// the horizon at dayEnd + the player ceiling, reasoning that no same-day start
// could reach further. That reasoning holds only on the civil day — the client
// attributes starts by the SESSION AXIS, and a spill window ends on the next
// calendar day, so its late marks reach far past dayEnd + 120m.
//
// `18:00→04:00` is a legal schedule (ValidateSchedule caps no window's length).
// Its window ends 04:00 the next day, and the card offers starts up to 03:00.
// A booking at 03:30 is therefore collidable from this day and MUST appear in
// this day's payload; under the old bound (02:00) it did not, and the card
// offered a slot the EXCLUDE constraint rejected.
func TestContinuity24h_LookaheadCoversSpillOverhang(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	e.fullWeekSchedule(t, "18:00", "04:00") // spills 4h past midnight
	ctx := context.Background()

	// 03:00→04:00 fills the window's tail. It starts a full hour past the OLD
	// bound (dayEnd + 120m = 02:00), so it was invisible to day D's payload
	// while remaining perfectly collidable from day D. (chk_min_duration
	// enforces a 60-minute floor, so the seed cannot be shorter.)
	nextDay := date.AddDate(0, 0, 1)
	bStart := xmAt(nextDay, 3, 0)
	bEnd := xmAt(nextDay, 4, 0)
	if _, _, err := e.repo.CreateManualBooking(ctx, ManualBookingParams{
		PitchID:     e.pitchID,
		Actor:       auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner},
		StartTime:   bStart.UTC(),
		EndTime:     bEnd.UTC(),
		GuestName:   "Late spill-tail booking",
		RepeatWeeks: 1,
	}); err != nil {
		t.Fatalf("seed next-day 03:00 booking: %v", err)
	}

	slots, err := e.repo.GetBookedSlots(ctx, int(e.pitchID), date)
	if err != nil {
		t.Fatalf("GetBookedSlots: %v", err)
	}
	var found bool
	for _, s := range slots {
		if s.StartTime.Equal(bStart.UTC()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("an 18:00→04:00 pitch offers starts past 02:00, so day %s must reveal the 03:00 booking; got %d slots: %+v",
			date.Format("2006-01-02"), len(slots), slots)
	}

	// The collision is real: a 02:30 start — comfortably offered by the card on
	// this schedule — overlaps the seeded booking, so hiding it would have meant
	// offering a slot the constraint rejects.
	if _, err := e.book(xmAt(nextDay, 2, 30), xmAt(nextDay, 3, 30), false); !errors.Is(err, ErrDoubleBooking) {
		t.Fatalf("02:30→03:30 overlapping the 03:00 booking: err = %v, want ErrDoubleBooking", err)
	}
}

// ── No schema change in this WO ──────────────────────────────────────────────

func TestContinuity24h_SchemaBaselineUnchanged(t *testing.T) {
	e := newOHGateEnv(t)
	testutil.AssertSchemaBaseline(t, e.pool)
}
