package data

import (
	"context"
	"fmt"
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
// This is the only method that both the availability read and the write-path gate
// should call, keeping the resolution logic single-sourced.
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
