package repository

import (
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ammanWeekStartUTC must return 00:00 Amman of the most recent Saturday (the start
// of Jordan's business week, weekend being Fri–Sat) for any instant in the week,
// expressed as a UTC instant. We assert across every weekday that the result is a
// Saturday in Amman, at local midnight, and never after the input.
func TestAmmanWeekStartUTC(t *testing.T) {
	amman := timeutil.Amman()

	// A known Saturday in Amman: 2026-06-13 is a Saturday.
	for offset := 0; offset < 7; offset++ {
		// Walk Sat→Fri, sampling mid-afternoon to avoid any midnight edge ambiguity.
		day := time.Date(2026, 6, 13+offset, 15, 30, 0, 0, amman)
		got := ammanWeekStartUTC(day.UTC())

		local := got.In(amman)
		if local.Weekday() != time.Saturday {
			t.Errorf("offset %d (%s): week start %s is not a Saturday in Amman", offset, day.Weekday(), local)
		}
		if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 {
			t.Errorf("offset %d: week start %s is not Amman midnight", offset, local)
		}
		if got.After(day.UTC()) {
			t.Errorf("offset %d: week start %s is after the input %s", offset, got, day.UTC())
		}
		// The most recent Saturday must be exactly `offset` days before this day.
		wantDay := 13 + offset - offset // = 13, the Saturday
		if local.Day() != wantDay {
			t.Errorf("offset %d: week start day = %d, want %d", offset, local.Day(), wantDay)
		}
	}
}
