package data

// WO-24H-CONTINUITY unit tests: the merged containment gate
// (ResolveWindowsForRange + CoalesceIntervals + SlotWithinHours) and the
// availability continuity signal (ContinuesIntoNextDay). Anchor week:
// 2026-07-26 (Sun) … 2026-08-01 (Sat), asserted below.

import (
	"testing"
	"time"
)

// fullWeek returns the same open/close row on all seven weekdays.
func fullWeek(open, close string) []OperatingWindow {
	out := make([]OperatingWindow, 0, 7)
	for wd := range 7 {
		out = append(out, win(wd, open, close))
	}
	return out
}

// weekExcept returns the row on every weekday except the listed ones.
func weekExcept(open, close string, skip ...int) []OperatingWindow {
	skipSet := map[int]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	out := make([]OperatingWindow, 0, 7)
	for wd := range 7 {
		if !skipSet[wd] {
			out = append(out, win(wd, open, close))
		}
	}
	return out
}

func within(t *testing.T, windows []OperatingWindow, s, e time.Time) bool {
	t.Helper()
	ok, err := SlotWithinHours(windows, s, e)
	if err != nil {
		t.Fatalf("SlotWithinHours: %v", err)
	}
	return ok
}

func TestMergedGate(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)
	if got := int(monday.Weekday()); got != mon {
		t.Fatalf("anchor date 2026-07-27 is weekday %d, expected Monday(%d)", got, mon)
	}

	t.Run("24/7 pitch: 23:00→00:30 next day accepted (the reported bug)", func(t *testing.T) {
		w := fullWeek("00:00", "00:00")
		if !within(t, w, am(2026, 7, 27, 23, 0), am(2026, 7, 28, 0, 30)) {
			t.Fatal("Mon 23:00 → Tue 00:30 must be accepted on a 24/7 pitch")
		}
	})

	t.Run("24/7 day followed by a CLOSED day: spill rejected, exact-midnight end kept", func(t *testing.T) {
		w := weekExcept("00:00", "00:00", tue) // Tuesday closed → no abutment at Tue 00:00
		if within(t, w, am(2026, 7, 27, 23, 0), am(2026, 7, 28, 0, 30)) {
			t.Fatal("Mon 23:00 → Tue 00:30 must be rejected when Tuesday is closed")
		}
		if !within(t, w, am(2026, 7, 27, 23, 0), am(2026, 7, 28, 0, 0)) {
			t.Fatal("Mon 23:00 → Tue 00:00 (ends exactly at midnight) must stay accepted")
		}
	})

	t.Run("spill pitch 16:00→01:00: behavior unchanged", func(t *testing.T) {
		w := fullWeek("16:00", "01:00")
		if !within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 1, 0)) {
			t.Fatal("23:30 → 01:00 inside the spill window must be accepted")
		}
		if !within(t, w, am(2026, 7, 28, 0, 30), am(2026, 7, 28, 1, 0)) {
			t.Fatal("00:30 start in Monday's tail must be accepted")
		}
		if within(t, w, am(2026, 7, 28, 0, 30), am(2026, 7, 28, 1, 30)) {
			t.Fatal("a slot ending 01:30 (past the 01:00 close) must stay rejected")
		}
	})

	t.Run("gap pitch: a range spanning a real closing gap rejected (no merge)", func(t *testing.T) {
		w := fullWeek("09:00", "17:00")
		if within(t, w, am(2026, 7, 27, 16, 0), am(2026, 7, 28, 10, 0)) {
			t.Fatal("Mon 16:00 → Tue 10:00 spans the 17:00→09:00 closed gap and must be rejected")
		}
		// Spill pitch closing gap: Tue 00:30 (Mon's tail) → Tue 16:30 crosses
		// the 01:00→16:00 closed gap.
		spill := fullWeek("16:00", "01:00")
		if within(t, spill, am(2026, 7, 28, 0, 30), am(2026, 7, 28, 16, 30)) {
			t.Fatal("a range across the daytime closed gap must be rejected")
		}
	})

	t.Run("multi-day owner span on a 24/7 pitch accepted (range anchoring, not D+1)", func(t *testing.T) {
		w := fullWeek("00:00", "00:00")
		// Mon 23:00 → Thu 05:00: touches 4 calendar days; a fixed D+1 candidate
		// set cannot cover it.
		if !within(t, w, am(2026, 7, 27, 23, 0), am(2026, 7, 30, 5, 0)) {
			t.Fatal("Mon 23:00 → Thu 05:00 must be accepted on a 24/7 pitch")
		}
		// Same span with one closed mid-day breaks the run.
		holed := weekExcept("00:00", "00:00", wed)
		if within(t, holed, am(2026, 7, 27, 23, 0), am(2026, 7, 30, 5, 0)) {
			t.Fatal("the span must be rejected when Wednesday is closed mid-run")
		}
	})

	t.Run("abutting split rows merge: 16:00→00:00 + 00:00→02:00", func(t *testing.T) {
		w := append(fullWeek("16:00", "00:00"), fullWeek("00:00", "02:00")...)
		if !within(t, w, am(2026, 7, 27, 23, 0), am(2026, 7, 28, 1, 0)) {
			t.Fatal("23:00 → 01:00 across the midnight seam of two abutting rows must be accepted")
		}
		if within(t, w, am(2026, 7, 28, 1, 30), am(2026, 7, 28, 2, 30)) {
			t.Fatal("a slot ending past the 02:00 close must stay rejected")
		}
	})
}

func TestCoalesceIntervals(t *testing.T) {
	iv := func(s, e time.Time) ConcreteInterval { return ConcreteInterval{Start: s.UTC(), End: e.UTC()} }
	a := iv(am(2026, 7, 27, 0, 0), am(2026, 7, 28, 0, 0))  // Mon full day
	b := iv(am(2026, 7, 28, 0, 0), am(2026, 7, 29, 0, 0))  // Tue full day (abuts a)
	c := iv(am(2026, 7, 29, 16, 0), am(2026, 7, 30, 1, 0)) // Wed evening (gap after b)

	got := CoalesceIntervals([]ConcreteInterval{c, b, a}) // unsorted input
	if len(got) != 2 {
		t.Fatalf("expected 2 merged intervals, got %d: %+v", len(got), got)
	}
	if !got[0].Start.Equal(a.Start) || !got[0].End.Equal(b.End) {
		t.Fatalf("abutting Mon+Tue must merge to [Mon00:00, Wed00:00), got %+v", got[0])
	}
	if !got[1].Start.Equal(c.Start) || !got[1].End.Equal(c.End) {
		t.Fatalf("the gapped interval must survive unmerged, got %+v", got[1])
	}

	// Overlap (not just abutment) also merges.
	d := iv(am(2026, 7, 27, 10, 0), am(2026, 7, 27, 12, 0))
	e := iv(am(2026, 7, 27, 11, 0), am(2026, 7, 27, 13, 0))
	got = CoalesceIntervals([]ConcreteInterval{d, e})
	if len(got) != 1 || !got[0].End.Equal(e.End) {
		t.Fatalf("overlapping intervals must merge, got %+v", got)
	}

	// A contained interval must not shrink the run's end.
	f := iv(am(2026, 7, 27, 10, 30), am(2026, 7, 27, 11, 0))
	got = CoalesceIntervals([]ConcreteInterval{d, f})
	if len(got) != 1 || !got[0].End.Equal(d.End) {
		t.Fatalf("a contained interval must not shorten the run, got %+v", got)
	}
}

// TestResolveHoursShape pins the three-way classification. The critical pair is
// spill vs day_bounded: a single continuity boolean reads FALSE for both, yet
// the client must render the after-midnight group for spill and suppress it for
// the other two (D-D: rendering a continuous pitch's 00:00 slot as the previous
// day's tail double-renders the same concrete slot under two day chips).
func TestResolveHoursShape(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)

	cases := []struct {
		name    string
		windows []OperatingWindow
		want    HoursShape
	}{
		// ── the three PRODUCTION shapes ──────────────────────────────────────
		{"prod pitches 1+2: 24/7 full-day rows all week", fullWeek("00:00", "00:00"), ShapeContinuous},
		{"prod pitch 3: spill 16:00→01:00", fullWeek("16:00", "01:00"), ShapeSpill},
		{"full day followed by a CLOSED day", weekExcept("00:00", "00:00", tue), ShapeDayBounded},
		// ── other shapes ─────────────────────────────────────────────────────
		{"normal day-bounded 09:00→17:00", fullWeek("09:00", "17:00"), ShapeDayBounded},
		{"abutting split rows 16:00→00:00 + next-day 00:00→02:00",
			append(fullWeek("16:00", "00:00"), fullWeek("00:00", "02:00")...), ShapeContinuous},
		{"unconfigured (fail-open 24/7)", nil, ShapeContinuous},
		{"spill row whose next day opens at midnight → continuous wins",
			append(fullWeek("16:00", "01:00"), fullWeek("00:00", "03:00")...), ShapeContinuous},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveHoursShape(tc.windows, monday)
			if err != nil {
				t.Fatalf("ResolveHoursShape: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestResolveHoursShape_PerDay proves the classification is per-DATE, not a
// property of the schedule: the day BEFORE a closed day is day_bounded while
// every other day of the same schedule is continuous.
func TestResolveHoursShape_PerDay(t *testing.T) {
	w := weekExcept("00:00", "00:00", tue) // Tuesday closed
	monday := am(2026, 7, 27, 0, 0)        // Mon → next day closed
	wednesday := am(2026, 7, 29, 0, 0)     // Wed → Thu open, abuts

	if got, _ := ResolveHoursShape(w, monday); got != ShapeDayBounded {
		t.Fatalf("Monday (next day closed) = %q, want day_bounded", got)
	}
	if got, _ := ResolveHoursShape(w, wednesday); got != ShapeContinuous {
		t.Fatalf("Wednesday (next day open at midnight) = %q, want continuous", got)
	}
}

// TestMergedGate_ReadPathsUnmerged is the D-B regression proof at the unit
// level: ResolveWindowsForDate — the resolver EVERY read path (SearchAvailability,
// GetOpenWindows, day view, calendar) consumes — must keep returning DAY-shaped
// windows for the 24/7 shape, never a merged multi-day run.
func TestMergedGate_ReadPathsUnmerged(t *testing.T) {
	w := fullWeek("00:00", "00:00")
	monday := am(2026, 7, 27, 0, 0)
	resolved, err := ResolveWindowsForDate(w, monday)
	if err != nil {
		t.Fatalf("ResolveWindowsForDate: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected exactly 1 day-shaped window, got %d: %+v", len(resolved), resolved)
	}
	wantStart := monday.UTC()
	wantEnd := am(2026, 7, 28, 0, 0).UTC()
	if !resolved[0].Start.Equal(wantStart) || !resolved[0].End.Equal(wantEnd) {
		t.Fatalf("day-shaped window must be [Mon00:00, Tue00:00) exactly, got %+v", resolved[0])
	}
}
