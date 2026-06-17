package repository

// Tests for the Academy Booking Generator (Discrete Bulk Insert).
//
//   - expandAcademyOccurrences is a PURE function (no DB) — those tests always run.
//   - The CreateAcademyBookings integration tests reuse blockEnv and are SKIPPED
//     unless PITCH_SCOPING_TEST_DATABASE_URL, exactly like the recurring suite:
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Academy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ── Pure expansion: days_of_week × [start,end] at a fixed window ──────────────

func TestExpandAcademy_MultiDayAcrossRange(t *testing.T) {
	loc := timeutil.Amman()
	// 2026-06-19 is a Friday (PG DOW 5); 2026-06-20 a Saturday (6).
	start := time.Date(2026, 6, 19, 0, 0, 0, 0, loc)
	end := time.Date(2026, 7, 3, 0, 0, 0, 0, loc) // two full Fri+Sat weeks inclusive
	occ, err := expandAcademyOccurrences(AcademyBookingParams{
		DaysOfWeek: []int{5, 6}, StartClock: "17:00", EndClock: "19:30",
		StartDate: start, EndDate: end,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// Fri 19, Sat 20, Fri 26, Sat 27, Fri 3-Jul → 5 occurrences (Sat 4-Jul is past end).
	if len(occ) != 5 {
		t.Fatalf("got %d occurrences, want 5", len(occ))
	}
	for _, o := range occ {
		wd := int(o[0].In(loc).Weekday())
		if wd != 5 && wd != 6 {
			t.Errorf("occurrence weekday=%d, want Fri(5)/Sat(6)", wd)
		}
		if d := o[1].Sub(o[0]); d != 150*time.Minute {
			t.Errorf("duration=%v, want 2h30m", d)
		}
		if h := o[0].In(loc).Hour(); h != 17 {
			t.Errorf("start hour=%d, want 17", h)
		}
	}
	// Chronological + ascending.
	for i := 1; i < len(occ); i++ {
		if !occ[i][0].After(occ[i-1][0]) {
			t.Errorf("occurrence %d not after previous", i)
		}
	}
}

func TestExpandAcademy_CrossMidnight(t *testing.T) {
	loc := timeutil.Amman()
	start := time.Date(2026, 6, 19, 0, 0, 0, 0, loc) // Friday
	occ, err := expandAcademyOccurrences(AcademyBookingParams{
		DaysOfWeek: []int{5}, StartClock: "23:00", EndClock: "01:00",
		StartDate: start, EndDate: start,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(occ) != 1 {
		t.Fatalf("got %d, want 1", len(occ))
	}
	if d := occ[0][1].Sub(occ[0][0]); d != 2*time.Hour {
		t.Errorf("cross-midnight duration=%v, want 2h", d)
	}
	// End rolls to the next calendar day.
	if occ[0][1].In(loc).Day() != 20 {
		t.Errorf("end day=%d, want 20 (next day)", occ[0][1].In(loc).Day())
	}
}

func TestExpandAcademy_NoMatchingDays(t *testing.T) {
	loc := timeutil.Amman()
	// A single Friday range, but only Mondays selected → zero occurrences.
	fri := time.Date(2026, 6, 19, 0, 0, 0, 0, loc)
	occ, err := expandAcademyOccurrences(AcademyBookingParams{
		DaysOfWeek: []int{1}, StartClock: "17:00", EndClock: "18:00",
		StartDate: fri, EndDate: fri,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(occ) != 0 {
		t.Fatalf("got %d, want 0", len(occ))
	}
}

func TestExpandAcademy_OverCapErrors(t *testing.T) {
	loc := timeutil.Amman()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(3, 0, 0) // 3 years, every day → > cap
	_, err := expandAcademyOccurrences(AcademyBookingParams{
		DaysOfWeek: []int{0, 1, 2, 3, 4, 5, 6}, StartClock: "10:00", EndClock: "11:00",
		StartDate: start, EndDate: end,
	})
	if err == nil || err.Error() != "too_many_occurrences" {
		t.Fatalf("err=%v, want too_many_occurrences", err)
	}
}

// ── Integration: all-or-nothing + idempotency (live DB) ──────────────────────

// academyDate returns a future Friday (Amman) at least `minDays` out, as a midnight
// Amman date — a stable anchor for the generator's date-range inputs.
func academyDate(minDays int) time.Time {
	loc := timeutil.Amman()
	d := time.Now().In(loc).AddDate(0, 0, minDays)
	for d.Weekday() != time.Friday {
		d = d.AddDate(0, 0, 1)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

func TestAcademy_HappyPathDiscreteRows(t *testing.T) {
	e := newBlockEnv(t) // 24/7 pitch (no schedule) → hours-gate passes
	fri := academyDate(30)
	group := uuid.NewString()

	rows, replayed, err := e.repo.CreateAcademyBookings(context.Background(), AcademyBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), AcademyName: "أكاديمية النسور",
		DaysOfWeek: []int{5}, StartClock: "10:00", EndClock: "11:30",
		StartDate: fri, EndDate: fri.AddDate(0, 0, 21), // 4 Fridays inclusive
		RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("academy create: %v", err)
	}
	if replayed {
		t.Fatalf("fresh create reported replayed=true")
	}
	if len(rows) != 4 {
		t.Fatalf("created %d rows, want 4", len(rows))
	}
	for i, b := range rows {
		if b.Source != models.SourceAcademy || b.PlayerID != nil {
			t.Errorf("row %d: source=%q player_id=%v, want academy/nil", i, b.Source, b.PlayerID)
		}
		if b.GuestName == nil || *b.GuestName != "أكاديمية النسور" {
			t.Errorf("row %d: guest_name=%v, want the academy name", i, b.GuestName)
		}
		if b.TotalPrice <= 0 {
			t.Errorf("row %d: total_price=%v, want >0 (revenue)", i, b.TotalPrice)
		}
		if b.RecurrenceGroupID == nil || *b.RecurrenceGroupID != group {
			t.Errorf("row %d: group=%v, want %s", i, b.RecurrenceGroupID, group)
		}
	}
	if got := e.countGroupRows(t, group); got != 4 {
		t.Fatalf("DB rows for group = %d, want 4", got)
	}
}

func TestAcademy_AllOrNothingListsEveryConflict(t *testing.T) {
	e := newBlockEnv(t)
	fri := academyDate(60)
	loc := timeutil.Amman()
	at1000 := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), 10, 0, 0, 0, loc)
	}
	// Pre-occupy the 1st and 3rd Fridays so the generator must name BOTH (not just
	// the first) and write zero rows.
	for _, off := range []int{0, 14} {
		s := at1000(fri.AddDate(0, 0, off))
		if _, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
			PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: s, EndTime: s.Add(time.Hour),
		}); err != nil {
			t.Fatalf("seed block off=%d: %v", off, err)
		}
	}

	group := uuid.NewString()
	_, _, err := e.repo.CreateAcademyBookings(context.Background(), AcademyBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), AcademyName: "أكاديمية متعارضة",
		DaysOfWeek: []int{5}, StartClock: "10:00", EndClock: "11:00",
		StartDate: fri, EndDate: fri.AddDate(0, 0, 21), // 4 Fridays
		RecurrenceGroupID: group,
	})
	var ac *AcademyConflictError
	if !errors.As(err, &ac) {
		t.Fatalf("err=%v, want *AcademyConflictError", err)
	}
	if len(ac.Conflicts) != 2 {
		t.Fatalf("named %d conflicts, want 2 (both blocked Fridays)", len(ac.Conflicts))
	}
	for _, c := range ac.Conflicts {
		if c.Reason != "conflict" {
			t.Errorf("conflict reason=%q, want conflict", c.Reason)
		}
	}
	if got := e.countGroupRows(t, group); got != 0 {
		t.Fatalf("rows after rollback = %d, want 0 (no partial writes)", got)
	}
}

func TestAcademy_ExactReplayShortCircuits(t *testing.T) {
	e := newBlockEnv(t)
	fri := academyDate(90)
	group := uuid.NewString()

	first, _, err := e.repo.CreateAcademyBookings(context.Background(), AcademyBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), AcademyName: "أكاديمية أصلية",
		DaysOfWeek: []int{5}, StartClock: "10:00", EndClock: "11:00",
		StartDate: fri, EndDate: fri.AddDate(0, 0, 14), RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Resubmit the same group id with DELIBERATELY different params → stored rows
	// replay verbatim, the generator never runs.
	replay, replayed, err := e.repo.CreateAcademyBookings(context.Background(), AcademyBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), AcademyName: "مختلفة",
		DaysOfWeek: []int{5, 6}, StartClock: "20:00", EndClock: "22:00",
		StartDate: fri, EndDate: fri.AddDate(0, 0, 28), RecurrenceGroupID: group,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed {
		t.Fatalf("replayed=false, want true")
	}
	if len(replay) != len(first) {
		t.Fatalf("replay rows=%d, want %d", len(replay), len(first))
	}
	for i := range first {
		if replay[i].ID != first[i].ID {
			t.Errorf("replay row %d id=%d, want verbatim %d", i, replay[i].ID, first[i].ID)
		}
	}
}
