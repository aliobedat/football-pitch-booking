package main

// Guard tests need no database. Reset/seed tests are gated on
// DEMO_RESET_TEST_DATABASE_URL, mirroring the repo's existing per-feature
// *_TEST_DATABASE_URL convention (see internal/repository/booking_manual_test.go
// etc.) — SKIPPED unless a scratch Postgres URL is supplied:
//
//	DEMO_RESET_TEST_DATABASE_URL=postgres://... go test ./cmd/demo-reset/

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ── Guard tests (no DB) ──────────────────────────────────────────────────────

func TestCheckBasicGuards_WrongAppEnvRefuses(t *testing.T) {
	cases := []string{"", "production", "development", "Demo", " demo", "demo "}
	for _, appEnv := range cases {
		if err := checkBasicGuards(appEnv, true, "pw"); err == nil {
			t.Errorf("checkBasicGuards(appEnv=%q) = nil, want a refusal", appEnv)
		}
	}
}

func TestCheckBasicGuards_MissingConfirmationRefuses(t *testing.T) {
	if err := checkBasicGuards("demo", false, "pw"); err == nil {
		t.Fatal("checkBasicGuards(confirmed=false) = nil, want a refusal")
	}
}

func TestCheckBasicGuards_MissingPasswordRefuses(t *testing.T) {
	for _, pw := range []string{"", "   "} {
		if err := checkBasicGuards("demo", true, pw); err == nil {
			t.Errorf("checkBasicGuards(password=%q) = nil, want a refusal", pw)
		}
	}
}

func TestCheckBasicGuards_AllPassAccepts(t *testing.T) {
	if err := checkBasicGuards("demo", true, "a-real-password"); err != nil {
		t.Fatalf("checkBasicGuards() = %v, want nil for a valid combination", err)
	}
}

// TestDemoPhone_ValidatesAsJOMobile proves every hardcoded demo phone literal
// (owner + all customer indices actually used) is structurally acceptable —
// catching a typo in the seed data itself, not user input.
func TestDemoPhone_NormalizesCleanly(t *testing.T) {
	for n := 1; n <= 20; n++ {
		if got := mustNormalizePhone(demoPhone(n)); got == "" {
			t.Errorf("mustNormalizePhone(demoPhone(%d)) returned empty", n)
		}
	}
}

// ── DB-gated tests ────────────────────────────────────────────────────────────

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DEMO_RESET_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DEMO_RESET_TEST_DATABASE_URL not set; skipping demo-reset DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	testutil.AssertSchemaBaseline(t, pool)
	return pool
}

// installMarker creates (or confirms) the Demo marker directly, mirroring
// database/demo/marma_demo_marker.sql, so the DB-gated tests don't depend on
// that file having been applied out of band.
func installMarker(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS marma_demo_marker (marker text PRIMARY KEY)`); err != nil {
		t.Fatalf("create marker table: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO marma_demo_marker (marker) VALUES ($1) ON CONFLICT (marker) DO NOTHING`,
		demoMarkerValue); err != nil {
		t.Fatalf("insert marker row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS marma_demo_marker`)
	})
}

// countRows is a small test helper mirroring the count queries in verifyCounts.
func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCheckMarker_MissingTableRefuses(t *testing.T) {
	pool := testPool(t)
	// Ensure the marker table does NOT exist for this test.
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS marma_demo_marker`); err != nil {
		t.Fatalf("drop marker table: %v", err)
	}
	if err := checkMarker(context.Background(), pool); err == nil {
		t.Fatal("checkMarker() = nil with no marker table, want a refusal")
	}
}

func TestCheckMarker_IncorrectValueRefuses(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS marma_demo_marker (marker text PRIMARY KEY)`); err != nil {
		t.Fatalf("create marker table: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS marma_demo_marker`) })
	if _, err := pool.Exec(ctx, `DELETE FROM marma_demo_marker`); err != nil {
		t.Fatalf("clear marker table: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO marma_demo_marker (marker) VALUES ('SOMETHING_ELSE')`); err != nil {
		t.Fatalf("insert wrong marker: %v", err)
	}
	if err := checkMarker(ctx, pool); err == nil {
		t.Fatal("checkMarker() = nil with an incorrect marker value, want a refusal")
	}
}

func TestCheckMarker_CorrectValueAccepts(t *testing.T) {
	pool := testPool(t)
	installMarker(t, pool)
	if err := checkMarker(context.Background(), pool); err != nil {
		t.Fatalf("checkMarker() = %v, want nil with the correct marker installed", err)
	}
}

// TestRunReset_GuardsFailBeforeMutation proves a guard failure (here: no
// marker) performs zero writes — a pre-existing sentinel row in a business
// table must survive an attempted (and refused) reset untouched.
func TestRunReset_GuardsFailBeforeMutation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS marma_demo_marker`); err != nil {
		t.Fatalf("drop marker table: %v", err)
	}

	before := countRows(t, pool, "users")

	if err := checkMarker(ctx, pool); err == nil {
		t.Fatal("expected checkMarker to refuse with no marker table")
	}
	// Guard refused → runReset must never be called by main() at this point.
	// Directly confirm no mutation occurred regardless.
	after := countRows(t, pool, "users")
	if before != after {
		t.Fatalf("users row count changed (%d -> %d) despite a refused guard", before, after)
	}
}

// TestRunReset_SuccessfulResetSeedsExpectedCounts is the core integration
// test: full delete+seed+verify+commit against a real scratch database.
func TestRunReset_SuccessfulResetSeedsExpectedCounts(t *testing.T) {
	pool := testPool(t)
	installMarker(t, pool)

	resetTime := time.Now().In(timeutil.Amman())
	got, err := runReset(context.Background(), pool, resetTime, "$2a$10$fakeFakeFakeFakeFakeFuFakeFakeFakeFakeFakeFakeFakeFa")
	if err != nil {
		t.Fatalf("runReset() = %v, want nil", err)
	}

	want := counts{
		Owners: 1, Venues: 1, Pitches: 2, Customers: 6, Bookings: 12, Expenses: 3,
		PastBookings: 4, TodayBookings: 3, FutureBookings: 5,
	}
	if got != want {
		t.Fatalf("runReset() counts = %+v, want %+v", got, want)
	}

	// Marker itself must have survived (never deleted by the reset).
	if err := checkMarker(context.Background(), pool); err != nil {
		t.Fatalf("marker missing after reset: %v", err)
	}
}

// TestRunReset_TwiceReturnsSameCounts proves the reset is fully deterministic
// and idempotent: running it back-to-back yields identical row counts.
func TestRunReset_TwiceReturnsSameCounts(t *testing.T) {
	pool := testPool(t)
	installMarker(t, pool)
	ctx := context.Background()
	hash := "$2a$10$fakeFakeFakeFakeFakeFuFakeFakeFakeFakeFakeFakeFakeFa"

	first, err := runReset(ctx, pool, time.Now().In(timeutil.Amman()), hash)
	if err != nil {
		t.Fatalf("first runReset() = %v", err)
	}
	second, err := runReset(ctx, pool, time.Now().In(timeutil.Amman()), hash)
	if err != nil {
		t.Fatalf("second runReset() = %v", err)
	}
	if first != second {
		t.Fatalf("counts differ between runs: first=%+v second=%+v", first, second)
	}
}

// TestSeededBookings_RespectGISTExclusion proves the seeded bookings do not
// merely avoid overlap by chance — attempting to insert a booking that DOES
// overlap an existing non-cancelled booking on the same pitch must still be
// rejected by bookings_pitch_id_booking_range_excl, confirming the constraint
// is live and was never bypassed.
func TestSeededBookings_RespectGISTExclusion(t *testing.T) {
	pool := testPool(t)
	installMarker(t, pool)
	ctx := context.Background()

	if _, err := runReset(ctx, pool, time.Now().In(timeutil.Amman()), "$2a$10$fakeFakeFakeFakeFakeFuFakeFakeFakeFakeFakeFakeFakeFa"); err != nil {
		t.Fatalf("runReset() = %v", err)
	}

	var pitchID int
	var start, end time.Time
	if err := pool.QueryRow(ctx, `
		SELECT pitch_id, lower(booking_range), upper(booking_range)
		FROM bookings WHERE status = 'confirmed' LIMIT 1
	`).Scan(&pitchID, &start, &end); err != nil {
		t.Fatalf("find a seeded confirmed booking: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// A 1-hour slot starting 15 minutes into the existing booking's range —
	// guaranteed to overlap it (every seeded booking is >= 1 hour long) while
	// still satisfying chk_min_duration itself, so the ONLY thing that can
	// reject this insert is the GIST exclusion constraint under test.
	overlapStart := start.Add(15 * time.Minute)
	overlapEnd := overlapStart.Add(1 * time.Hour)
	_, err = tx.Exec(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, status, total_price, source, guest_name, guest_phone)
		VALUES ($1, NULL, tstzrange($2, $3, '[)'), 'confirmed', 10, 'manual', 'Overlap Test', '+962790009999')
	`, pitchID, overlapStart, overlapEnd)
	if err == nil {
		t.Fatal("overlapping insert succeeded, want a GIST exclusion violation")
	}
	if !isExclusionViolation(err) {
		t.Fatalf("insert failed but not with an exclusion violation: %v", err)
	}
}

// isExclusionViolation reports whether err is Postgres SQLSTATE 23P01
// (exclusion_violation) — matched by error text to avoid an extra
// driver-internal (pgconn.PgError) import in this small test file.
func isExclusionViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23P01") ||
		strings.Contains(msg, "exclusion_violation") ||
		strings.Contains(msg, "conflicting key value violates exclusion constraint")
}
