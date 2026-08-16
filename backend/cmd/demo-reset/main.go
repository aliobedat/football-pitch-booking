// Command demo-reset resets the DEDICATED Demo Neon database to a small,
// deterministic, realistic dataset for showing Marma to prospective pitch
// owners. It is a standalone tool — NOT wired into the API server — run
// manually, with the Demo backend stopped:
//
//	APP_ENV=demo DEMO_OWNER_PASSWORD='...' go run ./cmd/demo-reset --confirm-reset
//
// See docs/demo-reset.md for the full workflow and database/demo/marma_demo_marker.sql
// for the one-time marker install (Demo database only — NEVER Production).
//
// Safety model: every guard below MUST pass before a single DELETE or INSERT
// runs. Any guard failure prints "DEMO RESET REFUSED", exits non-zero, and
// performs zero database mutations (checked in order: cheapest/no-DB guards
// first, so a misconfigured environment never even opens a DB connection).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"

	"github.com/ali/football-pitch-api/internal/phone"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// demoMarkerValue is the exact row value database/demo/marma_demo_marker.sql
// installs, by hand, ONLY in the Demo database. This tool never writes it.
const demoMarkerValue = "MARMA_DEMO_DATABASE_ONLY"

func main() {
	confirmReset := flag.Bool("confirm-reset", false, "required: confirms intent to irreversibly reset the Demo database")
	flag.Parse()

	_ = godotenv.Load() // best-effort; real deployments set env vars directly

	appEnv := os.Getenv("APP_ENV")
	ownerPassword := os.Getenv("DEMO_OWNER_PASSWORD")

	// Guards 1-3: pure, no DB connection needed — fail fastest, before any I/O.
	if err := checkBasicGuards(appEnv, *confirmReset, ownerPassword); err != nil {
		refuse(err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		refuse(errors.New("DATABASE_URL is required"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		refuse(fmt.Errorf("invalid DATABASE_URL: %w", err))
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		refuse(fmt.Errorf("could not connect to database: %w", err))
	}
	defer pool.Close()

	// Guard 4: the Demo-only marker. Checked BEFORE any mutation. Never
	// connect-and-mutate on the strength of env vars alone — the marker is the
	// guard that actually proves this DSN points at the Demo database.
	if err := checkMarker(ctx, pool); err != nil {
		refuse(err)
	}

	resetTime := time.Now().In(timeutil.Amman())

	ownerHash, err := bcrypt.GenerateFromPassword([]byte(ownerPassword), bcryptCostFromEnv())
	if err != nil {
		log.Fatalf("DEMO RESET FAILED: hash owner password: %v", err)
	}

	report, err := runReset(ctx, pool, resetTime, string(ownerHash))
	if err != nil {
		log.Fatalf("DEMO RESET FAILED: %v", err)
	}

	printReport(report, resetTime)
	fmt.Println("DEMO RESET SUCCESSFUL")
}

// refuse prints the required refusal banner and exits non-zero. Called ONLY
// before any database mutation has been attempted.
func refuse(err error) {
	fmt.Println("DEMO RESET REFUSED")
	log.Fatalf("guard failed: %v", err)
}

// checkBasicGuards validates guards 1-3 (APP_ENV, --confirm-reset,
// DEMO_OWNER_PASSWORD). Pure function — no I/O — so it is directly unit
// testable without a database.
func checkBasicGuards(appEnv string, confirmed bool, ownerPassword string) error {
	// Deliberately an EXACT byte comparison, no trimming — "equals demo
	// exactly" per the WO. " demo", "demo ", and "Demo" must all refuse, even
	// though config.IsDemoEnv (the API's own gate) is more lenient.
	if appEnv != "demo" {
		return fmt.Errorf("APP_ENV must equal %q exactly (got %q)", "demo", appEnv)
	}
	if !confirmed {
		return errors.New("--confirm-reset flag is required")
	}
	if strings.TrimSpace(ownerPassword) == "" {
		return errors.New("DEMO_OWNER_PASSWORD must be set and non-empty")
	}
	return nil
}

// checkMarker validates guard 4: the connected database must contain the
// EXACT marker row. A missing table, a missing row, or a wrong value all
// refuse identically — the caller learns nothing about which sub-case failed
// beyond "the marker check failed", so a probing caller cannot use this tool
// to fingerprint an unknown database.
func checkMarker(ctx context.Context, pool *pgxpool.Pool) error {
	var val string
	err := pool.QueryRow(ctx,
		`SELECT marker FROM marma_demo_marker WHERE marker = $1`, demoMarkerValue,
	).Scan(&val)
	if err != nil {
		return fmt.Errorf("Demo database marker (marma_demo_marker) not found or incorrect — refusing to touch a database that cannot prove it is Demo-only: %w", err)
	}
	return nil
}

// bcryptCostFromEnv mirrors dbadmin's cost resolution (BCRYPT_COST, default
// 12, clamped to bcrypt's valid range) so Demo passwords hash identically to
// the rest of the stack.
func bcryptCostFromEnv() int {
	cost := 12
	if v := os.Getenv("BCRYPT_COST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 31 {
			cost = n
		}
	}
	return cost
}

// demoPhone builds a synthetic Jordanian-mobile-shaped E.164 number from a
// small integer suffix. All numbers share the fake +962790000xxx block — a
// structurally valid JO mobile shape (phone.ValidateJOMobile-compatible)
// that resembles no real assigned subscriber range.
func demoPhone(n int) string {
	return fmt.Sprintf("+962790000%03d", n)
}

// mustNormalizePhone panics (deliberately — this only runs over hardcoded
// demo literals, never user input) if a demo phone literal is malformed,
// catching a typo in the seed data itself rather than silently seeding a
// bad row.
func mustNormalizePhone(raw string) string {
	n, err := phone.Normalize(raw)
	if err != nil {
		panic(fmt.Sprintf("demo-reset: invalid hardcoded demo phone %q: %v", raw, err))
	}
	return n
}

// beginTx is a tiny seam so tests can share the transaction wiring.
func beginTx(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	return pool.BeginTx(ctx, pgx.TxOptions{})
}

// ── Reset ───────────────────────────────────────────────────────────────────

// demoDataTables lists every table this tool deletes from, in strict
// child-before-parent FK order (see database/schema.sql's REFERENCES
// constraints). marma_demo_marker, migration files, and every other table not
// listed here (schema/enum definitions, otp_codes, waba_daily_sends, etc.) are
// never touched — this is an explicit table list, not TRUNCATE CASCADE.
var demoDataTables = []string{
	"message_deliveries",       // job_id -> notification_jobs (SET NULL)
	"notification_jobs",        // no inbound FK from bookings
	"booking_idempotency_keys", // no enforced FK; app-usage ephemera
	"reviews",                  // (booking_id,player_id,pitch_id) -> bookings (RESTRICT)
	"status_transitions",       // booking_id -> bookings (CASCADE)
	"bookings",                 // pitch_id/player_id (RESTRICT), customer_id (SET NULL)
	"pitch_audit_log",          // actor_id -> users (SET NULL)
	"staff",                    // pitch_id/owner_id/user_id -> * (CASCADE)
	"expenses",                 // owner_id -> users (RESTRICT), pitch_id -> pitches (SET NULL)
	"operating_hours",          // pitch_id -> pitches (CASCADE)
	"customers",                // owner_id -> users (CASCADE), player_id -> users (SET NULL)
	"pitches",                  // owner_id -> users (RESTRICT), venue_id -> venues (RESTRICT)
	"venues",                   // owner_id -> users (RESTRICT)
	"refresh_tokens",           // user_id -> users (CASCADE)
	"users",                    // the demo owner (and any player rows created via live testing)
}

// counts is the small safe report printed on success — row counts only, never
// secrets, phone numbers, or connection details.
type counts struct {
	Owners, Venues, Pitches, Customers, Bookings, Expenses int
	PastBookings, TodayBookings, FutureBookings            int
}

// runReset performs the full guarded reset inside ONE transaction: delete
// every table in demoDataTables (FK-safe order), seed the deterministic
// dataset, verify the expected row counts, and only then commit. Any error at
// any step rolls back the entire transaction — the database is left exactly
// as it was before the run.
func runReset(ctx context.Context, pool *pgxpool.Pool, resetTime time.Time, ownerPasswordHash string) (counts, error) {
	tx, err := beginTx(ctx, pool)
	if err != nil {
		return counts{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after a successful Commit

	for _, table := range demoDataTables {
		if _, err := tx.Exec(ctx, `DELETE FROM `+pgIdent(table)); err != nil {
			return counts{}, fmt.Errorf("delete from %s: %w", table, err)
		}
	}

	if err := seedDemoData(ctx, tx, resetTime, ownerPasswordHash); err != nil {
		return counts{}, fmt.Errorf("seed: %w", err)
	}

	got, err := verifyCounts(ctx, tx, resetTime)
	if err != nil {
		return counts{}, fmt.Errorf("verify: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return counts{}, fmt.Errorf("commit: %w", err)
	}
	return got, nil
}

// pgIdent allow-lists table names from the hardcoded demoDataTables slice
// only — never user input — so this is not a SQL-injection seam, just a
// clean way to build `DELETE FROM <table>` without a driver placeholder
// (Postgres does not allow parameterizing identifiers).
func pgIdent(table string) string {
	if !slices.Contains(demoDataTables, table) {
		panic("pgIdent: table not in the allow-listed demoDataTables: " + table)
	}
	return `"` + table + `"`
}

// ── Seed data ───────────────────────────────────────────────────────────────

const (
	ownerFullName     = "إدارة ملاعب سما عمان"
	venueName         = "ملاعب سما عمان"
	venueSlug         = "sama-amman"
	venueNeighborhood = "عبدون"
	pitch1Name        = "ملعب سما عمان 1"
	pitch2Name        = "ملعب سما عمان 2"
	pitch1PricePerHr  = 15
	pitch2PricePerHr  = 20
)

var demoCustomerNames = []string{
	"محمد العلي",
	"سارة أحمد",
	"عمر خالد",
	"لينا يوسف",
	"يزن مراد",
	"رنا سالم",
}

// bookingSpec describes one seeded booking in terms relative to resetTime, so
// the whole dataset regenerates identically (same relative shape) on every
// run regardless of when it is executed.
type bookingSpec struct {
	dayOffset    int // days relative to resetTime's calendar day, Asia/Amman
	pitchIdx     int // 0 or 1
	startHour    int
	durationHrs  float64
	cancelled    bool
	tracked      bool // true = amount_paid set to the full total_price; false = untracked (NULL)
	customerIdx  int  // index into demoCustomerNames / seeded customer ids
}

// demoBookings: 4 past, 3 today, 5 future = 12 total, 2 cancelled, mixed
// tracked/untracked payment, both pitches used. Distinct pitch+day+hour
// combinations guarantee no overlap, so the booking_range GIST exclusion
// constraint is satisfied without ever needing to touch it.
var demoBookings = []bookingSpec{
	// past (4)
	{dayOffset: -3, pitchIdx: 0, startHour: 18, durationHrs: 1, cancelled: false, tracked: true, customerIdx: 0},
	{dayOffset: -2, pitchIdx: 1, startHour: 19, durationHrs: 1.5, cancelled: false, tracked: false, customerIdx: 1},
	{dayOffset: -1, pitchIdx: 0, startHour: 20, durationHrs: 1, cancelled: true, tracked: false, customerIdx: 2},
	{dayOffset: -1, pitchIdx: 1, startHour: 17, durationHrs: 1, cancelled: false, tracked: true, customerIdx: 3},
	// today (3)
	{dayOffset: 0, pitchIdx: 0, startHour: 16, durationHrs: 1, cancelled: false, tracked: true, customerIdx: 4},
	{dayOffset: 0, pitchIdx: 1, startHour: 18, durationHrs: 1.5, cancelled: false, tracked: false, customerIdx: 5},
	{dayOffset: 0, pitchIdx: 0, startHour: 20, durationHrs: 1, cancelled: true, tracked: false, customerIdx: 0},
	// future (5)
	{dayOffset: 1, pitchIdx: 1, startHour: 17, durationHrs: 1, cancelled: false, tracked: true, customerIdx: 1},
	{dayOffset: 2, pitchIdx: 0, startHour: 19, durationHrs: 1, cancelled: false, tracked: false, customerIdx: 2},
	{dayOffset: 3, pitchIdx: 1, startHour: 20, durationHrs: 1.5, cancelled: false, tracked: true, customerIdx: 3},
	{dayOffset: 4, pitchIdx: 0, startHour: 18, durationHrs: 1, cancelled: false, tracked: false, customerIdx: 4},
	{dayOffset: 5, pitchIdx: 1, startHour: 21, durationHrs: 1, cancelled: false, tracked: true, customerIdx: 5},
}

var demoExpenses = []struct {
	dayOffset int
	category  string
	amount    float64
	note      string
}{
	{dayOffset: -5, category: "Electricity", amount: 45.500, note: "فاتورة كهرباء الشهر"},
	{dayOffset: -3, category: "Maintenance", amount: 30.000, note: "صيانة الشبك"},
	{dayOffset: -1, category: "Water", amount: 12.750, note: "فاتورة مياه"},
}

// seedDemoData inserts the full deterministic dataset within tx, relative to
// resetTime (Asia/Amman). All dates are computed from resetTime so the
// dataset always looks "fresh" whenever reset runs.
func seedDemoData(ctx context.Context, tx pgx.Tx, resetTime time.Time, ownerPasswordHash string) error {
	loc := resetTime.Location()
	dayStart := time.Date(resetTime.Year(), resetTime.Month(), resetTime.Day(), 0, 0, 0, 0, loc)

	// 1 owner.
	ownerPhone := mustNormalizePhone(demoPhone(1))
	var ownerID int
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, phone_verified, opt_in, password_hash)
		VALUES ($1, $2, 'owner', true, true, $3)
		RETURNING id
	`, ownerFullName, ownerPhone, ownerPasswordHash).Scan(&ownerID); err != nil {
		return fmt.Errorf("insert owner: %w", err)
	}

	// 1 venue.
	var venueID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url, is_active)
		VALUES ($1, $2, $3, $4, '', true)
		RETURNING id
	`, ownerID, venueName, venueSlug, venueNeighborhood).Scan(&venueID); err != nil {
		return fmt.Errorf("insert venue: %w", err)
	}

	// 2 pitches (owner_id/venue_id both derived from the venue just created —
	// preserves the WO-VENUES ownership invariant: pitch.owner_id == venue.owner_id).
	pitchIDs := make([]int, 2)
	pitchNames := [2]string{pitch1Name, pitch2Name}
	pitchPrices := [2]int{pitch1PricePerHr, pitch2PricePerHr}
	for i := range 2 {
		var pitchID int
		if err := tx.QueryRow(ctx, `
			INSERT INTO pitches (owner_id, venue_id, name, neighborhood, surface, format, price_per_hour, is_active)
			VALUES ($1, $2, $3, $4, 'artificial_grass', 'خماسي', $5, true)
			RETURNING id
		`, ownerID, venueID, pitchNames[i], venueNeighborhood, pitchPrices[i]).Scan(&pitchID); err != nil {
			return fmt.Errorf("insert pitch %d: %w", i, err)
		}
		pitchIDs[i] = pitchID

		// Operating hours: every day of the week, 08:00-23:00.
		for weekday := range 7 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time)
				VALUES ($1, $2, '08:00', '23:00')
			`, pitchID, weekday); err != nil {
				return fmt.Errorf("insert operating hours pitch=%d weekday=%d: %w", pitchID, weekday, err)
			}
		}
	}

	// 6 synthetic customers.
	customerIDs := make([]int64, len(demoCustomerNames))
	for i, name := range demoCustomerNames {
		custPhone := mustNormalizePhone(demoPhone(10 + i))
		var customerID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers (owner_id, name, phone)
			VALUES ($1, $2, $3)
			RETURNING id
		`, ownerID, name, custPhone).Scan(&customerID); err != nil {
			return fmt.Errorf("insert customer %q: %w", name, err)
		}
		customerIDs[i] = customerID
	}

	// 12 bookings — source='manual' (owner/staff-entered walk-in), mirroring the
	// existing manual-booking insert shape (player_id NULL, guest_name required).
	for i, b := range demoBookings {
		pitchID := pitchIDs[b.pitchIdx]
		price := pitchPrices[b.pitchIdx]
		start := dayStart.AddDate(0, 0, b.dayOffset).Add(time.Duration(b.startHour) * time.Hour)
		end := start.Add(time.Duration(b.durationHrs * float64(time.Hour)))
		totalPrice := float64(price) * b.durationHrs

		status := "confirmed"
		if b.cancelled {
			status = "cancelled"
		}
		var amountPaid *float64
		if b.tracked {
			v := totalPrice
			amountPaid = &v
		}

		custName := demoCustomerNames[b.customerIdx]
		custPhone := mustNormalizePhone(demoPhone(10 + b.customerIdx))
		custID := customerIDs[b.customerIdx]

		if _, err := tx.Exec(ctx, `
			INSERT INTO bookings (
				pitch_id, player_id, booking_range, status, total_price,
				source, guest_name, guest_phone, customer_id, amount_paid
			)
			VALUES (
				$1, NULL, tstzrange($2, $3, '[)'), $4, $5,
				'manual', $6, $7, $8, $9
			)
		`, pitchID, start, end, status, totalPrice, custName, custPhone, custID, amountPaid); err != nil {
			return fmt.Errorf("insert booking %d: %w", i, err)
		}
	}

	// 3 expenses.
	for i, e := range demoExpenses {
		occurredAt := dayStart.AddDate(0, 0, e.dayOffset).Add(12 * time.Hour)
		if _, err := tx.Exec(ctx, `
			INSERT INTO expenses (owner_id, category, amount, occurred_at, note)
			VALUES ($1, $2, $3, $4, $5)
		`, ownerID, e.category, e.amount, occurredAt, e.note); err != nil {
			return fmt.Errorf("insert expense %d: %w", i, err)
		}
	}

	return nil
}

// verifyCounts re-reads every table this tool just seeded and confirms the
// row counts (and the past/today/future booking split) match exactly what
// was intended — a mismatch here means the seed silently did something other
// than expected (e.g. a constraint coerced a row), and the whole transaction
// must roll back rather than commit a dataset that doesn't match its own spec.
func verifyCounts(ctx context.Context, tx pgx.Tx, resetTime time.Time) (counts, error) {
	loc := resetTime.Location()
	dayStart := time.Date(resetTime.Year(), resetTime.Month(), resetTime.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	var c counts
	queries := []struct {
		dst   *int
		query string
		args  []any
	}{
		{&c.Owners, `SELECT count(*) FROM users WHERE role = 'owner'`, nil},
		{&c.Venues, `SELECT count(*) FROM venues`, nil},
		{&c.Pitches, `SELECT count(*) FROM pitches`, nil},
		{&c.Customers, `SELECT count(*) FROM customers`, nil},
		{&c.Bookings, `SELECT count(*) FROM bookings`, nil},
		{&c.Expenses, `SELECT count(*) FROM expenses`, nil},
		{&c.PastBookings, `SELECT count(*) FROM bookings WHERE upper(booking_range) <= $1`, []any{dayStart}},
		{&c.TodayBookings, `SELECT count(*) FROM bookings WHERE lower(booking_range) >= $1 AND lower(booking_range) < $2`, []any{dayStart, dayEnd}},
		{&c.FutureBookings, `SELECT count(*) FROM bookings WHERE lower(booking_range) >= $1`, []any{dayEnd}},
	}
	for _, q := range queries {
		if err := tx.QueryRow(ctx, q.query, q.args...).Scan(q.dst); err != nil {
			return counts{}, fmt.Errorf("count query failed (%s): %w", q.query, err)
		}
	}

	want := counts{
		Owners: 1, Venues: 1, Pitches: 2, Customers: len(demoCustomerNames),
		Bookings: len(demoBookings), Expenses: len(demoExpenses),
		PastBookings: 4, TodayBookings: 3, FutureBookings: 5,
	}
	if c != want {
		return counts{}, fmt.Errorf("row-count mismatch: got %+v, want %+v", c, want)
	}
	return c, nil
}

// printReport prints ONLY row counts and the reset timestamp — never a
// secret, a password, a hash, a connection string, or a full phone number.
func printReport(c counts, resetTime time.Time) {
	fmt.Printf("owners=%d venues=%d pitches=%d customers=%d bookings=%d expenses=%d\n",
		c.Owners, c.Venues, c.Pitches, c.Customers, c.Bookings, c.Expenses)
	fmt.Printf("bookings: past=%d today=%d future=%d\n", c.PastBookings, c.TodayBookings, c.FutureBookings)
	fmt.Printf("reset time (Asia/Amman): %s\n", resetTime.Format("2006-01-02 15:04:05 MST"))
}
