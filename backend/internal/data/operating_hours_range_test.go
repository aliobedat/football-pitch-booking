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

// rows builds a schedule from (weekday, open, close) triples.
func rows(specs ...[3]string) []OperatingWindow {
	out := make([]OperatingWindow, 0, len(specs))
	for _, s := range specs {
		wd := map[string]int{"sun": sun, "mon": mon, "tue": tue, "wed": wed, "thu": thu, "fri": fri, "sat": sat}[s[0]]
		out = append(out, win(wd, s[1], s[2]))
	}
	return out
}

// TestResolveContinuationMinutes pins the value the availability payload serves.
// The shape alone cannot carry this: `continuous` proves the next day's coverage
// ABUTS this date's, but not how LONG it runs. Extending by a flat 120 on a
// short next-day window advertises hours the server rejects — the defect this
// signal exists to prevent.
func TestResolveContinuationMinutes(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)
	if int(monday.Weekday()) != mon {
		t.Fatalf("anchor 2026-07-27 is weekday %d, want Monday(%d)", int(monday.Weekday()), mon)
	}
	ceiling := 120 * time.Minute

	cases := []struct {
		name    string
		windows []OperatingWindow
		want    int
	}{
		{"24/7 (prod pitches 1+2) → full ceiling", fullWeek("00:00", "00:00"), 120},
		{"split shift, evening row abuts midnight, next day 00:00→08:00",
			rows([3]string{"mon", "09:00", "12:00"}, [3]string{"mon", "20:00", "00:00"}, [3]string{"tue", "00:00", "08:00"}), 120},
		{"SHORT next day 00:00→01:00 → only 60",
			rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "01:00"}), 60},
		{"very short next day 00:00→00:30 → only 30",
			rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "00:30"}), 30},
		{"SPLIT next day 00:00→01:00 + 01:00→03:00 coalesces → full ceiling",
			rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "01:00"}, [3]string{"tue", "01:00", "03:00"}), 120},
		{"next day has a GAP after 01:00 (01:30→05:00) → still only 60",
			rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "01:00"}, [3]string{"tue", "01:30", "05:00"}), 60},
		{"spill 16:00→01:00 → 0 (tail already in served windows)", fullWeek("16:00", "01:00"), 0},
		{"day-bounded 09:00→17:00 → 0", fullWeek("09:00", "17:00"), 0},
		{"continuous-then-CLOSED next day → 0", weekExcept("00:00", "00:00", tue), 0},
		{"unconfigured (fail-open) → 0", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveContinuationMinutes(tc.windows, monday, ceiling)
			if err != nil {
				t.Fatalf("ResolveContinuationMinutes: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
		})
	}
}

// TestContinuation_SplitShiftSeamOnly is the regression test for the reviewed
// defect: a split-shift day classified `continuous` must NOT license extending
// its MORNING window.
//
// The next day deliberately opens 00:00→01:00, so the correct continuation is
// 60 — NOT the 120 ceiling. That choice is what gives this test teeth: an
// implementation that returns a hardcoded 120, or that measures anything other
// than the seam run, FAILS here. (An earlier version of this test used a
// next-day window longer than the ceiling, so every assertion held even against
// a hardcoded 120 — it proved nothing about this function.)
func TestContinuation_SplitShiftSeamOnly(t *testing.T) {
	w := rows(
		[3]string{"mon", "09:00", "12:00"}, // morning shift — NOT seam-eligible
		[3]string{"mon", "20:00", "00:00"}, // evening shift — reaches the seam
		[3]string{"tue", "00:00", "01:00"}, // next-day run is only 60 minutes
	)
	monday := am(2026, 7, 27, 0, 0)

	cont, err := ResolveContinuationMinutes(w, monday, 120*time.Minute)
	if err != nil {
		t.Fatalf("ResolveContinuationMinutes: %v", err)
	}
	if cont != 60 {
		t.Fatalf("continuation = %d, want 60 — a hardcoded ceiling or a non-seam measurement gives 120", cont)
	}

	// The morning window earns NO continuation: 11:30→12:30 runs past its
	// 12:00 close into a real closure, and the gate rejects it.
	if within(t, w, am(2026, 7, 27, 11, 30), am(2026, 7, 27, 12, 30)) {
		t.Fatal("11:30→12:30 crosses the midday closure and must be REJECTED")
	}
	// The seam window earns exactly the served 60, and not one minute more.
	if !within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 0, 30)) {
		t.Fatal("23:30→00:30 is inside the seam run and must be accepted")
	}
	if within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 1, 30)) {
		t.Fatal("23:30→01:30 exceeds the 60-minute seam run and must be REJECTED")
	}
}

// TestContinuation_CeilingGuard pins the non-positive-ceiling guard. DELETING
// it makes a negative ceiling produce a NEGATIVE continuation (min(run, -1m) →
// -1), which this catches. Note the `<= 0` vs `< 0` refinement is deliberately
// NOT pinned: at exactly 0 the guard and min(run, 0) agree, so the two spellings
// are behaviourally identical and no test can distinguish them.
func TestContinuation_CeilingGuard(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)

	schedules := map[string][]OperatingWindow{
		// 24/7: with the guard removed the probe collapses and no run covers
		// the seam, so this alone would NOT catch the deletion.
		"24/7": fullWeek("00:00", "00:00"),
		// A date that both SPILLS and abuts. This is the one with teeth: only
		// day D gets anchored by a collapsed probe, and D's own spill window
		// still covers the seam — so without the guard, min(2h, -1m) yields a
		// NEGATIVE continuation.
		"spill+continuous": rows(
			[3]string{"mon", "16:00", "02:00"},
			[3]string{"tue", "00:00", "00:30"},
		),
	}

	for name, w := range schedules {
		for _, ceiling := range []time.Duration{0, -1 * time.Minute, -48 * time.Hour} {
			got, err := ResolveContinuationMinutes(w, monday, ceiling)
			if err != nil {
				t.Fatalf("%s ceiling=%s: %v", name, ceiling, err)
			}
			if got != 0 {
				t.Fatalf("%s ceiling=%s: continuation = %d, want 0 (a non-positive ceiling can never license a continuation, and must never go negative)", name, ceiling, got)
			}
		}
	}
}

// TestContinuation_ProbeWidthFollowsCeiling pins the probe span to the CEILING.
// The probe is [seam, seam+ceiling] and ResolveWindowsForRange anchors whole
// days across it, so the measurable run always reaches at least the ceiling.
//
// The ceiling here is 72h on purpose. A fixed +24h probe anchors days D…D+2,
// whose coalesced run already ends 48h past the seam — so it satisfies any
// ceiling ≤ 48h, and a 48-hour assertion PASSES against that mutation (verified
// by mutation testing). 72h is the smallest round ceiling that forces the probe
// to widen, making this the assertion that actually fails when the span stops
// following the ceiling.
func TestContinuation_ProbeWidthFollowsCeiling(t *testing.T) {
	w := fullWeek("00:00", "00:00") // open forever
	monday := am(2026, 7, 27, 0, 0)

	got, err := ResolveContinuationMinutes(w, monday, 72*time.Hour)
	if err != nil {
		t.Fatalf("ResolveContinuationMinutes: %v", err)
	}
	if got != 4320 {
		t.Fatalf("continuation = %d, want 4320 — the probe must span the whole ceiling, not a fixed day", got)
	}
	// And the gate agrees: a 72h run from the seam really is contained.
	if !within(t, w, am(2026, 7, 28, 0, 0), am(2026, 7, 31, 0, 0)) {
		t.Fatal("a 72h booking from the seam must be accepted on a 24/7 pitch")
	}
}

// TestContinuation_UnconfiguredIsStructural documents WHY the unconfigured case
// returns 0: not a special-cased early return, but because zero windows resolve
// to zero intervals, so no run covers the seam. There is deliberately no guard
// to pin — a guard here would be unreachable code, which this WO has already
// paid for once (see the ResolveOpenWindows warning).
func TestContinuation_UnconfiguredIsStructural(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)
	// Shape is continuous (the server's fail-open) …
	shape, err := ResolveHoursShape(nil, monday)
	if err != nil {
		t.Fatalf("ResolveHoursShape: %v", err)
	}
	if shape != ShapeContinuous {
		t.Fatalf("unconfigured shape = %q, want %q", shape, ShapeContinuous)
	}
	// … yet no continuation is advertised, for any ceiling.
	for _, ceiling := range []time.Duration{30 * time.Minute, 120 * time.Minute, 48 * time.Hour} {
		got, err := ResolveContinuationMinutes(nil, monday, ceiling)
		if err != nil {
			t.Fatalf("ceiling=%s: %v", ceiling, err)
		}
		if got != 0 {
			t.Fatalf("ceiling=%s: unconfigured continuation = %d, want 0", ceiling, got)
		}
	}
}

// TestContinuation_ShortNextDayRejectedBeyondRun proves the served 60 is not
// merely a smaller number but the ACTUAL limit the gate enforces.
func TestContinuation_ShortNextDayRejectedBeyondRun(t *testing.T) {
	w := rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "01:00"})
	monday := am(2026, 7, 27, 0, 0)

	cont, err := ResolveContinuationMinutes(w, monday, 120*time.Minute)
	if err != nil {
		t.Fatalf("ResolveContinuationMinutes: %v", err)
	}
	if cont != 60 {
		t.Fatalf("continuation = %d, want 60", cont)
	}
	// Exactly the served continuation is accepted …
	if !within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 0, 30)) {
		t.Fatal("23:30→00:30 is within the 60-minute continuation and must be accepted")
	}
	if !within(t, w, am(2026, 7, 28, 0, 0), am(2026, 7, 28, 1, 0)) {
		t.Fatal("00:00→01:00 fills the next-day run exactly and must be accepted")
	}
	// … and one minute beyond it is not. A flat 120 would have offered this.
	if within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 1, 30)) {
		t.Fatal("23:30→01:30 exceeds the 60-minute continuation and must be REJECTED")
	}
}

// TestContinuation_MultipleRowsEndingAtMidnight documents the declared edge:
// when SEVERAL rows of the same date end exactly at the seam, the continuation
// is a property of the seam, not of any one row — one value, applying to every
// such window. Overlapping rows cannot disagree, because any two windows ending
// at the same instant necessarily overlap.
func TestContinuation_MultipleRowsEndingAtMidnight(t *testing.T) {
	w := rows(
		[3]string{"mon", "20:00", "00:00"},
		[3]string{"mon", "22:00", "00:00"}, // second row, same seam
		[3]string{"tue", "00:00", "01:00"},
	)
	monday := am(2026, 7, 27, 0, 0)

	got, err := ResolveContinuationMinutes(w, monday, 120*time.Minute)
	if err != nil {
		t.Fatalf("ResolveContinuationMinutes: %v", err)
	}
	if got != 60 {
		t.Fatalf("continuation = %d, want 60 (bounded by the next-day run, not by row count)", got)
	}
	// Both rows reach the seam, so a booking from either is accepted up to it.
	if !within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 0, 30)) {
		t.Fatal("23:30→00:30 must be accepted")
	}
	if within(t, w, am(2026, 7, 27, 23, 30), am(2026, 7, 28, 1, 30)) {
		t.Fatal("23:30→01:30 exceeds the run and must be REJECTED")
	}
}

// TestResolveDateHours_BundlesBothSignals checks the combined accessor agrees
// with the individual ones and never reports a continuation off a non-continuous
// shape.
func TestResolveDateHours_BundlesBothSignals(t *testing.T) {
	monday := am(2026, 7, 27, 0, 0)
	ceiling := 120 * time.Minute
	for _, tc := range []struct {
		name      string
		windows   []OperatingWindow
		wantShape HoursShape
		wantCont  int
	}{
		{"24/7", fullWeek("00:00", "00:00"), ShapeContinuous, 120},
		{"short next day", rows([3]string{"mon", "16:00", "00:00"}, [3]string{"tue", "00:00", "01:00"}), ShapeContinuous, 60},
		{"spill", fullWeek("16:00", "01:00"), ShapeSpill, 0},
		{"day bounded", fullWeek("09:00", "17:00"), ShapeDayBounded, 0},
		{"unconfigured", nil, ShapeContinuous, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDateHours(tc.windows, monday, ceiling)
			if err != nil {
				t.Fatalf("ResolveDateHours: %v", err)
			}
			if got.Shape != tc.wantShape || got.ContinuationMinutes != tc.wantCont {
				t.Fatalf("got {%q, %d}, want {%q, %d}", got.Shape, got.ContinuationMinutes, tc.wantShape, tc.wantCont)
			}
		})
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
