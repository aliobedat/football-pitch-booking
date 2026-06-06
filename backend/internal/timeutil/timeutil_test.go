package timeutil

import (
	"testing"
	"time"
)

// TestTzdataEmbedded confirms Asia/Amman resolves — which it must, since the
// package blank-imports time/tzdata. In a scratch container without this embed,
// LoadLocation would fall back to UTC and every civil-day computation would skew.
func TestTzdataEmbedded(t *testing.T) {
	if Amman() == nil {
		t.Fatal("Amman location is nil")
	}
	if Amman().String() != AmmanZone {
		t.Fatalf("zone = %q, want %q", Amman().String(), AmmanZone)
	}
}

// TestOffsetIsPlusThree_FromTzDatabase checks Jordan's current year-round UTC+3
// (DST abolished ~2022) — read FROM the tz database for both a winter and a
// summer date, never hardcoded. If Jordan ever restores DST, the tz db changes
// and this test documents the expectation to revisit.
func TestOffsetIsPlusThree_FromTzDatabase(t *testing.T) {
	for _, d := range []time.Time{
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), // winter
		time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), // summer
	} {
		_, offset := InAmman(d).Zone()
		if offset != 3*60*60 {
			t.Errorf("%s: offset = %ds, want +10800 (UTC+3)", d.Format("2006-01"), offset)
		}
	}
}

// TestAmmanDayBoundsUTC pins the absolute UTC instants bounding an Amman civil
// day: midnight Amman is 21:00 the previous day in UTC (UTC+3).
func TestAmmanDayBoundsUTC(t *testing.T) {
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	start, end := AmmanDayBoundsUTC(date)

	wantStart := time.Date(2026, 6, 14, 21, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 15, 21, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("start = %s, want %s", start.Format(time.RFC3339), wantStart.Format(time.RFC3339))
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end = %s, want %s", end.Format(time.RFC3339), wantEnd.Format(time.RFC3339))
	}
	if got := end.Sub(start); got != 24*time.Hour {
		t.Errorf("day length = %s, want 24h (no DST in Jordan)", got)
	}
}

// TestAmmanDayBoundsUTC_HostTZIndependent is the core acceptance check: the bounds
// must NOT depend on the process's local zone (TZ=UTC vs TZ=America/New_York must
// agree). We simulate both by swapping time.Local and asserting an identical
// result, because the helper uses an explicit IANA location, never time.Local.
func TestAmmanDayBoundsUTC_HostTZIndependent(t *testing.T) {
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	wantStart, wantEnd := AmmanDayBoundsUTC(date)

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load NY: %v", err)
	}
	for _, loc := range []*time.Location{time.UTC, ny} {
		time.Local = loc
		start, end := AmmanDayBoundsUTC(date)
		if !start.Equal(wantStart) || !end.Equal(wantEnd) {
			t.Errorf("under TZ=%s: bounds (%s,%s), want (%s,%s)",
				loc, start.Format(time.RFC3339), end.Format(time.RFC3339),
				wantStart.Format(time.RFC3339), wantEnd.Format(time.RFC3339))
		}
	}
}

// TestNearMidnightCivilDay proves a booking near midnight Amman is attributed to
// the correct CIVIL day — the bug UTC day bounds would cause. A 00:30 Amman slot
// on the 15th is 21:30 UTC on the 14th: it belongs to the 15th's bounds, not the
// 14th's.
func TestNearMidnightCivilDay(t *testing.T) {
	d15 := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	d14 := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	// 00:30 on 2026-06-15 in Amman, expressed as an absolute UTC instant.
	slot := time.Date(2026, 6, 15, 0, 30, 0, 0, Amman()).UTC()

	start15, end15 := AmmanDayBoundsUTC(d15)
	if slot.Before(start15) || !slot.Before(end15) {
		t.Errorf("00:30 Amman slot %s not within civil day 15 [%s,%s)",
			slot.Format(time.RFC3339), start15.Format(time.RFC3339), end15.Format(time.RFC3339))
	}

	start14, end14 := AmmanDayBoundsUTC(d14)
	inDay14 := !slot.Before(start14) && slot.Before(end14)
	if inDay14 {
		t.Errorf("00:30 Amman slot %s wrongly within civil day 14 [%s,%s)",
			slot.Format(time.RFC3339), start14.Format(time.RFC3339), end14.Format(time.RFC3339))
	}
}
