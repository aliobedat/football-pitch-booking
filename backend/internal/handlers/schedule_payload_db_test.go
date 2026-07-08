package handlers

// WO-BOOKING-SHEET / PR-B.2a — DB-backed tests for the additive schedule payload:
// GET /schedule rows must carry total_price / amount_paid / payment_display /
// remaining plus a per-row price_per_hour, with the legacy fields untouched.
// Same scratch-DB recipe as TestBookingSheetDB (a skipped run is a failed gate):
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run SchedulePayloadDB -v

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// schedRouter wires the real GET /schedule handler behind an injected actor,
// mirroring bsEnv.router.
func (e *bsEnv) schedRouter(actorID int64, role string, boundPitch int64) *gin.Engine {
	r := e.router(actorID, role, boundPitch)
	sched := NewScheduleHandler(repository.NewScheduleRepository(e.pool))
	r.GET("/schedule", middleware.RequireRole("staff", "owner", "admin"), sched.GetDailySchedule)
	return r
}

// mkPitchRate inserts a pitch with an explicit hourly rate (bsEnv.mkPitch
// hardcodes 25; the per-row assertion needs two different rates).
func (e *bsEnv) mkPitchRate(t *testing.T, owner int64, rate int) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(t.Context(),
		`INSERT INTO pitches (owner_id, name, price_per_hour) VALUES ($1,$2,$3) RETURNING id`,
		owner, fmt.Sprintf("P%d", rate), rate).Scan(&id); err != nil {
		t.Fatalf("mkPitchRate: %v", err)
	}
	return id
}

// sameDaySpan returns non-overlapping hour windows all on ONE Amman civil day,
// so a single GET /schedule?date=... sees every seeded row.
func schedDay() time.Time {
	return time.Date(2027, 6, 1, 0, 0, 0, 0, timeutil.Amman())
}
func schedSpan(hourStart, hourEnd int) (time.Time, time.Time) {
	d := schedDay()
	return d.Add(time.Duration(hourStart) * time.Hour).UTC(), d.Add(time.Duration(hourEnd) * time.Hour).UTC()
}

func schedGet(t *testing.T, r *gin.Engine) []map[string]json.RawMessage {
	t.Helper()
	rec := bsDo(r, http.MethodGet, "/schedule?date="+schedDay().Format("2006-01-02"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /schedule: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Data
}

func rowByID(t *testing.T, rows []map[string]json.RawMessage, id int64) map[string]json.RawMessage {
	t.Helper()
	for _, r := range rows {
		var got int64
		if err := json.Unmarshal(r["id"], &got); err == nil && got == id {
			return r
		}
	}
	t.Fatalf("row %d not in schedule payload (%d rows)", id, len(rows))
	return nil
}

func jsonStr(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("expected string, got %s", raw)
	}
	return s
}

func jsonF64(t *testing.T, raw json.RawMessage) *float64 {
	t.Helper()
	if string(raw) == "null" {
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("expected number|null, got %s", raw)
	}
	return &v
}

func wantF64(t *testing.T, field string, raw json.RawMessage, want *float64) {
	t.Helper()
	got := jsonF64(t, raw)
	switch {
	case want == nil && got != nil:
		t.Fatalf("%s: want null, got %v", field, *got)
	case want != nil && got == nil:
		t.Fatalf("%s: want %v, got null", field, *want)
	case want != nil && math.Abs(*got-*want) > 0.0005:
		t.Fatalf("%s: want %.3f, got %.3f", field, *want, *got)
	}
}

func TestSchedulePayloadDB(t *testing.T) {
	e := newBSEnv(t)
	// Two pitches under ownerA with DIFFERENT hourly rates.
	p25 := e.mkPitchRate(t, e.ownerA, 25)
	p40 := e.mkPitchRate(t, e.ownerA, 40)

	s1, e1 := schedSpan(8, 9)
	s2, e2 := schedSpan(9, 10)
	s3, e3 := schedSpan(10, 11)
	s4, e4 := schedSpan(11, 12)
	s5, e5 := schedSpan(12, 13)

	untracked := e.mkBooking(t, p25, "manual", "confirmed", nil, s1, e1, 25, nil, "unpaid")
	partial := e.mkBooking(t, p40, "manual", "confirmed", nil, s2, e2, 30.5, f64(10), "unpaid")
	paid := e.mkBooking(t, p25, "player", "confirmed", &e.playerID, s3, e3, 25, f64(25), "paid_cash")
	zero := e.mkBooking(t, p40, "manual", "confirmed", nil, s4, e4, 40, f64(0), "unpaid")
	block := e.mkBooking(t, p25, "block", "confirmed", nil, s5, e5, 0, nil, "unpaid")

	r := e.schedRouter(e.ownerA, "owner", 0)
	rows := schedGet(t, r)

	t.Run("per_row_price_per_hour_across_rates", func(t *testing.T) {
		for id, want := range map[int64]int{untracked: 25, partial: 40, paid: 25, zero: 40, block: 25} {
			row := rowByID(t, rows, id)
			var rate int
			if err := json.Unmarshal(row["price_per_hour"], &rate); err != nil || rate != want {
				t.Fatalf("booking %d price_per_hour: want %d, got %s (err=%v)", id, want, row["price_per_hour"], err)
			}
			for _, k := range []string{"total_price", "amount_paid", "payment_display", "remaining"} {
				if _, ok := row[k]; !ok {
					t.Fatalf("booking %d: field %q missing from payload", id, k)
				}
			}
		}
	})

	t.Run("derivation_untracked", func(t *testing.T) {
		row := rowByID(t, rows, untracked)
		if d := jsonStr(t, row["payment_display"]); d != "untracked" {
			t.Fatalf("payment_display: want untracked, got %s", d)
		}
		wantF64(t, "amount_paid", row["amount_paid"], nil)
		wantF64(t, "remaining", row["remaining"], nil)
		wantF64(t, "total_price", row["total_price"], f64(25))
	})

	t.Run("derivation_partial_3dp", func(t *testing.T) {
		row := rowByID(t, rows, partial)
		if d := jsonStr(t, row["payment_display"]); d != "partial" {
			t.Fatalf("payment_display: want partial, got %s", d)
		}
		wantF64(t, "amount_paid", row["amount_paid"], f64(10))
		wantF64(t, "remaining", row["remaining"], f64(20.5))
		wantF64(t, "total_price", row["total_price"], f64(30.5))
	})

	t.Run("derivation_paid", func(t *testing.T) {
		row := rowByID(t, rows, paid)
		if d := jsonStr(t, row["payment_display"]); d != "paid" {
			t.Fatalf("payment_display: want paid, got %s", d)
		}
		wantF64(t, "remaining", row["remaining"], f64(0))
	})

	t.Run("derivation_zero_unpaid", func(t *testing.T) {
		row := rowByID(t, rows, zero)
		if d := jsonStr(t, row["payment_display"]); d != "unpaid" {
			t.Fatalf("payment_display: want unpaid, got %s", d)
		}
		wantF64(t, "amount_paid", row["amount_paid"], f64(0))
		wantF64(t, "remaining", row["remaining"], f64(40))
	})

	t.Run("block_row_null_safe", func(t *testing.T) {
		row := rowByID(t, rows, block)
		if d := jsonStr(t, row["payment_display"]); d != "untracked" {
			t.Fatalf("block payment_display: want untracked (NULL amount), got %s", d)
		}
		wantF64(t, "amount_paid", row["amount_paid"], nil)
		wantF64(t, "remaining", row["remaining"], nil)
		if s := jsonStr(t, row["source"]); s != "block" {
			t.Fatalf("source: want block, got %s", s)
		}
	})

	t.Run("legacy_fields_byte_identical", func(t *testing.T) {
		// Backward-compat proof for the two untouched callers (calendar + bookings
		// pages): every pre-B.2a key still present with the exact same JSON bytes.
		row := rowByID(t, rows, paid)
		want := map[string]string{
			"id":             fmt.Sprintf("%d", paid),
			"pitch_id":       fmt.Sprintf("%d", p25),
			"pitch_name":     `"P25"`,
			"source":         `"player"`,
			"status":         `"confirmed"`,
			"attendance":     `"pending"`,
			"payment_status": `"paid_cash"`,
			"attendee_name":  `"T player"`,
		}
		for k, w := range want {
			got, ok := row[k]
			if !ok {
				t.Fatalf("legacy field %q missing", k)
			}
			if string(got) != w {
				t.Fatalf("legacy field %q changed: want %s, got %s", k, w, got)
			}
		}
		for _, k := range []string{"start_time", "end_time"} {
			if _, ok := row[k]; !ok {
				t.Fatalf("legacy field %q missing", k)
			}
		}
	})
}
