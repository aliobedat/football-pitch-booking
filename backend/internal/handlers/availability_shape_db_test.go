package handlers

// WO-24H-CONTINUITY: the /pitches/:id/availability payload must carry the
// hours_shape signal, and open_windows must stay DAY-shaped (D-A/D-B). Drives
// the REAL gin handler + repository + SQL and asserts the SERVED JSON for all
// three production schedule shapes. Reuses bsEnv from booking_sheet_db_test.go.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run AvailabilityShape -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// seedHours replaces a pitch's weekly schedule with one open/close on every
// weekday except those in `closed`.
func (e *bsEnv) seedHours(t *testing.T, pitch int64, open, close string, closed ...int) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, pitch); err != nil {
		t.Fatalf("clear hours: %v", err)
	}
	skip := map[int]bool{}
	for _, c := range closed {
		skip[c] = true
	}
	for wd := range 7 {
		if skip[wd] {
			continue
		}
		if _, err := e.pool.Exec(ctx,
			`INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time) VALUES ($1,$2,$3,$4)`,
			pitch, wd, open, close); err != nil {
			t.Fatalf("seed hours: %v", err)
		}
	}
}

type availabilityPayload struct {
	PitchID     int    `json:"pitch_id"`
	Date        string `json:"date"`
	Count       int    `json:"count"`
	HasSchedule bool   `json:"has_schedule"`
	HoursShape  string `json:"hours_shape"`
	OpenWindows []struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"open_windows"`
}

func TestAvailabilityShape_ServedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	pitch := e.mkPitch(t, e.ownerA)

	// A fixed future Monday, so "the next day" is a known weekday.
	monday := time.Date(2027, 3, 1, 0, 0, 0, 0, timeutil.Amman())
	if monday.Weekday() != time.Monday {
		t.Fatalf("anchor 2027-03-01 is %s, expected Monday", monday.Weekday())
	}
	dateStr := monday.Format("2006-01-02")
	dayStart := monday.UTC()
	nextMidnight := monday.AddDate(0, 0, 1).UTC()

	r := gin.New()
	h := NewBookingHandler(e.pool, nil)
	r.GET("/pitches/:id/availability", h.GetPitchAvailability)

	get := func(t *testing.T) availabilityPayload {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/pitches/%d/availability?date=%s", pitch, dateStr), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var p availabilityPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
		}
		return p
	}

	// ── Production shape 1: 24/7 (pitches «ملعب ١» and «ملعب ٢») ─────────────
	t.Run("continuous_24_7", func(t *testing.T) {
		e.seedHours(t, pitch, "00:00", "00:00")
		p := get(t)
		if p.HoursShape != string(data.ShapeContinuous) {
			t.Fatalf("hours_shape = %q, want %q", p.HoursShape, data.ShapeContinuous)
		}
		if !p.HasSchedule {
			t.Fatalf("has_schedule = false, want true")
		}
		// D-A/D-B: the payload stays DAY-shaped — no merged multi-day run.
		if len(p.OpenWindows) != 1 ||
			!p.OpenWindows[0].Start.Equal(dayStart) || !p.OpenWindows[0].End.Equal(nextMidnight) {
			t.Fatalf("open_windows must stay day-shaped [%s,%s), got %+v", dayStart, nextMidnight, p.OpenWindows)
		}
	})

	// ── Production shape 2: spill (pitch «ملعب المنطلق», 16:00→01:00) ────────
	t.Run("spill_16_to_01", func(t *testing.T) {
		e.seedHours(t, pitch, "16:00", "01:00")
		p := get(t)
		if p.HoursShape != string(data.ShapeSpill) {
			t.Fatalf("hours_shape = %q, want %q", p.HoursShape, data.ShapeSpill)
		}
		// The past-midnight tail is carried by the window itself.
		spillEnd := monday.AddDate(0, 0, 1).Add(time.Hour).UTC()
		var found bool
		for _, w := range p.OpenWindows {
			if w.End.Equal(spillEnd) {
				found = true
			}
		}
		if !found {
			t.Fatalf("spill window ending %s missing from open_windows: %+v", spillEnd, p.OpenWindows)
		}
	})

	// ── Production shape 3: full day followed by a CLOSED day ────────────────
	t.Run("day_bounded_closed_next", func(t *testing.T) {
		e.seedHours(t, pitch, "00:00", "00:00", int(time.Tuesday)) // Tue closed
		p := get(t)
		if p.HoursShape != string(data.ShapeDayBounded) {
			t.Fatalf("hours_shape = %q, want %q", p.HoursShape, data.ShapeDayBounded)
		}
	})

	// ── Unconfigured: fail-open 24/7 → continuous, has_schedule false ────────
	t.Run("unconfigured_fail_open", func(t *testing.T) {
		if _, err := e.pool.Exec(context.Background(),
			`DELETE FROM operating_hours WHERE pitch_id = $1`, pitch); err != nil {
			t.Fatalf("clear hours: %v", err)
		}
		p := get(t)
		if p.HasSchedule {
			t.Fatalf("has_schedule = true, want false")
		}
		if p.HoursShape != string(data.ShapeContinuous) {
			t.Fatalf("hours_shape = %q, want %q", p.HoursShape, data.ShapeContinuous)
		}
	})
}
