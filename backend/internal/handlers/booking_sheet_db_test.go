package handlers

// WO-BOOKING-SHEET / PR-A — Gate 3: DB-backed end-to-end tests for the extend
// and payment endpoints, driving the REAL gin handlers + repositories + SQL
// against a live database. Gated on PITCH_SCOPING_TEST_DATABASE_URL; the
// acceptance run points it at a disposable scratch database (faithful schema +
// migration 032 applied) so the suite EXECUTES — a skipped run is a failed gate.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run BookingSheetDB -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type bsEnv struct {
	pool                                 *pgxpool.Pool
	ownerA, ownerB, staffUser, playerID  int64
	pitchA, pitchB, pitchStaff, pitchHrs int64 // pitchHrs has 24/7 operating_hours
	dayCounter                           int
}

func newBSEnv(t *testing.T) *bsEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping booking-sheet DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	testutil.AssertSchemaBaseline(t, pool)
	e := &bsEnv{pool: pool}
	e.ownerA = e.mkUser(t, "owner")
	e.ownerB = e.mkUser(t, "owner")
	e.staffUser = e.mkUser(t, "staff")
	e.playerID = e.mkUser(t, "player")
	e.pitchA = e.mkPitch(t, e.ownerA)
	e.pitchB = e.mkPitch(t, e.ownerB)
	e.pitchStaff = e.mkPitch(t, e.ownerA) // owned by A; staff is bound to it in the router
	e.pitchHrs = e.mkPitch(t, e.ownerA)
	// pitchHrs: open 08:00–22:00 every weekday (all 7 → no weekday math needed).
	for wd := 0; wd < 7; wd++ {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time) VALUES ($1,$2,'08:00','22:00')`,
			e.pitchHrs, wd); err != nil {
			t.Fatalf("seed hours: %v", err)
		}
	}
	return e
}

func (e *bsEnv) mkUser(t *testing.T, role string) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO users (role, full_name) VALUES ($1::user_role, $2) RETURNING id`,
		role, "T "+role).Scan(&id); err != nil {
		t.Fatalf("mkUser: %v", err)
	}
	return id
}

// mkVenue seeds a venue for the owner — pitches.venue_id is NOT NULL since
// migration 034, so every raw pitch insert needs one. Named after its lone
// pitch, mirroring the 033 1:1 backfill: the single-pitch collapse rule in
// pitchDisplayNameExpr then yields the pitch's own name, keeping legacy
// display fields byte-identical.
func (e *bsEnv) mkVenue(t *testing.T, owner int64, name string) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url)
		 VALUES ($1,$2,$3,'Amman','') RETURNING id`,
		owner, name, fmt.Sprintf("bs-venue-%d", testutil.UniqueSuffix())).Scan(&id); err != nil {
		t.Fatalf("mkVenue: %v", err)
	}
	return id
}

func (e *bsEnv) mkPitch(t *testing.T, owner int64) int64 {
	t.Helper()
	// neighborhood/surface/format are NOT NULL on the live schema (drift ledger);
	// defaults only — no assertion reads them.
	var id int64
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO pitches (owner_id, name, price_per_hour, neighborhood, surface, format, venue_id)
		 VALUES ($1,$2,$3,'Amman','artificial_grass','خماسي',$4) RETURNING id`,
		owner, "P", 25, e.mkVenue(t, owner, "P")).Scan(&id); err != nil {
		t.Fatalf("mkPitch: %v", err)
	}
	return id
}

// span allocates a unique, non-overlapping [start,end) window (distinct day per
// call) so seeded bookings never collide across subtests via the EXCLUDE.
func (e *bsEnv) span(hourStart, hourEnd int) (time.Time, time.Time) {
	e.dayCounter++
	base := time.Date(2027, 1, 1, 0, 0, 0, 0, timeutil.Amman()).AddDate(0, 0, e.dayCounter)
	s := base.Add(time.Duration(hourStart) * time.Hour)
	en := base.Add(time.Duration(hourEnd) * time.Hour)
	return s.UTC(), en.UTC()
}

// mkBooking inserts a booking and returns its id. playerID nil → non-player row.
func (e *bsEnv) mkBooking(t *testing.T, pitch int64, source, status string, playerID *int64, start, end time.Time, total float64, amountPaid *float64, paymentStatus string) int64 {
	t.Helper()
	guest := "ضيف"
	var gn any = guest
	if source == "player" || source == "block" {
		gn = nil
	}
	if paymentStatus == "" {
		paymentStatus = "unpaid"
	}
	var id int64
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, status, source, total_price, amount_paid, payment_status, guest_name)
		VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), $5::booking_status, $6, $7, $8, $9::payment_status, $10)
		RETURNING id`,
		pitch, playerID, start, end, status, source, total, amountPaid, paymentStatus, gn).Scan(&id)
	if err != nil {
		t.Fatalf("mkBooking: %v", err)
	}
	return id
}

type bookingState struct {
	total         float64
	amountPaid    *float64
	paymentStatus string
	status        string
	start, end    time.Time
}

func (e *bsEnv) readBooking(t *testing.T, id int64) bookingState {
	t.Helper()
	var b bookingState
	if err := e.pool.QueryRow(context.Background(), `
		SELECT total_price::float8, amount_paid::float8, payment_status, status,
		       lower(booking_range), upper(booking_range)
		FROM bookings WHERE id=$1`, id).
		Scan(&b.total, &b.amountPaid, &b.paymentStatus, &b.status, &b.start, &b.end); err != nil {
		t.Fatalf("readBooking: %v", err)
	}
	return b
}

// router wires the two real endpoints with injected actor+scope (simulating
// RequireAuth+ResolveScope), including the real RequireRole guards.
func (e *bsEnv) router(actorID int64, role string, boundPitch int64) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int(actorID))
		c.Set(middleware.ContextKeyRole, role)
		c.Set(middleware.ContextKeyActor, auth.Actor{UserID: int(actorID), Role: role})
		c.Set(middleware.ContextKeyScope, auth.Scope{BoundPitchIDs: []int{int(boundPitch)}, ProvisionedBy: 1})
		c.Next()
	})
	sched := NewScheduleHandler(repository.NewScheduleRepository(e.pool))
	sheet := NewBookingSheetHandler(repository.NewBookingSheetRepository(e.pool), &data.PitchModel{DB: e.pool})
	r.PATCH("/bookings/:id/payment", middleware.RequireRole("staff", "owner", "admin"), sched.PatchPayment)
	r.PATCH("/bookings/:id/extend", middleware.RequireRole("owner", "admin"), sheet.ExtendBooking)
	return r
}

func bsDo(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeSheet(t *testing.T, rec *httptest.ResponseRecorder) repository.BookingSheet {
	t.Helper()
	var resp struct {
		Data repository.BookingSheet `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	return resp.Data
}

func f64(v float64) *float64 { return &v }

func TestBookingSheetDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newBSEnv(t)
	ctx := context.Background()
	ownerA := e.router(e.ownerA, "owner", 0)

	// ── 1. +30 and +60 happy path: range grows, delta exact, amount_paid untouched ──
	t.Run("extend_happy_30_60", func(t *testing.T) {
		for _, m := range []int{30, 60} {
			s, en := e.span(10, 11)
			id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
			rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": m})
			if rec.Code != 200 {
				t.Fatalf("minutes=%d: status=%d body=%s", m, rec.Code, rec.Body.String())
			}
			b := e.readBooking(t, id)
			wantEnd := en.Add(time.Duration(m) * time.Minute)
			if !b.end.Equal(wantEnd) {
				t.Fatalf("minutes=%d: end=%v want %v", m, b.end, wantEnd)
			}
			wantTotal := 25 + 25*float64(m)/60
			if fmt.Sprintf("%.3f", b.total) != fmt.Sprintf("%.3f", wantTotal) {
				t.Fatalf("minutes=%d: total=%.3f want %.3f", m, b.total, wantTotal)
			}
			if b.amountPaid != nil {
				t.Fatalf("amount_paid touched: %v", *b.amountPaid)
			}
		}
	})

	// ── 2. Adjacent non-cancelled booking → 409 slot_conflict, unchanged ──
	t.Run("extend_conflict", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, en, en.Add(time.Hour), 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.end.Equal(en) {
			t.Fatalf("row changed on conflict: end=%v want %v", b.end, en)
		}
	})

	// ── 3. Extend into a slot occupied ONLY by a cancelled booking → succeeds ──
	t.Run("extend_over_cancelled_ok", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		e.mkBooking(t, e.pitchA, "player", "cancelled", &e.playerID, en, en.Add(time.Hour), 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (predicate should exclude cancelled) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── 4. Past operating close → 400; no-hours pitch → fail-open, allowed ──
	t.Run("extend_outside_hours", func(t *testing.T) {
		s, en := e.span(21, 22) // ends at 22:00 close
		id := e.mkBooking(t, e.pitchHrs, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 400 {
			t.Fatalf("status=%d want 400 outside_operating_hours (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("extend_no_hours_failopen", func(t *testing.T) {
		s, en := e.span(21, 22)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid") // pitchA has no hours
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (fail-open) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── 5. Cancelled → 409; ended → 400; in-progress → succeeds ──
	t.Run("extend_cancelled", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "cancelled", &e.playerID, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 booking_cancelled (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("extend_ended", func(t *testing.T) {
		start := time.Now().Add(-2 * time.Hour)
		end := time.Now().Add(-1 * time.Hour)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, start, end, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 400 {
			t.Fatalf("status=%d want 400 booking_ended (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("extend_in_progress_ok", func(t *testing.T) {
		start := time.Now().Add(-30 * time.Minute)
		end := time.Now().Add(90 * time.Minute)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, start, end, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 (in-progress extendable) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── 6. source='block' → 409 not_a_booking ──
	t.Run("extend_block", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "block", "confirmed", nil, s, en, 0, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 not_a_booking (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── 7. Cross-tenant → 404 ──
	t.Run("extend_cross_tenant_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid") // owner B's pitch
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── 8. minutes:45 → 400 ──
	t.Run("extend_invalid_minutes", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 45})
		if rec.Code != 400 {
			t.Fatalf("status=%d want 400 invalid_minutes (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── 9. Concurrency: two parallel +60, only one fits → one 200, one 409 ──
	t.Run("extend_concurrency", func(t *testing.T) {
		s, en := e.span(10, 11)                                                                                                 // [10,11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")                            // extendable to 12
		e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, en.Add(time.Hour), en.Add(2*time.Hour), 25, nil, "unpaid") // Z=[12,13)
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60})
				codes[i] = rec.Code
			}(i)
		}
		wg.Wait()
		ok, conflict := 0, 0
		for _, c := range codes {
			switch c {
			case 200:
				ok++
			case 409:
				conflict++
			}
		}
		if ok != 1 || conflict != 1 {
			t.Fatalf("codes=%v want exactly one 200 and one 409", codes)
		}
		if b := e.readBooking(t, id); !b.end.Equal(en.Add(time.Hour)) {
			t.Fatalf("final end=%v want %v (extended exactly once)", b.end, en.Add(time.Hour))
		}
	})

	// ── 10. Fully-paid booking extended → amount_paid unchanged, display→partial, legacy status unchanged ──
	t.Run("extend_fully_paid_becomes_partial", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 25, f64(25), "paid_cash")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60})
		if rec.Code != 200 {
			t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
		}
		sheet := decodeSheet(t, rec)
		if sheet.AmountPaid == nil || *sheet.AmountPaid != 25 {
			t.Fatalf("amount_paid=%v want 25 (unchanged)", sheet.AmountPaid)
		}
		if sheet.PaymentDisplay != "partial" {
			t.Fatalf("display=%q want partial", sheet.PaymentDisplay)
		}
		if b := e.readBooking(t, id); b.paymentStatus != "paid_cash" {
			t.Fatalf("legacy payment_status=%q want paid_cash (untouched by extend)", b.paymentStatus)
		}
	})

	// ── 11. legacy paid_cash → amount_paid=total, status paid_cash ──
	t.Run("pay_legacy_paid", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"payment_status": "paid_cash"})
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if b.amountPaid == nil || *b.amountPaid != 30 || b.paymentStatus != "paid_cash" {
			t.Fatalf("got amount_paid=%v status=%q want 30/paid_cash", b.amountPaid, b.paymentStatus)
		}
	})

	// ── 12. legacy unpaid → amount_paid=NULL, status unpaid ──
	t.Run("pay_legacy_unpaid", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, f64(30), "paid_cash")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"payment_status": "unpaid"})
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if b.amountPaid != nil || b.paymentStatus != "unpaid" {
			t.Fatalf("got amount_paid=%v status=%q want NULL/unpaid", b.amountPaid, b.paymentStatus)
		}
	})

	// ── 13. new partial 15/30 → display partial, remaining 15, payment_status unpaid ──
	t.Run("pay_new_partial", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 15})
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		sheet := decodeSheet(t, rec)
		if sheet.PaymentDisplay != "partial" || sheet.Remaining == nil || *sheet.Remaining != 15 {
			t.Fatalf("display=%q remaining=%v want partial/15", sheet.PaymentDisplay, sheet.Remaining)
		}
		if b := e.readBooking(t, id); b.paymentStatus != "unpaid" {
			t.Fatalf("legacy payment_status=%q want unpaid (partial invisible to frozen consumers)", b.paymentStatus)
		}
	})

	// ── 14. new amount_paid=total → paid_cash ──
	t.Run("pay_new_full", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 30})
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); b.paymentStatus != "paid_cash" {
			t.Fatalf("payment_status=%q want paid_cash (bridge)", b.paymentStatus)
		}
	})

	// ── 15. amount_paid > total → 422; CHECK also proven by direct SQL ──
	t.Run("pay_exceeds_total_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 40})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 paid_exceeds_total (body=%s)", rec.Code, rec.Body.String())
		}
		// Direct-SQL negative: the CHECK rejects amount_paid > total_price.
		_, err := e.pool.Exec(ctx,
			`INSERT INTO bookings (pitch_id, player_id, booking_range, status, source, total_price, amount_paid)
			 VALUES ($1,$2, tstzrange(now()+interval '5 day', now()+interval '5 day 1 hour','[)'),'confirmed','player',10,20)`,
			e.pitchA, e.playerID)
		if err == nil {
			t.Fatalf("CHECK failed: amount_paid=20 > total=10 was accepted")
		}
	})

	// ── 16. amount_paid:null → NULL, payment_status untouched ──
	t.Run("pay_null_untouched", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, f64(30), "paid_cash")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), map[string]any{"amount_paid": nil})
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if b.amountPaid != nil {
			t.Fatalf("amount_paid=%v want NULL", *b.amountPaid)
		}
		if b.paymentStatus != "paid_cash" {
			t.Fatalf("payment_status=%q want paid_cash (untouched on revert-to-null)", b.paymentStatus)
		}
	})

	// ── 17. Discount (new total) then extend → delta applies on discounted total ──
	t.Run("discount_then_extend", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		if rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"total_price": 20}); rec.Code != 200 {
			t.Fatalf("discount status=%d body=%s", rec.Code, rec.Body.String())
		}
		if rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 60}); rec.Code != 200 {
			t.Fatalf("extend status=%d body=%s", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); fmt.Sprintf("%.3f", b.total) != "45.000" {
			t.Fatalf("total=%.3f want 45.000 (20 discounted + 25 delta)", b.total)
		}
	})

	// ── 18. Lower total below existing amount_paid, no paid adjustment → 422 ──
	t.Run("lower_total_below_paid_422", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		if rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 25}); rec.Code != 200 {
			t.Fatalf("set paid status=%d body=%s", rec.Code, rec.Body.String())
		}
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"total_price": 20})
		if rec.Code != 422 {
			t.Fatalf("status=%d want 422 (lower total below paid) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── 19. mixed → 400; cancelled → 409; block → 409; cross-tenant → 404 ──
	t.Run("pay_mixed_400", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"payment_status": "paid_cash", "amount_paid": 10})
		if rec.Code != 400 {
			t.Fatalf("status=%d want 400 ambiguous_payment_body (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("pay_cancelled_409", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "cancelled", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 10})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 booking_cancelled (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("pay_block_409", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "block", "confirmed", nil, s, en, 0, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 0})
		if rec.Code != 409 {
			t.Fatalf("status=%d want 409 not_a_booking (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("pay_cross_tenant_404", func(t *testing.T) {
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchB, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(ownerA, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 10})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// ── 20. Staff on bound pitch: legacy toggle + new amount_paid only → 200, bridge correct ──
	t.Run("staff_bound_ok", func(t *testing.T) {
		staff := e.router(e.staffUser, "staff", e.pitchStaff)
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		if rec := bsDo(staff, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"payment_status": "paid_cash"}); rec.Code != 200 {
			t.Fatalf("staff legacy toggle status=%d body=%s", rec.Code, rec.Body.String())
		}
		if rec := bsDo(staff, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 15}); rec.Code != 200 {
			t.Fatalf("staff new amount_paid status=%d body=%s", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); b.paymentStatus != "unpaid" || b.amountPaid == nil || *b.amountPaid != 15 {
			t.Fatalf("bridge wrong: amount_paid=%v status=%q want 15/unpaid", b.amountPaid, b.paymentStatus)
		}
	})

	// ── 21. Staff bound + total_price → 403 price_change_forbidden, row unchanged ──
	t.Run("staff_total_forbidden", func(t *testing.T) {
		staff := e.router(e.staffUser, "staff", e.pitchStaff)
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(staff, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 10, "total_price": 20})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 price_change_forbidden (body=%s)", rec.Code, rec.Body.String())
		}
		b := e.readBooking(t, id)
		if b.amountPaid != nil || b.total != 30 {
			t.Fatalf("row changed: amount_paid=%v total=%.3f want NULL/30", b.amountPaid, b.total)
		}
	})

	// ── 22. Staff on UNBOUND pitch → 404 ──
	t.Run("staff_unbound_404", func(t *testing.T) {
		staff := e.router(e.staffUser, "staff", e.pitchStaff) // bound to pitchStaff, not pitchA
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchA, "player", "confirmed", &e.playerID, s, en, 30, nil, "unpaid")
		rec := bsDo(staff, http.MethodPatch, fmt.Sprintf("/bookings/%d/payment", id), gin.H{"amount_paid": 10})
		if rec.Code != 404 {
			t.Fatalf("status=%d want 404 (staff unbound) body=%s", rec.Code, rec.Body.String())
		}
	})

	// ── 23. Staff attempting extend → route-guard 403, booking unchanged ──
	t.Run("staff_extend_forbidden", func(t *testing.T) {
		staff := e.router(e.staffUser, "staff", e.pitchStaff)
		s, en := e.span(10, 11)
		id := e.mkBooking(t, e.pitchStaff, "player", "confirmed", &e.playerID, s, en, 25, nil, "unpaid")
		rec := bsDo(staff, http.MethodPatch, fmt.Sprintf("/bookings/%d/extend", id), gin.H{"minutes": 30})
		if rec.Code != 403 {
			t.Fatalf("status=%d want 403 (route guard bars staff) body=%s", rec.Code, rec.Body.String())
		}
		if b := e.readBooking(t, id); !b.end.Equal(en) {
			t.Fatalf("booking changed: end=%v want %v", b.end, en)
		}
	})
}
