package repository

// Integration tests for RECURRING walk-ins (PR 3.5 Phase B) against a live DB.
// They grade the three Gate-2 acceptance criteria:
//  1. Happy-path revenue — N priced rows sharing one recurrence_group_id.
//  2. All-or-nothing rollback — a week-3 conflict names week 3 and leaves ZERO rows.
//  3. Idempotency — exact replay (under the lock, verbatim) AND retry-after-rollback
//     (the UUID key is not poisoned by a rolled-back attempt).
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Recurring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// countGroupRows returns how many bookings rows carry the group id (any status).
func (e *blockEnv) countGroupRows(t *testing.T, groupID string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings WHERE recurrence_group_id = $1`, groupID).Scan(&n); err != nil {
		t.Fatalf("countGroupRows: %v", err)
	}
	return n
}

// ── Criterion 1: happy-path revenue — N priced rows, one shared group id ─────

func TestRecurring_HappyPathRevenueAndGrouping(t *testing.T) {
	e := newBlockEnv(t)
	base := e.futureAt(200) // open 24/7 (no schedule), so the gate passes
	end := base.Add(time.Hour)
	group := uuid.NewString()

	bookings, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: end,
		GuestName: "أكاديمية الأحد", RepeatWeeks: 4, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("4-week recurring create: %v", err)
	}
	if replayed {
		t.Fatalf("fresh create reported replayed=true")
	}
	if len(bookings) != 4 {
		t.Fatalf("created %d rows, want 4", len(bookings))
	}
	for i, b := range bookings {
		// Each row is a priced manual occurrence in the shared group.
		if b.Source != models.SourceManual || b.PlayerID != nil {
			t.Errorf("week %d: source=%q player_id=%v, want manual/nil", i+1, b.Source, b.PlayerID)
		}
		if b.TotalPrice <= 0 {
			t.Errorf("week %d: total_price=%v, want >0 (revenue via shared insert builder)", i+1, b.TotalPrice)
		}
		if b.RecurrenceGroupID == nil || *b.RecurrenceGroupID != group {
			t.Errorf("week %d: recurrence_group_id=%v, want %s", i+1, b.RecurrenceGroupID, group)
		}
		// Exactly 7 Amman days between consecutive occurrences.
		if i > 0 {
			gap := b.StartTime.Sub(bookings[i-1].StartTime)
			if gap != 7*24*time.Hour {
				t.Errorf("week %d gap = %v, want 168h", i+1, gap)
			}
		}
	}
	if got := e.countGroupRows(t, group); got != 4 {
		t.Fatalf("DB rows for group = %d, want 4", got)
	}
}

// ── Criterion 2: all-or-nothing — week-3 conflict names week 3, ZERO rows ────

func TestRecurring_AllOrNothingRollbackOnWeek3(t *testing.T) {
	e := newBlockEnv(t)
	base := e.futureAt(220)
	end := base.Add(time.Hour)

	// Pre-occupy the WEEK-3 slot (base + 14 days) with a player booking.
	week3Start := addWeeksAmman(base, 2)
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: week3Start, EndTime: week3Start.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed week-3 conflict: %v", err)
	}

	group := uuid.NewString()
	_, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: end,
		GuestName: "أكاديمية متعارضة", RepeatWeeks: 4, RecurrenceGroupID: group,
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) || rec.Reason != "conflict" {
		t.Fatalf("err = %v, want conflict RecurrenceConflictError", err)
	}
	if rec.Week != 3 {
		t.Errorf("failing week = %d, want 3", rec.Week)
	}
	// The payload names week 3's attempted slot.
	if !rec.OccStart.Equal(week3Start.UTC()) {
		t.Errorf("failing occurrence start = %s, want %s", rec.OccStart, week3Start.UTC())
	}
	if rec.Conflicts[0].Source != models.SourcePlayer || rec.Conflicts[0].PlayerName == nil {
		t.Errorf("conflict detail = %+v, want the player booking", rec.Conflicts[0])
	}
	// Full rollback: NO rows for the group (weeks 1 & 2 were undone).
	if got := e.countGroupRows(t, group); got != 0 {
		t.Fatalf("rows for group after rollback = %d, want 0 (no partial writes)", got)
	}
}

// ── Criterion 3a: exact replay — same group id returns stored rows verbatim ───

func TestRecurring_ExactReplayShortCircuits(t *testing.T) {
	e := newBlockEnv(t)
	base := e.futureAt(260)
	end := base.Add(time.Hour)
	group := uuid.NewString()

	first, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: end,
		GuestName: "أكاديمية أصلية", RepeatWeeks: 3, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Resubmit the SAME group id — with DELIBERATELY DIFFERENT payload params to
	// prove the loop never runs (the new params are ignored, stored rows replayed).
	replayRows, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(),
		StartTime: base.Add(48 * time.Hour), EndTime: base.Add(49 * time.Hour),
		GuestName: "محاولة مختلفة", RepeatWeeks: 9, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed {
		t.Fatalf("second submit replayed=false, want true (must short-circuit under the lock)")
	}
	if len(replayRows) != len(first) {
		t.Fatalf("replay returned %d rows, want %d (no duplication, no 409)", len(replayRows), len(first))
	}
	for i := range first {
		if replayRows[i].ID != first[i].ID {
			t.Errorf("replay row %d id=%d, want verbatim %d", i, replayRows[i].ID, first[i].ID)
		}
	}
	// No extra rows written by the replay.
	if got := e.countGroupRows(t, group); got != 3 {
		t.Fatalf("rows for group after replay = %d, want 3 (verbatim, no duplicates)", got)
	}
}

// ── Criterion 3b: retry-after-rollback — the UUID key is not poisoned ─────────

func TestRecurring_RetryAfterRollbackSucceeds(t *testing.T) {
	e := newBlockEnv(t)
	base := e.futureAt(300)
	end := base.Add(time.Hour)
	group := uuid.NewString()

	// Block the WEEK-2 slot so the first attempt fails mid-series and rolls back.
	week2Start := addWeeksAmman(base, 1)
	blk, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: week2Start, EndTime: week2Start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed week-2 block: %v", err)
	}

	// First attempt → conflict on week 2, full rollback, zero rows for the group.
	_, _, err = e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: end,
		GuestName: "أكاديمية", RepeatWeeks: 3, RecurrenceGroupID: group,
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) || rec.Week != 2 {
		t.Fatalf("first attempt err = %v, want conflict on week 2", err)
	}
	if got := e.countGroupRows(t, group); got != 0 {
		t.Fatalf("rows after first rollback = %d, want 0", got)
	}

	// RESOLVE the conflict: unblock week 2.
	ownerID := e.ownerID
	if _, err := e.repo.CancelBooking(context.Background(), CancelBookingParams{
		BookingID: blk.ID, ActorID: &ownerID, ActorRole: ActorOwner, RequireSource: "block",
	}); err != nil {
		t.Fatalf("unblock week 2: %v", err)
	}

	// Resubmit with the SAME group id → must now succeed with the full set (the key
	// was never poisoned by the rolled-back attempt).
	rows, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: end,
		GuestName: "أكاديمية", RepeatWeeks: 3, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("retry after resolving conflict: %v", err)
	}
	if replayed {
		t.Fatalf("retry reported replayed=true, but the first attempt wrote nothing to replay")
	}
	if len(rows) != 3 {
		t.Fatalf("retry created %d rows, want 3", len(rows))
	}
	if got := e.countGroupRows(t, group); got != 3 {
		t.Fatalf("rows for group after retry = %d, want 3", got)
	}
}

// ── Patch 1a: a recurring OFF-HOURS submit is rejected, naming the first week ──
// Every occurrence shares the weekday+time, so an off-hours slot fails at week 1.

func TestRecurring_OffHoursRejectsNamingFirstWeek(t *testing.T) {
	e := newBlockEnv(t)
	// Configure 09:00–12:00 on the base date's weekday so a 20:00 slot is off-hours
	// every week (all occurrences land on the same weekday).
	date := time.Now().In(timeutil.Amman()).AddDate(0, 0, 4)
	wd := int(date.Weekday())
	if err := e.model.ReplaceOperatingHours(context.Background(), int(e.pitchID), e.ownerActor(),
		[]data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "12:00"}}); err != nil {
		t.Fatalf("set hours: %v", err)
	}
	y, m, d := date.Date()
	loc := timeutil.Amman()
	base := time.Date(y, m, d, 20, 0, 0, 0, loc)
	group := uuid.NewString()

	_, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: base.Add(time.Hour),
		GuestName: "أكاديمية مسائية", RepeatWeeks: 4, RecurrenceGroupID: group,
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) || rec.Reason != "outside_hours" {
		t.Fatalf("off-hours recurring: err = %v, want outside_hours RecurrenceConflictError", err)
	}
	if rec.Week != 1 {
		t.Errorf("failing week = %d, want 1 (first off-hours occurrence)", rec.Week)
	}
	if got := e.countGroupRows(t, group); got != 0 {
		t.Fatalf("rows after off-hours reject = %d, want 0", got)
	}
}

// ── Patch 1b: force_bypass_hours is honored on EVERY iteration, not just week 1 ─

func TestRecurring_OffHoursBypassCreatesAll(t *testing.T) {
	e := newBlockEnv(t)
	date := time.Now().In(timeutil.Amman()).AddDate(0, 0, 5)
	wd := int(date.Weekday())
	if err := e.model.ReplaceOperatingHours(context.Background(), int(e.pitchID), e.ownerActor(),
		[]data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "12:00"}}); err != nil {
		t.Fatalf("set hours: %v", err)
	}
	y, m, d := date.Date()
	loc := timeutil.Amman()
	base := time.Date(y, m, d, 20, 0, 0, 0, loc) // off-hours every week
	group := uuid.NewString()

	rows, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: base.Add(time.Hour),
		GuestName: "أكاديمية مسائية", RepeatWeeks: 4, RecurrenceGroupID: group, BypassHours: true,
	})
	if err != nil {
		t.Fatalf("off-hours recurring with bypass: want success, got %v", err)
	}
	if replayed || len(rows) != 4 {
		t.Fatalf("rows=%d replayed=%v, want 4 fresh (bypass honored on all weeks)", len(rows), replayed)
	}
	if got := e.countGroupRows(t, group); got != 4 {
		t.Fatalf("DB rows for group = %d, want 4", got)
	}
}

// ── Patch 3: idempotency replay is scoped to the pitch ───────────────────────
// A stale group id reused on a DIFFERENT pitch must NOT replay the first pitch's
// rows — it materialises fresh occurrences on the second pitch.

func TestRecurring_ReplayScopedToPitch(t *testing.T) {
	e := newBlockEnv(t)
	// A second pitch owned by the same owner.
	p2, err := e.model.CreatePitch(context.Background(), data.CreatePitchRequest{
		Name: "BK Pitch 2", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 40, OwnerID: int(e.ownerID),
	})
	if err != nil {
		t.Fatalf("seed pitch 2: %v", err)
	}
	pitch2 := int64(p2.ID)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pitch2)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, pitch2)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitch2)
	})

	base := e.futureAt(340)
	group := uuid.NewString()

	// Materialise on pitch A.
	rowsA, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: base.Add(time.Hour),
		GuestName: "أكاديمية أ", RepeatWeeks: 2, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("create on pitch A: %v", err)
	}

	// Reuse the SAME group id on pitch B → must NOT replay A; must create fresh.
	rowsB, replayed, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: pitch2, Actor: e.ownerActor(), StartTime: base, EndTime: base.Add(time.Hour),
		GuestName: "أكاديمية ب", RepeatWeeks: 2, RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("create on pitch B with reused group id: %v", err)
	}
	if replayed {
		t.Fatalf("pitch B reported replayed=true — replay leaked across pitches")
	}
	if len(rowsB) != 2 {
		t.Fatalf("pitch B created %d rows, want 2 (fresh, not A's)", len(rowsB))
	}
	for _, b := range rowsB {
		if b.PitchID != pitch2 {
			t.Errorf("pitch B row has pitch_id=%d, want %d", b.PitchID, pitch2)
		}
	}
	// IDs must be disjoint from A's.
	for _, a := range rowsA {
		for _, b := range rowsB {
			if a.ID == b.ID {
				t.Fatalf("pitch B replayed pitch A's row id=%d", a.ID)
			}
		}
	}
}

// ── Patch 4: each of the N occurrences writes its own creation status_transition ─

func TestRecurring_AuditRowPerOccurrence(t *testing.T) {
	e := newBlockEnv(t)
	base := e.futureAt(380)
	group := uuid.NewString()
	const weeks = 5

	if _, _, err := e.repo.CreateManualBooking(context.Background(), ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: base, EndTime: base.Add(time.Hour),
		GuestName: "أكاديمية مدققة", RepeatWeeks: weeks, RecurrenceGroupID: group,
	}); err != nil {
		t.Fatalf("create %d-week series: %v", weeks, err)
	}

	var transitions int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM status_transitions st
		JOIN bookings b ON b.id = st.booking_id
		WHERE b.recurrence_group_id = $1 AND st.to_status = 'confirmed' AND st.from_status IS NULL
	`, group).Scan(&transitions); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if transitions != weeks {
		t.Fatalf("creation transitions = %d, want %d (one audited NULL→confirmed per occurrence)", transitions, weeks)
	}
}
