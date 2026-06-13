package data

import (
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/timeutil"
)

// am builds an absolute instant from Asia/Amman wall-clock fields (the natural way
// to express a local slot the way a player would pick it).
func am(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, timeutil.Amman())
}

// containedOnDate resolves `windows` for the Amman date of slotStart and tests
// full containment — mirroring exactly what the write-path gate will do.
func containedOnDate(t *testing.T, windows []OperatingWindow, slotStart, slotEnd time.Time) bool {
	t.Helper()
	resolved, err := ResolveWindowsForDate(windows, slotStart)
	if err != nil {
		t.Fatalf("ResolveWindowsForDate: %v", err)
	}
	return SlotContained(slotStart, slotEnd, resolved)
}

func TestSlotContainment(t *testing.T) {
	// Anchor dates with KNOWN weekdays, asserted below so the test fails loudly if
	// the calendar assumption is ever wrong.
	//   2026-06-12 is a Friday, 2026-06-14 is a Sunday.
	friday := am(2026, 6, 12, 0, 0)
	sunday := am(2026, 6, 14, 0, 0)
	if got := int(friday.Weekday()); got != fri {
		t.Fatalf("anchor date 2026-06-12 is weekday %d, expected Friday(%d)", got, fri)
	}
	if got := int(sunday.Weekday()); got != sun {
		t.Fatalf("anchor date 2026-06-14 is weekday %d, expected Sunday(%d)", got, sun)
	}

	tests := []struct {
		name      string
		windows   []OperatingWindow
		slotStart time.Time
		slotEnd   time.Time
		want      bool
	}{
		{
			name:      "slot inside a normal window",
			windows:   []OperatingWindow{win(fri, "09:00", "17:00")},
			slotStart: am(2026, 6, 12, 10, 0),
			slotEnd:   am(2026, 6, 12, 11, 0),
			want:      true,
		},
		{
			name:      "slot exactly fills the window (boundary, half-open)",
			windows:   []OperatingWindow{win(fri, "09:00", "17:00")},
			slotStart: am(2026, 6, 12, 9, 0),
			slotEnd:   am(2026, 6, 12, 17, 0),
			want:      true,
		},
		{
			name:      "slot runs past the window close is rejected",
			windows:   []OperatingWindow{win(fri, "09:00", "17:00")},
			slotStart: am(2026, 6, 12, 16, 30),
			slotEnd:   am(2026, 6, 12, 17, 30),
			want:      false,
		},
		{
			name: "slot straddling a split-shift gap is rejected",
			windows: []OperatingWindow{
				win(fri, "09:00", "12:00"),
				win(fri, "14:00", "18:00"),
			},
			slotStart: am(2026, 6, 12, 11, 30),
			slotEnd:   am(2026, 6, 12, 14, 30),
			want:      false,
		},
		{
			name: "slot fully inside the second shift is accepted",
			windows: []OperatingWindow{
				win(fri, "09:00", "12:00"),
				win(fri, "14:00", "18:00"),
			},
			slotStart: am(2026, 6, 12, 15, 0),
			slotEnd:   am(2026, 6, 12, 16, 0),
			want:      true,
		},
		{
			name:      "early-hours tail of a cross-midnight window, accepted via W-1 (Thu 16->02 covers Fri 01:00-02:00)",
			windows:   []OperatingWindow{win(thu, "16:00", "02:00")},
			slotStart: am(2026, 6, 12, 1, 0), // Friday 01:00
			slotEnd:   am(2026, 6, 12, 2, 0), // Friday 02:00
			want:      true,
		},
		{
			name:      "early-hours slot running past the cross-midnight close is rejected (Fri 01:30-02:30)",
			windows:   []OperatingWindow{win(thu, "16:00", "02:00")},
			slotStart: am(2026, 6, 12, 1, 30),
			slotEnd:   am(2026, 6, 12, 2, 30),
			want:      false,
		},
		{
			name:      "slot that itself crosses midnight, inside one cross-midnight window (Fri 23:30 -> Sat 01:00 in Fri 16->02)",
			windows:   []OperatingWindow{win(fri, "16:00", "02:00")},
			slotStart: am(2026, 6, 12, 23, 30),
			slotEnd:   am(2026, 6, 13, 1, 0),
			want:      true,
		},
		{
			name:      "Sat-night -> Sun-morning wrap, accepted via W-1 (Sat 22->03 covers Sun 01:00-02:00)",
			windows:   []OperatingWindow{win(sat, "22:00", "03:00")},
			slotStart: am(2026, 6, 14, 1, 0), // Sunday 01:00
			slotEnd:   am(2026, 6, 14, 2, 0),
			want:      true,
		},
		{
			name:      "Sunday daytime slot with only a Sat-night window is closed (rejected)",
			windows:   []OperatingWindow{win(sat, "22:00", "03:00")},
			slotStart: am(2026, 6, 14, 10, 0),
			slotEnd:   am(2026, 6, 14, 11, 0),
			want:      false,
		},
		{
			name:      "configured pitch, no window on this date at all -> closed (rejected)",
			windows:   []OperatingWindow{win(mon, "09:00", "17:00")},
			slotStart: am(2026, 6, 12, 10, 0), // Friday
			slotEnd:   am(2026, 6, 12, 11, 0),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containedOnDate(t, tt.windows, tt.slotStart, tt.slotEnd)
			if got != tt.want {
				t.Fatalf("containment = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveCandidateAnchoring proves the resolved intervals are the EXACT
// absolute UTC instants we expect — guarding the anchoring math (Amman is UTC+3),
// including that the W-1 cross-midnight window is anchored to the previous day.
func TestResolveCandidateAnchoring(t *testing.T) {
	// Friday 2026-06-12. Thursday cross-midnight window 16:00->02:00 must resolve to
	// [Thu 16:00 Amman, Fri 02:00 Amman) = [Thu 13:00 UTC, Fri 23:00... ] — compute:
	// Amman is UTC+3, so 16:00 Amman = 13:00 UTC (Thu), 02:00 Amman = 23:00 UTC (Thu).
	windows := []OperatingWindow{win(thu, "16:00", "02:00")}
	resolved, err := ResolveWindowsForDate(windows, am(2026, 6, 12, 0, 0))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved interval, got %d", len(resolved))
	}
	wantStart := time.Date(2026, 6, 11, 13, 0, 0, 0, time.UTC) // Thu 16:00 Amman
	wantEnd := time.Date(2026, 6, 11, 23, 0, 0, 0, time.UTC)   // Fri 02:00 Amman == Thu 23:00 UTC
	if !resolved[0].Start.Equal(wantStart) {
		t.Errorf("start = %s, want %s", resolved[0].Start, wantStart)
	}
	if !resolved[0].End.Equal(wantEnd) {
		t.Errorf("end = %s, want %s", resolved[0].End, wantEnd)
	}
}

// TestResolveEmpty documents the resolver/gate contract: an empty window slice
// resolves to no intervals, so SlotContained is false. The fail-open (open 24/7)
// case is NOT here — it lives in ResolveOpenWindows via hasSchedule, because the
// gate must distinguish "unconfigured" from "configured but closed today".
func TestResolveEmpty(t *testing.T) {
	resolved, err := ResolveWindowsForDate(nil, am(2026, 6, 12, 0, 0))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("expected 0 intervals, got %d", len(resolved))
	}
	if SlotContained(am(2026, 6, 12, 10, 0), am(2026, 6, 12, 11, 0), resolved) {
		t.Fatal("empty windows must not contain any slot")
	}
}
