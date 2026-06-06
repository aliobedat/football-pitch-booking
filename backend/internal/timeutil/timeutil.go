// Package timeutil centralises Malaeb's timezone policy.
//
// Principle: STORE and COMPUTE in UTC (absolute instants); INTERPRET and DISPLAY
// civil/"today" semantics in Asia/Amman via the IANA tz database — never a
// hardcoded offset. Jordan currently observes UTC+3 year-round (DST abolished
// ~2022), but that rule lives in the tz database, not in this code, so a future
// rule change is picked up automatically.
//
// The blank import of time/tzdata EMBEDS the IANA database into the binary, so
// time.LoadLocation("Asia/Amman") works even in a minimal/scratch container that
// ships no system zoneinfo. Without it LoadLocation would silently fall back to
// UTC and every civil-day computation would be wrong.
package timeutil

import (
	"time"
	_ "time/tzdata" // embed the IANA tz database (scratch-container safe)
)

// AmmanZone is the IANA name for Jordan's timezone.
const AmmanZone = "Asia/Amman"

// amman is loaded once at init. Loading is fatal on failure: a deployment whose
// binary cannot resolve Asia/Amman must fail loudly, never silently serve UTC
// times as if they were local.
var amman = mustLoad(AmmanZone)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("timeutil: could not load location " + name + ": " + err.Error())
	}
	return loc
}

// Amman returns the Asia/Amman location. Use it to render an absolute UTC instant
// in Jordan civil time, e.g. utcInstant.In(timeutil.Amman()).
func Amman() *time.Location { return amman }

// InAmman renders an absolute instant in Asia/Amman civil time. The underlying
// instant is unchanged — only its location (and thus wall-clock fields) shifts.
func InAmman(t time.Time) time.Time { return t.In(amman) }

// AmmanDayBoundsUTC returns the [start, end) UTC instants that bound the civil
// day (the year/month/day of date, in Asia/Amman). It is the correct way to ask
// "which absolute instants belong to Amman calendar-day X" — e.g. for daily slot
// availability against UTC-stored booking ranges. The day's length is derived
// from the tz database (start-of-next-day minus start-of-day), so any DST rule is
// honoured without special-casing.
//
// Only the calendar date of `date` is read; its clock time and location are
// ignored, so callers may pass a date parsed as midnight-UTC.
func AmmanDayBoundsUTC(date time.Time) (startUTC, endUTC time.Time) {
	y, m, d := date.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, amman)
	end := time.Date(y, m, d+1, 0, 0, 0, 0, amman) // normalises across month/year
	return start.UTC(), end.UTC()
}
