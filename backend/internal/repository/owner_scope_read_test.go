package repository

// §5.6 Batch 2 — cross-owner READ isolation. Owner A's read surfaces must exclude
// owner B's data. Two assertion shapes:
//
//   Shape 1 (membership): customers list, customer profile, calendar day-view,
//   expense list — owner A's result CONTAINS A's seeded ids and NOT B's.
//
//   Shape 2 (EXACT value): analytics revenue + timeseries — a leak sums B's money
//   INTO A's figure and still looks plausible, so we assert A's total == the
//   hand-computed X EXACTLY while owner B's distinct Y is present in the same DB.
//   If A came back as X+Y that's a CRITICAL revenue leak.
//
// Owners are freshly seeded, so an owner-scoped aggregate over ALL time equals
// exactly that owner's seeded total regardless of other rows already in the shared
// DB — that is what makes the exact assertion robust.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run OwnerScopeRead -v

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// Hand-computed seeded revenue totals. A = 30 + 45; B = 55 (distinct, non-zero).
const (
	ownerARevenue = 75.0
	ownerBRevenue = 55.0
)

type ownerReadEnv struct {
	pool           *pgxpool.Pool
	analytics      AnalyticsRepository
	custRepo       CustomerRepository
	calRepo        CalendarRepository
	expRepo        ExpenseRepository
	ownerA, ownerB int
	pitchA, pitchB int64
	custA, custB   int64
	expA, expB     int64
	d1, d2         time.Time // Amman 10:00 on two distinct far-future days
}

func newOwnerReadEnv(t *testing.T) *ownerReadEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping owner-scope read integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	suffix := time.Now().UnixNano() % 1_000_000

	mkUser := func(name, prefix string) int {
		var id int
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,'owner',TRUE) RETURNING id
		`, name, phone).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	player := func() int {
		var id int
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ('OR Player',$1,'player',TRUE) RETURNING id
		`, fmt.Sprintf("+96258%06d", suffix)).Scan(&id); err != nil {
			t.Fatalf("seed player: %v", err)
		}
		return id
	}()

	e := &ownerReadEnv{
		pool:      pool,
		analytics: NewAnalyticsRepository(pool),
		custRepo:  NewCustomerRepository(pool),
		calRepo:   NewCalendarRepository(pool),
		expRepo:   NewExpenseRepository(pool),
		ownerA:    mkUser("OR Owner A", "54"),
		ownerB:    mkUser("OR Owner B", "55"),
	}

	pm := &data.PitchModel{DB: pool}
	mkPitch := func(name string, owner int) int64 {
		p, err := pm.CreatePitch(ctx, data.CreatePitchRequest{
			Name: name, Neighborhood: "Amman", Surface: "artificial_grass",
			Format: "خماسي", PricePerHour: 30, OwnerID: owner,
		})
		if err != nil {
			t.Fatalf("seed pitch %s: %v", name, err)
		}
		return int64(p.ID)
	}
	e.pitchA = mkPitch("OR Pitch A", e.ownerA)
	e.pitchB = mkPitch("OR Pitch B", e.ownerB)

	// Two distinct far-future Amman days at 10:00 (far enough that the shared DB is
	// unlikely to hold other bookings there — keeps admin timeseries buckets clean).
	base := time.Now().In(timeutil.Amman())
	mkDay := func(addDays int) time.Time {
		d := base.AddDate(0, 0, addDays)
		return time.Date(d.Year(), d.Month(), d.Day(), 10, 0, 0, 0, timeutil.Amman())
	}
	e.d1 = mkDay(200)
	e.d2 = mkDay(202)

	insBooking := func(pitch int64, start time.Time, price float64) {
		end := start.Add(time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
			VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), $5, 'confirmed', 'player')
		`, pitch, player, start.UTC(), end.UTC(), price); err != nil {
			t.Fatalf("seed booking pitch %d: %v", pitch, err)
		}
	}
	// Owner A: two non-overlapping slots on d1 → 30 + 45 = X (75).
	insBooking(e.pitchA, e.d1, 30)
	insBooking(e.pitchA, e.d1.Add(time.Hour), 45)
	// Owner B: one slot on d2 → Y (55).
	insBooking(e.pitchB, e.d2, 55)

	mkCustomer := func(owner int, prefix string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (owner_id, phone, name) VALUES ($1,$2,'Cust') RETURNING id
		`, owner, fmt.Sprintf("+962%s%06d", prefix, suffix)).Scan(&id); err != nil {
			t.Fatalf("seed customer (owner %d): %v", owner, err)
		}
		return id
	}
	e.custA = mkCustomer(e.ownerA, "56")
	e.custB = mkCustomer(e.ownerB, "57")

	mkExpense := func(owner int) int64 {
		exp, err := e.expRepo.Create(ctx, actorOwner(owner), ExpenseInput{
			PitchID: nil, Category: "Maintenance", Amount: 9, OccurredAt: time.Now().UTC(), Note: "r",
		})
		if err != nil {
			t.Fatalf("seed expense (owner %d): %v", owner, err)
		}
		return exp.ID
	}
	e.expA = mkExpense(e.ownerA)
	e.expB = mkExpense(e.ownerB)

	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(cctx, `DELETE FROM expenses WHERE owner_id = ANY($1)`, []int{e.ownerA, e.ownerB})
		_, _ = pool.Exec(cctx, `DELETE FROM customers WHERE owner_id = ANY($1)`, []int{e.ownerA, e.ownerB})
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, []int64{e.pitchA, e.pitchB})
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, []int64{e.pitchA, e.pitchB})
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int{e.ownerA, e.ownerB, player})
		pool.Close()
	})
	return e
}

// ── Shape 2: revenue summary, EXACT value ─────────────────────────────────────

func TestOwnerScopeRead_RevenueExact(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	// Owner A's all-time confirmed revenue must equal X EXACTLY — with owner B's
	// distinct Y present in the same DB. A==X (not X+Y) is the leak proof.
	a, err := e.analytics.OwnerRevenueSummary(ctx, actorOwner(e.ownerA), 0)
	if err != nil {
		t.Fatalf("owner A revenue: %v", err)
	}
	if a.TotalRevenue != ownerARevenue {
		t.Fatalf("CRITICAL revenue leak: owner A total = %.3f, want EXACTLY %.3f (B's %.3f must NOT be summed in; X+Y would be %.3f)",
			a.TotalRevenue, ownerARevenue, ownerBRevenue, ownerARevenue+ownerBRevenue)
	}
	if a.BookingCount != 2 {
		t.Fatalf("owner A booking count = %d, want 2", a.BookingCount)
	}

	b, err := e.analytics.OwnerRevenueSummary(ctx, actorOwner(e.ownerB), 0)
	if err != nil {
		t.Fatalf("owner B revenue: %v", err)
	}
	if b.TotalRevenue != ownerBRevenue {
		t.Fatalf("owner B total = %.3f, want %.3f", b.TotalRevenue, ownerBRevenue)
	}

	// Cross-owner by pitch id: owner A querying owner B's pitch → 0 (scoped out).
	cross, err := e.analytics.OwnerRevenueSummary(ctx, actorOwner(e.ownerA), int(e.pitchB))
	if err != nil {
		t.Fatalf("cross revenue: %v", err)
	}
	if cross.TotalRevenue != 0 || cross.BookingCount != 0 {
		t.Fatalf("CRITICAL: owner A read owner B's pitch revenue = %.3f/%d, want 0/0", cross.TotalRevenue, cross.BookingCount)
	}

	// Admin control: predicate → TRUE, so admin reads BOTH pitches. Per-pitch
	// scoping isolates from the rest of the shared DB, so these are exact.
	adminA, _ := e.analytics.OwnerRevenueSummary(ctx, auth.Actor{Role: auth.RoleAdmin}, int(e.pitchA))
	adminB, _ := e.analytics.OwnerRevenueSummary(ctx, auth.Actor{Role: auth.RoleAdmin}, int(e.pitchB))
	if adminA.TotalRevenue != ownerARevenue || adminB.TotalRevenue != ownerBRevenue {
		t.Fatalf("admin per-pitch = %.3f / %.3f, want %.3f / %.3f", adminA.TotalRevenue, adminB.TotalRevenue, ownerARevenue, ownerBRevenue)
	}
	if adminA.TotalRevenue+adminB.TotalRevenue != ownerARevenue+ownerBRevenue {
		t.Fatalf("admin X+Y = %.3f, want %.3f (proves predicate isn't hard-coding A's figure)",
			adminA.TotalRevenue+adminB.TotalRevenue, ownerARevenue+ownerBRevenue)
	}
}

// ── Shape 2: timeseries, EXACT buckets ────────────────────────────────────────

func bucketRevenue(series []TimeBucket, day string) (float64, bool) {
	for _, b := range series {
		if b.Bucket == day {
			return b.Revenue, true
		}
	}
	return 0, false
}

func TestOwnerScopeRead_TimeSeriesExact(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	from, _ := timeutil.AmmanDayBoundsUTC(e.d1)
	_, to := timeutil.AmmanDayBoundsUTC(e.d2)
	d1str := e.d1.Format("2006-01-02")
	d2str := e.d2.Format("2006-01-02")
	params := TimeSeriesParams{Granularity: "day", From: from, To: to}

	// Owner A: d1 bucket == X exactly; the B-only day (d2) must be ABSENT.
	aSeries, err := e.analytics.OwnerTimeSeries(ctx, actorOwner(e.ownerA), params)
	if err != nil {
		t.Fatalf("owner A timeseries: %v", err)
	}
	if rev, ok := bucketRevenue(aSeries, d1str); !ok || rev != ownerARevenue {
		t.Fatalf("owner A d1 bucket = (%.3f, present=%v), want (%.3f, true)", rev, ok, ownerARevenue)
	}
	if rev, ok := bucketRevenue(aSeries, d2str); ok {
		t.Fatalf("CRITICAL: owner A's series has the B-only day %s with revenue %.3f — cross-owner leak", d2str, rev)
	}

	// Owner B: d2 bucket == Y; d1 absent.
	bSeries, err := e.analytics.OwnerTimeSeries(ctx, actorOwner(e.ownerB), params)
	if err != nil {
		t.Fatalf("owner B timeseries: %v", err)
	}
	if rev, ok := bucketRevenue(bSeries, d2str); !ok || rev != ownerBRevenue {
		t.Fatalf("owner B d2 bucket = (%.3f, present=%v), want (%.3f, true)", rev, ok, ownerBRevenue)
	}
	if _, ok := bucketRevenue(bSeries, d1str); ok {
		t.Fatalf("owner B's series leaked owner A's day %s", d1str)
	}

	// Admin: predicate → TRUE, sees BOTH days (the B-only day owner A could not).
	adminSeries, _ := e.analytics.OwnerTimeSeries(ctx, auth.Actor{Role: auth.RoleAdmin}, params)
	if _, ok := bucketRevenue(adminSeries, d1str); !ok {
		t.Fatalf("admin series missing owner A's day %s", d1str)
	}
	if _, ok := bucketRevenue(adminSeries, d2str); !ok {
		t.Fatalf("admin series missing owner B's day %s — predicate did not degrade to TRUE", d2str)
	}
}

// ── Shape 1: customers list (membership) ──────────────────────────────────────

func TestOwnerScopeRead_CustomersList(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	aList, err := e.custRepo.ListCustomers(ctx, actorOwner(e.ownerA), "", "")
	if err != nil {
		t.Fatalf("owner A customers: %v", err)
	}
	aIDs := map[int64]bool{}
	for _, c := range aList {
		aIDs[c.ID] = true
	}
	if !aIDs[e.custA] {
		t.Fatalf("owner A must see own customer %d", e.custA)
	}
	if aIDs[e.custB] {
		t.Fatalf("CRITICAL: owner A's customer list contains owner B's customer %d", e.custB)
	}
}

// ── Shape 1: customer profile (cross-owner → not found) ───────────────────────

func TestOwnerScopeRead_CustomerProfile(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	// Cross-owner fetch by id → ErrCustomerNotFound, NOT a populated record.
	prof, err := e.custRepo.GetCustomerProfile(ctx, actorOwner(e.ownerA), e.custB)
	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("owner A fetching owner B's customer: err = %v, want ErrCustomerNotFound", err)
	}
	if prof != nil {
		t.Fatalf("CRITICAL: cross-owner profile returned a populated record: %+v", prof)
	}

	// Positive control: own customer resolves.
	own, err := e.custRepo.GetCustomerProfile(ctx, actorOwner(e.ownerA), e.custA)
	if err != nil {
		t.Fatalf("owner A own profile: %v", err)
	}
	if own == nil || own.Customer.ID != e.custA {
		t.Fatalf("own profile id = %v, want %d", own, e.custA)
	}
}

// ── Shape 1: calendar day-view (membership over pitch rows) ───────────────────

func TestOwnerScopeRead_Calendar(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	aCal, err := e.calRepo.OwnerDayCalendar(ctx, actorOwner(e.ownerA), e.d1)
	if err != nil {
		t.Fatalf("owner A calendar: %v", err)
	}
	aPitches := map[int64]bool{}
	for _, p := range aCal.Pitches {
		aPitches[p.PitchID] = true
	}
	if !aPitches[e.pitchA] {
		t.Fatalf("owner A calendar must include own pitch %d", e.pitchA)
	}
	if aPitches[e.pitchB] {
		t.Fatalf("CRITICAL: owner A's calendar includes owner B's pitch %d", e.pitchB)
	}

	// Owner B's calendar includes B's pitch (positive control for the other side).
	bCal, err := e.calRepo.OwnerDayCalendar(ctx, actorOwner(e.ownerB), e.d2)
	if err != nil {
		t.Fatalf("owner B calendar: %v", err)
	}
	bHasB := false
	for _, p := range bCal.Pitches {
		if p.PitchID == e.pitchB {
			bHasB = true
		}
		if p.PitchID == e.pitchA {
			t.Fatalf("CRITICAL: owner B's calendar includes owner A's pitch %d", e.pitchA)
		}
	}
	if !bHasB {
		t.Fatalf("owner B calendar must include own pitch %d", e.pitchB)
	}
}

// ── Shape 1: expense list (membership) ────────────────────────────────────────

func TestOwnerScopeRead_ExpenseList(t *testing.T) {
	e := newOwnerReadEnv(t)
	ctx := context.Background()

	from := time.Now().UTC().Add(-24 * time.Hour)
	to := time.Now().UTC().Add(24 * time.Hour)

	aList, err := e.expRepo.List(ctx, actorOwner(e.ownerA), from, to, "")
	if err != nil {
		t.Fatalf("owner A expenses: %v", err)
	}
	aIDs := map[int64]bool{}
	for _, x := range aList {
		aIDs[x.ID] = true
	}
	if !aIDs[e.expA] {
		t.Fatalf("owner A must see own expense %d", e.expA)
	}
	if aIDs[e.expB] {
		t.Fatalf("CRITICAL: owner A's expense list contains owner B's expense %d", e.expB)
	}
}
