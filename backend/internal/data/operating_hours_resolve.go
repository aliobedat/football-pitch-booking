package data

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// ResolveOpenWindows — the SINGLE referee for "what is open"
//
// A recurring local-Amman window (e.g. Thu 16:00 → 02:00) is meaningless until it
// is anchored to a concrete calendar date and resolved to ABSOLUTE UTC instants —
// only then can it be compared against the tstzrange occupancy model (which stores
// true points in time). This file does that anchoring once; both the player
// availability read and the booking write-path gate consume its output, so the
// cross-midnight / week-wrap logic lives in exactly one place.
//
// Anchoring is done by CALENDAR DATE, not by modular weekday arithmetic. That is
// the key simplification: the Saturday→Sunday week wrap, month boundaries, and
// year boundaries are all handled by time.Date normalisation on real dates — there
// is no special-case for "weekday 6 → weekday 0". Each window's open/close is
// constructed in the Asia/Amman location and converted to UTC, so the resulting
// instant is exact and immune to any DST rule change (sourced from embedded tzdata).
// ─────────────────────────────────────────────────────────────────────────────

// ConcreteInterval is an open window anchored to a specific date and resolved to
// absolute UTC instants. [Start, End) is half-open, matching the booking_range
// tstzrange '[)' bound inclusivity — so containment math is consistent with the
// occupancy model. End may be on the calendar day AFTER Start when the underlying
// window crosses midnight (Start and End are full unclipped instants — never
// clipped to civil-day bounds).
type ConcreteInterval struct {
	Start time.Time `json:"start"` // absolute UTC, inclusive
	End   time.Time `json:"end"`   // absolute UTC, exclusive
}

// anchorWindow resolves window w against the Amman calendar day whose local
// midnight is dayStart, returning the concrete UTC interval. open is placed on the
// anchor day; close is placed on the SAME day for a normal window, or the NEXT day
// when the window crosses midnight (close <= open), OR exactly 24h later for the
// explicit full-day window (00:00->00:00) — never on the SAME instant as open,
// which would produce a zero-length interval. time.Date normalises the +1 across
// month/year ends, and the .UTC() conversion yields the correct absolute instant
// regardless of the Amman UTC offset in effect on that date.
func anchorWindow(w OperatingWindow, dayStart time.Time) (ConcreteInterval, error) {
	o, err := parseHHMM(w.OpenTime)
	if err != nil {
		return ConcreteInterval{}, err
	}
	c, err := parseHHMM(w.CloseTime)
	if err != nil {
		return ConcreteInterval{}, err
	}
	loc := dayStart.Location()
	y, m, d := dayStart.Date()

	open := time.Date(y, m, d, o/60, o%60, 0, 0, loc)
	if o == c { // full-day window: exactly the next local midnight, 24h later
		return ConcreteInterval{Start: open.UTC(), End: open.AddDate(0, 0, 1).UTC()}, nil
	}
	endDay := d
	if c < o { // crosses midnight → close lands on the following calendar day
		endDay = d + 1
	}
	close := time.Date(y, m, endDay, c/60, c%60, 0, 0, loc)

	return ConcreteInterval{Start: open.UTC(), End: close.UTC()}, nil
}

// ResolveWindowsForDate anchors a pitch's weekly windows to the Amman calendar
// date `ammanDate` (only its Y/M/D are read) and returns every concrete UTC
// interval that touches that date. The candidate set is exactly:
//
//	{ windows on the target weekday }                       anchored to D, plus
//	{ cross-midnight windows on the PREVIOUS weekday (W−1) } anchored to D−1
//
// The W−1 set captures the early-hours TAIL of yesterday's cross-midnight window:
// a 01:00 instant on Friday is covered by Thursday's 16:00→02:00 row, which has no
// Friday row at all. The previous day is found with dayStart.AddDate(0,0,-1) — a
// real calendar date — so when D is Sunday, W−1 is the actual previous Saturday
// (the Sat→Sun wrap needs no special handling).
func ResolveWindowsForDate(windows []OperatingWindow, ammanDate time.Time) ([]ConcreteInterval, error) {
	loc := timeutil.Amman()
	y, m, d := ammanDate.Date()

	dayStart := time.Date(y, m, d, 0, 0, 0, 0, loc) // local midnight of D
	prevStart := dayStart.AddDate(0, 0, -1)         // local midnight of D−1 (real date)

	targetWeekday := int(dayStart.Weekday()) // 0=Sun … 6=Sat
	prevWeekday := int(prevStart.Weekday())  // == (targetWeekday+6)%7, via the real date

	out := make([]ConcreteInterval, 0, len(windows))
	for _, w := range windows {
		switch {
		case w.Weekday == targetWeekday:
			iv, err := anchorWindow(w, dayStart)
			if err != nil {
				return nil, err
			}
			out = append(out, iv)
		case w.Weekday == prevWeekday:
			crosses, err := w.CrossesMidnight()
			if err != nil {
				return nil, err
			}
			if crosses {
				iv, err := anchorWindow(w, prevStart)
				if err != nil {
					return nil, err
				}
				out = append(out, iv)
			}
		}
	}
	return out, nil
}

// SlotContained reports whether the half-open slot [slotStart, slotEnd) is FULLY
// contained within a single open window (containment, not overlap — a slot
// straddling a split-shift gap is not bookable). Inputs are compared as absolute
// instants (normalised to UTC); the windows are unclipped, so a slot that itself
// crosses midnight (e.g. 23:30→01:00 inside 16:00→02:00) is correctly accepted.
//
// Callers MUST resolve `windows` for the Amman calendar date of slotStart
// (ResolveWindowsForDate): that candidate set — target-day windows plus the
// previous day's cross-midnight spill — covers every window that can contain a
// slot beginning on that date, including one whose own end runs past midnight.
func SlotContained(slotStart, slotEnd time.Time, windows []ConcreteInterval) bool {
	s := slotStart.UTC()
	e := slotEnd.UTC()
	for _, iv := range windows {
		// iv.Start <= s && e <= iv.End
		if !iv.Start.After(s) && !e.After(iv.End) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Merged containment gate (WO-24H-CONTINUITY)
//
// A 24/7 pitch stores the explicit full-day row 00:00→00:00, which anchors to a
// window ending EXACTLY at the next local midnight. Monday's window and
// Tuesday's window abut at that instant but ResolveWindowsForDate never puts
// both in one candidate set, and SlotContained requires a SINGLE interval to
// contain the slot — so Mon 23:00 → Tue 00:30 was rejected even though the
// pitch never closes. The functions below fix that for the WRITE-PATH GATE
// ONLY: they anchor every day the slot touches, coalesce abutting/overlapping
// concrete intervals into maximal open runs, and test containment against the
// merged set. Read paths (availability search, GetOpenWindows, day view,
// calendar) deliberately keep consuming ResolveWindowsForDate and are
// behaviorally untouched.
// ─────────────────────────────────────────────────────────────────────────────

// ResolveWindowsForRange anchors the weekly windows to EVERY Amman calendar day
// the half-open range [start, end) touches, plus the day before the first (for
// its cross-midnight / full-day coverage of the early hours), and returns all
// resulting concrete UTC intervals, unmerged. The day count follows the range —
// a multi-day owner booking anchors every day it spans, not a fixed D+1.
func ResolveWindowsForRange(windows []OperatingWindow, start, end time.Time) ([]ConcreteInterval, error) {
	loc := timeutil.Amman()
	firstLocal := start.In(loc)
	lastLocal := end.In(loc)

	y, m, d := firstLocal.Date()
	day := time.Date(y, m, d-1, 0, 0, 0, 0, loc) // D−1 first: yesterday's spill/full-day
	out := make([]ConcreteInterval, 0, len(windows))
	for !day.After(lastLocal) {
		wd := int(day.Weekday())
		for _, w := range windows {
			if w.Weekday != wd {
				continue
			}
			iv, err := anchorWindow(w, day)
			if err != nil {
				return nil, err
			}
			out = append(out, iv)
		}
		day = day.AddDate(0, 0, 1)
	}
	return out, nil
}

// CoalesceIntervals merges abutting or overlapping half-open intervals into
// maximal open runs. Operates ONLY on concrete UTC instants (never on TIME
// values): [Mon00:00,Tue00:00) + [Tue00:00,Wed00:00) → [Mon00:00,Wed00:00).
// Intervals separated by any gap are left distinct. Input order is irrelevant;
// the input slice is not modified.
func CoalesceIntervals(ivs []ConcreteInterval) []ConcreteInterval {
	if len(ivs) <= 1 {
		return append([]ConcreteInterval(nil), ivs...)
	}
	sorted := append([]ConcreteInterval(nil), ivs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	out := []ConcreteInterval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]
		if !iv.Start.After(last.End) { // abuts or overlaps → extend the run
			if iv.End.After(last.End) {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// SlotWithinHours is the merged operating-hours gate: it reports whether
// [slotStart, slotEnd) lies fully inside the coalesced union of the open
// windows anchored across every day the slot touches. This — not the
// single-day SlotContained — is what every WRITE-path hours gate must call.
// Callers keep their own fail-open handling (len(windows)==0 → skip the gate).
func SlotWithinHours(windows []OperatingWindow, slotStart, slotEnd time.Time) (bool, error) {
	resolved, err := ResolveWindowsForRange(windows, slotStart, slotEnd)
	if err != nil {
		return false, err
	}
	return SlotContained(slotStart, slotEnd, CoalesceIntervals(resolved)), nil
}

// HoursShape classifies how a pitch's coverage behaves at the midnight boundary
// of a given Amman calendar date. It is the availability payload's shape signal:
// ONE mutually-exclusive value, because the two facts the client needs — "may a
// booking end past midnight?" and "do the after-midnight slots belong to THIS
// day or to the next one?" — cannot be carried by a single boolean (spill and
// day-bounded would both read false on a continuity flag).
type HoursShape string

const (
	// ShapeContinuous — the day's coverage ends exactly at the next local
	// midnight AND the next day's coverage begins exactly there (the 24/7
	// 00:00→00:00 shape, or two abutting rows). A booking may end past
	// midnight, but the after-midnight slots belong to the NEXT day's own
	// window: a client must NOT render them as this day's tail, or the same
	// concrete slot appears under two day chips.
	ShapeContinuous HoursShape = "continuous"
	// ShapeSpill — a window of THIS day genuinely ends after the next local
	// midnight (e.g. 16:00→01:00) and the next day does not open at midnight.
	// A booking may end past midnight AND the after-midnight slots are this
	// day's tail — the client renders them as such.
	ShapeSpill HoursShape = "spill"
	// ShapeDayBounded — coverage stops at or before the next local midnight
	// with nothing continuing it (09:00→17:00, or a full day followed by a
	// CLOSED day). No booking may end past midnight.
	ShapeDayBounded HoursShape = "day_bounded"
)

// ResolveHoursShape classifies `ammanDate` for this schedule. Windows are
// anchored directly to the date and to the following date — deliberately NOT
// via ResolveWindowsForDate, whose candidate set also carries the PREVIOUS
// day's spill tail, which says nothing about THIS day's midnight boundary.
// Zero windows (unconfigured → fail-open 24/7) is continuous.
//
// The two questions are answered independently and then combined:
//
//   - does THIS day's coverage reach past midnight? (ends exactly at it, or a
//     window runs beyond it)
//   - does the NEXT day's own coverage begin exactly at midnight?
//
// Both true → continuous: the coverage is unbroken across the seam AND the next
// day's window owns the after-midnight slots, so they must not be rendered as
// this day's tail. Only the first → spill: the tail is genuinely this day's.
// Neither → day_bounded. Note the first question is true for an ABUTTING window
// (ends exactly at midnight) and for an OVERLAPPING one (ends after it); both
// forms of continuity count.
func ResolveHoursShape(windows []OperatingWindow, ammanDate time.Time) (HoursShape, error) {
	if len(windows) == 0 {
		return ShapeContinuous, nil // unconfigured → open 24/7
	}
	loc := timeutil.Amman()
	y, m, d := ammanDate.In(loc).Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, loc)
	nextStart := dayStart.AddDate(0, 0, 1)
	nextMidnight := nextStart.UTC()

	var reachesMidnight, spills bool
	todayWD := int(dayStart.Weekday())
	for _, w := range windows {
		if w.Weekday != todayWD {
			continue
		}
		iv, err := anchorWindow(w, dayStart)
		if err != nil {
			return "", err
		}
		switch {
		case iv.End.Equal(nextMidnight): // abuts the seam
			reachesMidnight = true
		case iv.End.After(nextMidnight): // runs past it
			reachesMidnight = true
			spills = true
		}
	}

	var startsAtMidnight bool
	nextWD := int(nextStart.Weekday())
	for _, w := range windows {
		if w.Weekday != nextWD {
			continue
		}
		iv, err := anchorWindow(w, nextStart)
		if err != nil {
			return "", err
		}
		if iv.Start.Equal(nextMidnight) {
			startsAtMidnight = true
		}
	}

	switch {
	case reachesMidnight && startsAtMidnight:
		return ShapeContinuous, nil
	case spills:
		return ShapeSpill, nil
	default:
		return ShapeDayBounded, nil
	}
}

// MaxPlayerBookingDuration is the upper bound on a single PLAYER booking's
// length. It lives here, in the lowest layer, because three places must agree
// on it and drift between them is a correctness bug, not a style issue:
//
//   - the player CreateBooking validation (rejects anything longer),
//   - the continuation ceiling served on the availability payload,
//   - the booked-slots lookahead (a day's payload must reveal any booking a
//     same-day start could still collide with, which reaches exactly this far
//     past the day's end).
//
// Owner/admin write paths deliberately have NO max-duration cap; this bound is
// player-path only.
const MaxPlayerBookingDuration = 120 * time.Minute

// DateHours bundles the per-date signals the availability payload carries. The
// served open_windows stay DAY-shaped; these two fields state what a client
// cannot correctly infer from them.
type DateHours struct {
	Shape HoursShape
	// ContinuationMinutes is how far past the next local midnight a booking may
	// validly run on this date. ZERO unless Shape is continuous. A client may
	// extend ONLY the window whose end reaches that midnight, and only by this
	// many minutes.
	ContinuationMinutes int
}

// ResolveContinuationMinutes reports how far past the next local midnight this
// date's coverage actually continues, capped at `ceiling` (the caller's booking
// ceiling). It answers the question the SHAPE alone cannot: `continuous` proves
// the next day's coverage ABUTS this one, but says nothing about how LONG that
// next-day coverage runs.
//
// Returns 0 for every non-continuous shape and for an unconfigured pitch (zero
// windows) — the latter is fail-open server-side but deliberately advertises no
// continuation, keeping clients day-bounded there.
//
// The run is measured with the SAME machinery the write-path gate uses —
// ResolveWindowsForRange + CoalesceIntervals — so the advertised continuation
// can never exceed what SlotWithinHours would accept. Measuring from the
// coalesced run (not from a single next-day row) is what makes a split next-day
// schedule (00:00→01:00 followed by 01:00→03:00) report the full 120 rather
// than a premature 60.
// An UNCONFIGURED pitch (zero windows) reports 0 structurally, with no special
// case: no windows resolve to no intervals, so no run covers the seam. The
// server fails OPEN for such a pitch, but advertising no continuation keeps
// clients day-bounded there by contract rather than by client-side exception.
func ResolveContinuationMinutes(windows []OperatingWindow, ammanDate time.Time, ceiling time.Duration) (int, error) {
	shape, err := ResolveHoursShape(windows, ammanDate)
	if err != nil {
		return 0, err
	}
	if shape != ShapeContinuous {
		return 0, nil
	}
	if ceiling <= 0 {
		return 0, nil
	}

	loc := timeutil.Amman()
	y, m, d := ammanDate.In(loc).Date()
	nextMidnight := time.Date(y, m, d+1, 0, 0, 0, 0, loc)

	// Probe the ceiling-wide span past the seam. ResolveWindowsForRange anchors
	// WHOLE DAYS from the day before the range start through the day of its
	// end, so (a) this date's own midnight-abutting window is present and
	// coalesces across the seam, and (b) the probe is wide enough for ANY
	// ceiling: if coverage really runs through nm+ceiling, the last anchored
	// day's window ends at or after nm+ceiling, so the run is never truncated
	// below the cap. Pinned by the multi-day-ceiling test.
	resolved, err := ResolveWindowsForRange(windows, nextMidnight, nextMidnight.Add(ceiling))
	if err != nil {
		return 0, err
	}
	nm := nextMidnight.UTC()
	for _, iv := range CoalesceIntervals(resolved) {
		// The run covering the seam instant itself.
		if !iv.Start.After(nm) && iv.End.After(nm) {
			run := min(iv.End.Sub(nm), ceiling)
			return int(run / time.Minute), nil
		}
	}
	return 0, nil
}

// ResolveDateHours computes both per-date signals in one pass. `ceiling` is the
// caller's booking ceiling (the player path's maxBookingDuration).
func ResolveDateHours(windows []OperatingWindow, ammanDate time.Time, ceiling time.Duration) (DateHours, error) {
	shape, err := ResolveHoursShape(windows, ammanDate)
	if err != nil {
		return DateHours{}, err
	}
	if shape != ShapeContinuous {
		return DateHours{Shape: shape}, nil
	}
	cont, err := ResolveContinuationMinutes(windows, ammanDate, ceiling)
	if err != nil {
		return DateHours{}, err
	}
	return DateHours{Shape: shape, ContinuationMinutes: cont}, nil
}

// ResolveOpenWindows loads a pitch's weekly schedule and resolves it to the
// concrete UTC intervals touching the Amman calendar date `ammanDate`. It returns
// (intervals, hasSchedule, error):
//
//   - hasSchedule == false means the pitch has ZERO configured windows → it is
//     treated as OPEN 24/7 (the PR's fail-open-on-unconfigured decision). Callers
//     MUST branch on this and skip the containment gate, NOT infer "closed" from an
//     empty interval slice — an empty slice also occurs for a configured pitch with
//     no window on the requested date (which IS closed that day).
//   - hasSchedule == true with an empty slice means "configured, but closed on
//     this date".
//
// ⚠ DO NOT USE THIS FOR A CONTAINMENT GATE. As of WO-24H-CONTINUITY this method
// has ZERO production callers — the booking-extension handler was its last one.
// It is the UNMERGED, SINGLE-DAY resolver: its candidate set is one calendar day
// (plus the previous day's cross-midnight spill), and containment against it can
// only ever succeed inside ONE interval. On a continuously-open pitch (the
// explicit full-day row 00:00→00:00) that rejects any slot crossing midnight,
// because the abutting next-day window is absent from the set and two abutting
// windows jointly covering a slot still fail single-interval containment — the
// exact production bug WO-24H-CONTINUITY fixed. A new gate caller reaching for
// this name would silently reintroduce it.
//
// Use SlotWithinHours (windows already loaded, e.g. under a tx lock) or
// SlotWithinOpenHours (loads them for a pitch id) instead: both resolve every
// day the slot touches and coalesce abutting windows first.
//
// Kept, not deleted, so the WO's additions-only proof over this file holds —
// read paths depend on anchorWindow / ResolveWindowsForDate / SlotContained
// being byte-unchanged. Delete or rename it under its own WO
// (docs/followups/operating-hours-gate-fail-open.md).
func (m *PitchModel) ResolveOpenWindows(ctx context.Context, pitchID int, ammanDate time.Time) (intervals []ConcreteInterval, hasSchedule bool, err error) {
	windows, err := m.GetOperatingHours(ctx, pitchID)
	if err != nil {
		return nil, false, fmt.Errorf("ResolveOpenWindows: %w", err)
	}
	if len(windows) == 0 {
		return nil, false, nil // unconfigured → open 24/7
	}
	resolved, err := ResolveWindowsForDate(windows, ammanDate)
	if err != nil {
		return nil, true, err
	}
	return resolved, true, nil
}

// SlotWithinOpenHours loads the pitch's schedule and runs the MERGED
// containment gate (SlotWithinHours) over [slotStart, slotEnd). Returns
// (contained, hasSchedule, err); hasSchedule==false means unconfigured →
// fail-open (contained is returned true in that case, callers need no extra
// branch). This is the write-path gate entry point for handlers that only hold
// a pitch id (the extend sheet); the repository create paths call
// SlotWithinHours directly on windows they load under their own tx lock.
func (m *PitchModel) SlotWithinOpenHours(ctx context.Context, pitchID int, slotStart, slotEnd time.Time) (contained bool, hasSchedule bool, err error) {
	windows, err := m.GetOperatingHours(ctx, pitchID)
	if err != nil {
		return false, false, fmt.Errorf("SlotWithinOpenHours: %w", err)
	}
	if len(windows) == 0 {
		return true, false, nil // unconfigured → open 24/7
	}
	ok, err := SlotWithinHours(windows, slotStart, slotEnd)
	if err != nil {
		return false, true, err
	}
	return ok, true, nil
}
