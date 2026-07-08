package repository

// WO-SERIES-CANCEL / PR-1 — DB-backed tests for GroupUpcoming (the cancel-all
// confirm-dialog preview) and the additive recurrence_group_id payload field on
// the Day View + Schedule surfaces. Reuses blockEnv (owner/other/pitch fixtures).
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run "GroupUpcoming|SeriesPayload"

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// seedRowFull inserts one manual row with explicit group (nil → one-off) and
// amount_paid (nil → untracked), returning its id.
func (e *blockEnv) seedRowFull(t *testing.T, group *string, start time.Time, dur time.Duration, status string, amountPaid *float64) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source, guest_name, recurrence_group_id, amount_paid)
		VALUES ($1, NULL, tstzrange($2::timestamptz, $3::timestamptz, '[)'), 30, $4::booking_status, 'manual', 'ضيف', $5, $6)
		RETURNING id
	`, e.pitchID, start.UTC(), start.Add(dur).UTC(), status, group, amountPaid).Scan(&id); err != nil {
		t.Fatalf("seedRowFull: %v", err)
	}
	return id
}

func fptr(v float64) *float64 { return &v }

// ── GroupUpcoming: count == what CancelFutureGroup would remove ──────────────

func TestGroupUpcoming_CountExcludesPastAndCancelled(t *testing.T) {
	e := newBlockEnv(t)
	group := uuid.NewString()
	now := time.Now()

	// 4-instance series: 1 past (confirmed), 1 cancelled (future), 2 future confirmed.
	e.seedRowFull(t, &group, now.Add(-48*time.Hour), time.Hour, "confirmed", nil)
	e.seedRowFull(t, &group, now.Add(24*time.Hour), time.Hour, "cancelled", nil)
	e.seedRowFull(t, &group, now.Add(48*time.Hour), time.Hour, "confirmed", nil)
	e.seedRowFull(t, &group, now.Add(72*time.Hour), time.Hour, "confirmed", nil)

	count, tracked, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group, Actor: e.ownerActor(), ActorID: e.ownerID,
	})
	if err != nil {
		t.Fatalf("GroupUpcoming: %v", err)
	}
	if count != 2 {
		t.Errorf("upcoming_count = %d, want 2 (past + cancelled excluded)", count)
	}
	if tracked {
		t.Errorf("has_tracked_money = true, want false (no amount_paid set)")
	}
}

func TestGroupUpcoming_TrackedMoney(t *testing.T) {
	e := newBlockEnv(t)
	now := time.Now()

	// Distinct, non-overlapping 1-hour windows per sub-case (one shared pitch → the
	// GIST EXCLUDE rejects any overlap between seeded rows).

	// (a) none tracked → false.
	gNone := uuid.NewString()
	e.seedRowFull(t, &gNone, now.Add(24*time.Hour), time.Hour, "confirmed", nil)
	if _, tracked, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: gNone, Actor: e.ownerActor(), ActorID: e.ownerID,
	}); err != nil || tracked {
		t.Errorf("none-tracked: tracked=%v err=%v, want false/nil", tracked, err)
	}

	// (b) an UPCOMING sibling has amount_paid → true.
	gUp := uuid.NewString()
	e.seedRowFull(t, &gUp, now.Add(48*time.Hour), time.Hour, "confirmed", nil)
	e.seedRowFull(t, &gUp, now.Add(72*time.Hour), time.Hour, "confirmed", fptr(15))
	if _, tracked, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: gUp, Actor: e.ownerActor(), ActorID: e.ownerID,
	}); err != nil || !tracked {
		t.Errorf("upcoming-tracked: tracked=%v err=%v, want true/nil", tracked, err)
	}

	// (c) only a PAST sibling has amount_paid → false (past is out of the window).
	gPast := uuid.NewString()
	e.seedRowFull(t, &gPast, now.Add(-48*time.Hour), time.Hour, "confirmed", fptr(30))
	e.seedRowFull(t, &gPast, now.Add(96*time.Hour), time.Hour, "confirmed", nil)
	if _, tracked, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: gPast, Actor: e.ownerActor(), ActorID: e.ownerID,
	}); err != nil || tracked {
		t.Errorf("past-only-tracked: tracked=%v err=%v, want false/nil", tracked, err)
	}
}

// Dialog-honesty proof: the previewed count equals what the DELETE then removes.
func TestGroupUpcoming_CountEqualsDelete(t *testing.T) {
	e := newBlockEnv(t)
	group := uuid.NewString()
	now := time.Now()

	e.seedRowFull(t, &group, now.Add(-24*time.Hour), time.Hour, "confirmed", nil) // past
	e.seedRowFull(t, &group, now.Add(24*time.Hour), time.Hour, "confirmed", nil)
	e.seedRowFull(t, &group, now.Add(48*time.Hour), time.Hour, "confirmed", nil)
	e.seedRowFull(t, &group, now.Add(72*time.Hour), time.Hour, "confirmed", nil)

	preview, _, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group, Actor: e.ownerActor(), ActorID: e.ownerID,
	})
	if err != nil {
		t.Fatalf("GroupUpcoming: %v", err)
	}
	cancelled, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group, Actor: e.ownerActor(), ActorID: e.ownerID,
	})
	if err != nil {
		t.Fatalf("CancelFutureGroup: %v", err)
	}
	if preview != cancelled {
		t.Fatalf("dialog dishonesty: upcoming_count=%d but cancelled_count=%d", preview, cancelled)
	}
	if preview != 3 {
		t.Errorf("expected 3 upcoming, got %d", preview)
	}
}

// Cross-tenant → ErrPitchNotFound (404); unknown group on OWNED pitch → {0,false}.
func TestGroupUpcoming_ScopeAndEmpty(t *testing.T) {
	e := newBlockEnv(t)

	// otherID owns a different pitch → previewing e.pitchID's group is a 404.
	_, _, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: uuid.NewString(),
		Actor: auth.Actor{UserID: int(e.otherID), Role: auth.RoleOwner}, ActorID: e.otherID,
	})
	if err != ErrPitchNotFound {
		t.Fatalf("cross-tenant err = %v, want ErrPitchNotFound (404)", err)
	}

	// Owner, unknown group on their OWN pitch → 0/false, no error (idempotent-empty).
	count, tracked, err := e.repo.GroupUpcoming(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: uuid.NewString(), Actor: e.ownerActor(), ActorID: e.ownerID,
	})
	if err != nil || count != 0 || tracked {
		t.Fatalf("unknown-group = (%d,%v,%v), want (0,false,nil)", count, tracked, err)
	}
}

// ── Additive recurrence_group_id payload on both surfaces ────────────────────

func TestSeriesPayload_DayViewAndSchedule(t *testing.T) {
	e := newBlockEnv(t)
	group := uuid.NewString()

	// A fixed future Amman day; a series row + a one-off row on it.
	day := time.Date(2031, 8, 10, 0, 0, 0, 0, timeutil.Amman())
	fromUTC, toUTC := timeutil.AmmanDayBoundsUTC(day)
	seriesStart := day.Add(9 * time.Hour) // 09:00 Amman
	oneOffStart := day.Add(11 * time.Hour)
	seriesID := e.seedRowFull(t, &group, seriesStart, time.Hour, "confirmed", nil)
	oneOffID := e.seedRowFull(t, nil, oneOffStart, time.Hour, "confirmed", nil)

	// Day View: the booked slots carry recurrence_group_id (series) / null (one-off).
	dv := NewDayViewRepository(e.pool)
	view, err := dv.OwnerDayView(context.Background(), e.ownerActor(), e.pitchID, day)
	if err != nil {
		t.Fatalf("OwnerDayView: %v", err)
	}
	var sawSeries, sawOneOff bool
	for _, s := range view.Slots {
		if s.Booking == nil {
			continue
		}
		switch s.Booking.ID {
		case seriesID:
			sawSeries = true
			if s.Booking.RecurrenceGroupID == nil || *s.Booking.RecurrenceGroupID != group {
				t.Errorf("day-view series slot: recurrence_group_id = %v, want %s", s.Booking.RecurrenceGroupID, group)
			}
		case oneOffID:
			sawOneOff = true
			if s.Booking.RecurrenceGroupID != nil {
				t.Errorf("day-view one-off slot: recurrence_group_id = %v, want nil", *s.Booking.RecurrenceGroupID)
			}
		}
	}
	if !sawSeries || !sawOneOff {
		t.Fatalf("day-view: sawSeries=%v sawOneOff=%v (both must appear)", sawSeries, sawOneOff)
	}

	// Schedule: same assertion over the row list.
	sched := NewScheduleRepository(e.pool)
	rows, err := sched.DailySchedule(context.Background(), e.ownerActor(), nil, int(e.pitchID), fromUTC, toUTC)
	if err != nil {
		t.Fatalf("DailySchedule: %v", err)
	}
	var schedSeries, schedOneOff bool
	for _, r := range rows {
		switch r.ID {
		case seriesID:
			schedSeries = true
			if r.RecurrenceGroupID == nil || *r.RecurrenceGroupID != group {
				t.Errorf("schedule series row: recurrence_group_id = %v, want %s", r.RecurrenceGroupID, group)
			}
		case oneOffID:
			schedOneOff = true
			if r.RecurrenceGroupID != nil {
				t.Errorf("schedule one-off row: recurrence_group_id = %v, want nil", *r.RecurrenceGroupID)
			}
		}
	}
	if !schedSeries || !schedOneOff {
		t.Fatalf("schedule: sawSeries=%v sawOneOff=%v (both must appear)", schedSeries, schedOneOff)
	}
}
