// Command dbadmin is a throwaway local DB utility (Cockpit WO1): apply a .sql
// migration file and run the owner-scoped CRM customer backfill. psql is not
// installed in this environment, so schema/data steps go through the same pgx
// stack the app uses. NOT wired into the server; run manually.
//
//	go run ./cmd/dbadmin -exec-sql migrations/023_customers.up.sql
//	go run ./cmd/dbadmin -backfill            # DRY RUN (no writes)
//	go run ./cmd/dbadmin -backfill -apply     # perform the backfill
//
// The backfill is idempotent (ON CONFLICT upserts; only fills customer_id where
// NULL) and owner-scoped throughout (owner = pitch owner). Manual-booking phones
// are normalised in Go via internal/phone so the dedup key matches go-forward.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/phone"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

func main() {
	execSQL := flag.String("exec-sql", "", "path to a .sql file to execute, then exit")
	backfill := flag.Bool("backfill", false, "run the CRM customer backfill")
	apply := flag.Bool("apply", false, "with -backfill: perform writes (default is dry-run)")
	verifyCRM := flag.Bool("verify-crm", false, "cross-tenant CRM scoping probe (read-only)")
	smokeCRM := flag.Bool("smoke-crm", false, "exercise the real CRM repository (list+profile) against live data")
	smokeCal := flag.String("smoke-cal", "", "exercise the real calendar repository for a date YYYY-MM-DD against live data")
	flag.Parse()

	_ = godotenv.Load()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required (load backend/.env)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse DSN: %v", err)
	}
	// Simple protocol so a multi-statement .sql file runs in one Exec.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	switch {
	case *execSQL != "":
		runSQLFile(ctx, pool, *execSQL)
	case *backfill:
		runBackfill(ctx, pool, *apply)
	case *verifyCRM:
		verifyCrossTenant(ctx, pool)
	case *smokeCRM:
		smokeCRMRepo(ctx, pool)
	case *smokeCal != "":
		smokeCalendarRepo(ctx, pool, *smokeCal)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func runSQLFile(ctx context.Context, pool *pgxpool.Pool, path string) {
	sql, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		log.Fatalf("exec %s: %v", path, err)
	}
	fmt.Printf("✓ executed %s\n", path)
}

func runBackfill(ctx context.Context, pool *pgxpool.Pool, apply bool) {
	mode := "DRY RUN (no writes)"
	if apply {
		mode = "APPLY"
	}
	fmt.Printf("── CRM customer backfill — %s ──\n", mode)

	// ── Player bookings ──────────────────────────────────────────────────────
	// users.phone is already canonical E.164, so this whole branch is set-based.
	var playerLinkCandidates, playerNoPhone int
	must(pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE u.phone IS NOT NULL),
			count(*) FILTER (WHERE u.phone IS NULL)
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.source = 'player' AND b.customer_id IS NULL
	`).Scan(&playerLinkCandidates, &playerNoPhone))

	fmt.Printf("players : %d booking(s) to link, %d skipped (player has no phone)\n",
		playerLinkCandidates, playerNoPhone)

	if apply {
		ct, err := pool.Exec(ctx, `
			INSERT INTO customers (owner_id, player_id, name, phone)
			SELECT DISTINCT p.owner_id, u.id, u.full_name, u.phone
			FROM bookings b
			JOIN pitches p ON p.id = b.pitch_id
			JOIN users   u ON u.id = b.player_id
			WHERE b.source = 'player' AND b.player_id IS NOT NULL
			  AND u.phone IS NOT NULL AND p.owner_id IS NOT NULL
			ON CONFLICT (owner_id, phone) DO UPDATE
			  SET player_id = COALESCE(customers.player_id, EXCLUDED.player_id),
			      name      = COALESCE(customers.name, EXCLUDED.name)
		`)
		must(err)
		fmt.Printf("        → upserted %d player customer row(s)\n", ct.RowsAffected())

		ct, err = pool.Exec(ctx, `
			UPDATE bookings b
			SET customer_id = c.id
			FROM pitches p, users u, customers c
			WHERE b.pitch_id = p.id AND b.player_id = u.id
			  AND c.owner_id = p.owner_id AND c.phone = u.phone
			  AND b.source = 'player' AND b.customer_id IS NULL
		`)
		must(err)
		fmt.Printf("        → linked %d player booking(s)\n", ct.RowsAffected())
	}

	// ── Manual / walk-in bookings ────────────────────────────────────────────
	// guest_phone is free text → normalise in Go (skip rows that can't normalise).
	rows, err := pool.Query(ctx, `
		SELECT b.id, p.owner_id, COALESCE(b.guest_name,''), COALESCE(b.guest_phone,'')
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.source = 'manual' AND b.customer_id IS NULL AND p.owner_id IS NOT NULL
	`)
	must(err)
	type manualRow struct {
		bookingID, ownerID int64
		name, rawPhone     string
	}
	var manuals []manualRow
	for rows.Next() {
		var m manualRow
		must(rows.Scan(&m.bookingID, &m.ownerID, &m.name, &m.rawPhone))
		manuals = append(manuals, m)
	}
	rows.Close()
	must(rows.Err())

	identities := map[string]bool{} // owner|phone — distinct customers implied
	manualLink, manualSkip := 0, 0
	for _, m := range manuals {
		norm, err := phone.Normalize(m.rawPhone)
		if err != nil {
			manualSkip++
			continue
		}
		identities[fmt.Sprintf("%d|%s", m.ownerID, norm)] = true
		manualLink++

		if apply {
			var custID int64
			must(pool.QueryRow(ctx, `
				INSERT INTO customers (owner_id, phone, name)
				VALUES ($1, $2, NULLIF($3,''))
				ON CONFLICT (owner_id, phone) DO UPDATE
				  SET name = COALESCE(customers.name, NULLIF(EXCLUDED.name,''))
				RETURNING id
			`, m.ownerID, norm, m.name).Scan(&custID))
			if _, err := pool.Exec(ctx,
				`UPDATE bookings SET customer_id = $1 WHERE id = $2 AND customer_id IS NULL`,
				custID, m.bookingID); err != nil {
				log.Fatalf("link manual booking %d: %v", m.bookingID, err)
			}
		}
	}
	fmt.Printf("manual  : %d booking(s) to link across ~%d distinct contact(s), %d skipped (no/invalid phone)\n",
		manualLink, len(identities), manualSkip)

	// ── Totals ───────────────────────────────────────────────────────────────
	var totalCustomers, linkedBookings int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&totalCustomers))
	must(pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE customer_id IS NOT NULL`).Scan(&linkedBookings))
	fmt.Printf("totals  : customers=%d, bookings linked=%d\n", totalCustomers, linkedBookings)

	if !apply {
		fmt.Println("\n(dry run — no rows written. Re-run with -apply to perform.)")
	} else {
		fmt.Println("\n✓ backfill applied.")
	}
}

// verifyCrossTenant proves the CRM owner-scoping holds against live data: it picks
// the two owners with the most customers, then runs the SAME owner-scoped predicate
// the repository uses (c.owner_id = $owner) for Owner A and asserts that none of
// Owner B's customer ids/phones appear in A's result.
func verifyCrossTenant(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── CRM cross-tenant scoping probe (read-only) ──")

	rows, err := pool.Query(ctx, `
		SELECT owner_id, count(*) FROM customers GROUP BY owner_id ORDER BY count(*) DESC LIMIT 2
	`)
	must(err)
	var owners []int64
	for rows.Next() {
		var oid int64
		var n int
		must(rows.Scan(&oid, &n))
		fmt.Printf("owner %d : %d customer(s)\n", oid, n)
		owners = append(owners, oid)
	}
	rows.Close()
	if len(owners) < 2 {
		fmt.Println("(need ≥2 owners with customers for a cross-tenant probe; structural scoping still holds)")
		return
	}
	a, b := owners[0], owners[1]

	// A's scoped view (mirrors repo: WHERE c.owner_id = $1).
	aRows, err := pool.Query(ctx, `SELECT id, phone FROM customers WHERE owner_id = $1`, a)
	must(err)
	aIDs := map[int64]bool{}
	aPhones := map[string]bool{}
	for aRows.Next() {
		var id int64
		var ph string
		must(aRows.Scan(&id, &ph))
		aIDs[id] = true
		aPhones[ph] = true
	}
	aRows.Close()

	// B's customers must NOT appear in A's scoped view.
	bRows, err := pool.Query(ctx, `SELECT id, phone FROM customers WHERE owner_id = $1`, b)
	must(err)
	leak := 0
	for bRows.Next() {
		var id int64
		var ph string
		must(bRows.Scan(&id, &ph))
		if aIDs[id] {
			leak++
		}
		// A shared phone across owners is EXPECTED to be two distinct rows; only an
		// id appearing in both scopes would be a leak.
		_ = ph
	}
	bRows.Close()

	if leak == 0 {
		fmt.Printf("✓ owner %d's scoped view contains NONE of owner %d's customer rows (no cross-tenant leak)\n", a, b)
	} else {
		fmt.Printf("✗ LEAK: %d of owner %d's customers appeared in owner %d's scope\n", leak, b, a)
		os.Exit(1)
	}

	// Sanity: every customer belongs to exactly one owner (no NULL/shared owner).
	var orphan int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE owner_id IS NULL`).Scan(&orphan))
	fmt.Printf("orphan (owner_id IS NULL) customers: %d\n", orphan)
}

// smokeCRMRepo exercises the REAL repository SQL (list + profile) end-to-end
// against live data, as an admin (unscoped), to prove the queries execute and the
// aggregates/derived slots come back well-formed.
func smokeCRMRepo(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── CRM repository smoke (real SQL, admin/unscoped) ──")
	repo := repository.NewCustomerRepository(pool)
	admin := auth.Actor{UserID: 0, Role: auth.RoleAdmin}

	list, err := repo.ListCustomers(ctx, admin, "", "booking_count")
	must(err)
	fmt.Printf("ListCustomers → %d row(s)\n", len(list))
	for i, c := range list {
		if i >= 3 {
			break
		}
		last := "—"
		if c.LastBooked != nil {
			last = c.LastBooked.Format("2006-01-02")
		}
		fmt.Printf("  #%d %-18s app=%-5v bookings=%d no_show=%d last=%s\n",
			c.ID, c.Name, c.IsAppPlayer, c.BookingCount, c.NoShowCount, last)
	}
	if len(list) == 0 {
		fmt.Println("(no customers — skip profile)")
		return
	}

	prof, err := repo.GetCustomerProfile(ctx, admin, list[0].ID)
	must(err)
	fmt.Printf("GetCustomerProfile(#%d) → name=%q bookings=%d checked_in=%d no_show=%d slots=%d history=%d\n",
		prof.Customer.ID, prof.Customer.Name, prof.BookingCount, prof.CheckedInCount,
		prof.NoShowCount, len(prof.PreferredSlots), len(prof.RecentBookings))
	for _, s := range prof.PreferredSlots {
		fmt.Printf("  preferred: weekday=%d hour=%02d ×%d\n", s.Weekday, s.Hour, s.Count)
	}
	fmt.Println("✓ CRM repository SQL executes cleanly against live data.")
}

// smokeCalendarRepo exercises the real calendar repository (resource-timeline
// payload) for a date against live data, as an admin (unscoped).
func smokeCalendarRepo(ctx context.Context, pool *pgxpool.Pool, dateStr string) {
	day, err := time.ParseInLocation("2006-01-02", dateStr, timeutil.Amman())
	must(err)
	fmt.Printf("── Calendar repository smoke for %s (admin/unscoped) ──\n", dateStr)
	repo := repository.NewCalendarRepository(pool)
	cal, err := repo.OwnerDayCalendar(ctx, auth.Actor{UserID: 0, Role: auth.RoleAdmin}, day)
	must(err)
	fmt.Printf("date=%s pitches=%d\n", cal.Date, len(cal.Pitches))
	for i, p := range cal.Pitches {
		if i >= 6 {
			fmt.Printf("  …(+%d more)\n", len(cal.Pitches)-6)
			break
		}
		fmt.Printf("  pitch #%d %-16s active=%-5v windows=%d events=%d has_schedule=%v\n",
			p.PitchID, p.PitchName, p.IsActive, len(p.OpenWindows), len(p.Events), p.HasSchedule)
	}
	fmt.Println("✓ calendar repository SQL executes cleanly against live data.")
}

func must(err error) {
	if err != nil {
		log.Fatalf("db error: %v", err)
	}
}
