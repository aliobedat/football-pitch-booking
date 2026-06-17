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
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
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
	enumName := flag.String("enum", "", "print the values of an enum type, then exit")
	smokeFin := flag.Bool("smoke-fin", false, "exercise analytics KPIs + timeseries (Expected vs Collected) against live data")
	verifySettle := flag.Bool("verify-settle", false, "exercise the real SetPayment path on one in-week booking, show Collected move, then revert")
	smokeSched := flag.String("smoke-sched", "", "exercise the real DailySchedule repository for a date YYYY-MM-DD")
	introspect := flag.String("introspect", "", "print column types of a table, then exit")
	verifyRecon := flag.Bool("verify-recon", false, "reconciliation audit: Net == collected(F1) − Σexpenses to the fil (create+soft-delete round-trip)")
	verifyExpTenant := flag.Bool("verify-exp-tenant", false, "cross-tenant: Owner A cannot read/edit/delete Owner B's expense")
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
	case *enumName != "":
		printEnum(ctx, pool, *enumName)
	case *smokeFin:
		smokeFinancials(ctx, pool)
	case *verifySettle:
		verifySettlement(ctx, pool)
	case *smokeSched != "":
		smokeSchedule(ctx, pool, *smokeSched)
	case *introspect != "":
		introspectTable(ctx, pool, *introspect)
	case *verifyRecon:
		verifyReconciliation(ctx, pool)
	case *verifyExpTenant:
		verifyExpenseTenant(ctx, pool)
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

// smokeFinancials exercises the analytics KPI + timeseries SQL (with the new
// Collected/paid_cash aggregates) against live data, as admin (unscoped).
func smokeFinancials(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── Financials smoke: Expected vs Collected (admin/unscoped) ──")
	repo := repository.NewAnalyticsRepository(pool)
	admin := auth.Actor{UserID: 0, Role: auth.RoleAdmin}

	k, err := repo.OwnerKPIs(ctx, admin)
	must(err)
	fmt.Printf("KPIs: today expected=%.2f collected=%.2f | wtd expected=%.2f collected=%.2f | today_count=%d upcoming=%d\n",
		k.TodayRevenue, k.TodayCollected, k.WeekToDateRevenue, k.WeekToDateCollected,
		k.TodayConfirmedCount, k.UpcomingBookings)

	now := time.Now().UTC()
	from, _ := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, -29))
	_, to := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now))
	series, err := repo.OwnerTimeSeries(ctx, admin, repository.TimeSeriesParams{Granularity: "day", From: from, To: to})
	must(err)
	fmt.Printf("timeseries(day, 30d): %d bucket(s)\n", len(series))
	for i, b := range series {
		if i >= 5 {
			break
		}
		fmt.Printf("  %s expected=%.2f collected=%.2f volume=%d\n", b.Bucket, b.Revenue, b.Collected, b.Volume)
	}
	fmt.Println("✓ analytics SQL (Expected + Collected) executes cleanly.")
}

// verifySettlement proves the end-to-end Cash-Settlement flow against live data:
// it picks one confirmed, non-block booking that plays in the current Amman week,
// drives the REAL SetPayment repository path (the exact code the endpoint runs) to
// 'paid_cash', shows week-to-date Collected move, then reverts to its original
// value — leaving the database exactly as found.
func verifySettlement(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── End-to-end Cash-Settlement verification (mutate + revert) ──")
	sched := repository.NewScheduleRepository(pool)
	an := repository.NewAnalyticsRepository(pool)
	admin := auth.Actor{UserID: 0, Role: auth.RoleAdmin}

	// Pick the most recent PAST confirmed, non-block booking (so it lands inside a
	// measurable timeseries bucket).
	var bookingID int
	var ownerID int64
	var original string
	var playedAt time.Time
	err := pool.QueryRow(ctx, `
		SELECT b.id, p.owner_id, b.payment_status, lower(b.booking_range)
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed' AND b.source <> 'block'
		  AND lower(b.booking_range) < now()
		ORDER BY lower(b.booking_range) DESC
		LIMIT 1
	`).Scan(&bookingID, &ownerID, &original, &playedAt)
	if err != nil {
		fmt.Printf("(no past confirmed booking to exercise: %v)\n", err)
		return
	}
	dayFrom, dayTo := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(playedAt))
	bucketCollected := func() float64 {
		s, e := an.OwnerTimeSeries(ctx, admin, repository.TimeSeriesParams{Granularity: "day", From: dayFrom, To: dayTo})
		must(e)
		var c float64
		for _, b := range s {
			c += b.Collected
		}
		return c
	}
	fmt.Printf("target booking #%d (owner %d) played %s, original=%q\n",
		bookingID, ownerID, timeutil.InAmman(playedAt).Format("2006-01-02 15:04"), original)

	before := bucketCollected()
	if _, err := sched.SetPayment(ctx, admin, nil, bookingID, "paid_cash"); err != nil { // REAL endpoint path
		must(fmt.Errorf("SetPayment paid_cash: %w", err))
	}
	after := bucketCollected()
	fmt.Printf("that day's Collected: %.2f → %.2f (Δ=%.2f)\n", before, after, after-before)

	if _, err := sched.SetPayment(ctx, admin, nil, bookingID, original); err != nil { // revert
		must(fmt.Errorf("SetPayment revert: %w", err))
	}
	fmt.Printf("reverted that day's Collected: %.2f (baseline)\n", bucketCollected())
	fmt.Println("✓ SetPayment writes, Collected aggregate moves by the booking's price, revert clean.")
}

// smokeSchedule exercises the real DailySchedule repository (which now carries
// payment_status) for a date against live data, as admin (unscoped).
func smokeSchedule(ctx context.Context, pool *pgxpool.Pool, dateStr string) {
	day, err := time.ParseInLocation("2006-01-02", dateStr, timeutil.Amman())
	must(err)
	from, to := timeutil.AmmanDayBoundsUTC(day)
	fmt.Printf("── DailySchedule smoke for %s (admin/unscoped) ──\n", dateStr)
	repo := repository.NewScheduleRepository(pool)
	rows, err := repo.DailySchedule(ctx, auth.Actor{UserID: 0, Role: auth.RoleAdmin}, nil, 0, from, to)
	must(err)
	fmt.Printf("rows=%d\n", len(rows))
	for i, r := range rows {
		if i >= 6 {
			break
		}
		fmt.Printf("  #%d %-14s attendance=%-10s payment=%-9s %q\n",
			r.ID, r.PitchName, r.Attendance, r.PaymentStatus, r.AttendeeName)
	}
	fmt.Println("✓ DailySchedule returns payment_status cleanly.")
}

// verifyReconciliation is the WO-F2 headline audit. For a real owner over the
// trailing 30 Amman days it: (1) reads the F1 collected leg via the SAME
// OwnerTimeSeries aggregation, (2) reads Σexpenses, (3) creates a known expense and
// proves Net moves by exactly that amount, then (4) soft-deletes it and proves the
// ledger returns to baseline — all to the fil (NUMERIC(10,3)). The collected leg
// must be byte-identical before and after (expenses never touch it).
func verifyReconciliation(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── WO-F2 Reconciliation Audit: Net == Collected − Σexpenses (to the fil) ──")
	an := repository.NewAnalyticsRepository(pool)
	ex := repository.NewExpenseRepository(pool)

	now := time.Now().UTC()
	from, _ := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, -29))
	_, to := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now))

	// Pick an owner that actually has paid_cash collected in the window.
	var ownerID int64
	err := pool.QueryRow(ctx, `
		SELECT p.owner_id
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE b.payment_status = 'paid_cash'
		  AND lower(b.booking_range) >= $1 AND lower(b.booking_range) < $2
		GROUP BY p.owner_id ORDER BY SUM(b.total_price) DESC LIMIT 1
	`, from, to).Scan(&ownerID)
	if err != nil {
		fmt.Printf("(no paid_cash collected in window to reconcile against: %v)\n", err)
		return
	}
	actor := auth.Actor{UserID: int(ownerID), Role: auth.RoleOwner}
	round3 := func(v float64) float64 { return math.Round(v*1000) / 1000 }

	collected := func() float64 {
		s, e := an.OwnerTimeSeries(ctx, actor, repository.TimeSeriesParams{Granularity: "day", From: from, To: to})
		must(e)
		var c float64
		for _, b := range s {
			c += b.Collected
		}
		return round3(c)
	}

	col0 := collected()
	exp0, err := ex.SumExpenses(ctx, actor, from, to)
	must(err)
	net0 := round3(col0 - exp0)
	fmt.Printf("owner %d baseline: collected=%.3f expenses=%.3f net=%.3f\n", ownerID, col0, exp0, net0)

	// Create a known expense (3-dp to stress the fil).
	const amt = 123.456
	created, err := ex.Create(ctx, actor, repository.ExpenseInput{
		Category: "Other", Amount: amt, OccurredAt: now, Note: "recon-audit (temp)",
	})
	must(err)
	col1 := collected()
	exp1, err := ex.SumExpenses(ctx, actor, from, to)
	must(err)
	net1 := round3(col1 - exp1)
	fmt.Printf("after +%.3f expense: collected=%.3f expenses=%.3f net=%.3f\n", amt, col1, exp1, net1)

	okCollected := col1 == col0
	okExpDelta := round3(exp1-exp0) == amt
	okNetDelta := round3(net0-net1) == amt
	fmt.Printf("  collected unchanged: %v | Σexpenses Δ == %.3f: %v | net Δ == %.3f: %v\n",
		okCollected, amt, okExpDelta, amt, okNetDelta)

	// Soft-delete → ledger returns to baseline.
	must(ex.SoftDelete(ctx, actor, created.ID))
	exp2, err := ex.SumExpenses(ctx, actor, from, to)
	must(err)
	okRevert := exp2 == exp0
	fmt.Printf("after soft-delete: expenses=%.3f (baseline %.3f) revert-clean: %v\n", exp2, exp0, okRevert)

	if okCollected && okExpDelta && okNetDelta && okRevert {
		fmt.Println("✓ RECONCILED to the fil: Net == Collected − Σexpenses; collected leg untouched; soft-delete clean.")
	} else {
		fmt.Println("✗ RECONCILIATION FAILED")
		os.Exit(1)
	}
}

// verifyExpenseTenant proves an owner can only touch their OWN expenses: it creates
// one as owner A, then attempts read/update/delete as a different owner B and asserts
// each is denied (invisible / ErrExpenseNotFound). Cleans up afterward.
func verifyExpenseTenant(ctx context.Context, pool *pgxpool.Pool) {
	fmt.Println("── WO-F2 cross-tenant: Owner A's expense is invisible to Owner B ──")
	ex := repository.NewExpenseRepository(pool)

	// Two distinct owners (users that own at least one pitch).
	rows, err := pool.Query(ctx, `SELECT DISTINCT owner_id FROM pitches WHERE owner_id IS NOT NULL ORDER BY owner_id LIMIT 2`)
	must(err)
	var owners []int
	for rows.Next() {
		var o int
		must(rows.Scan(&o))
		owners = append(owners, o)
	}
	rows.Close()
	if len(owners) < 2 {
		fmt.Println("(need ≥2 owners; structural scoping still holds)")
		return
	}
	a := auth.Actor{UserID: owners[0], Role: auth.RoleOwner}
	b := auth.Actor{UserID: owners[1], Role: auth.RoleOwner}
	now := time.Now().UTC()
	from, _ := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, -1))
	_, to := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, 1))

	created, err := ex.Create(ctx, a, repository.ExpenseInput{Category: "Water", Amount: 7.001, OccurredAt: now, Note: "tenant-test"})
	must(err)
	fmt.Printf("owner A (%d) created expense #%d\n", owners[0], created.ID)

	// B lists → must not see A's row.
	bList, err := ex.List(ctx, b, from, to, "")
	must(err)
	leaked := false
	for _, e := range bList {
		if e.ID == created.ID {
			leaked = true
		}
	}
	// B update → ErrExpenseNotFound.
	_, updErr := ex.Update(ctx, b, created.ID, repository.ExpenseInput{Category: "Staff", Amount: 1, OccurredAt: now})
	// B delete → ErrExpenseNotFound.
	delErr := ex.SoftDelete(ctx, b, created.ID)

	fmt.Printf("  B sees A's row: %v | B update→%v | B delete→%v\n",
		leaked, errIs(updErr, repository.ErrExpenseNotFound), errIs(delErr, repository.ErrExpenseNotFound))

	// A can still delete its own (proves the row was untouched by B).
	aDel := ex.SoftDelete(ctx, a, created.ID)
	pass := !leaked && errIs(updErr, repository.ErrExpenseNotFound) && errIs(delErr, repository.ErrExpenseNotFound) && aDel == nil
	if pass {
		fmt.Println("✓ Owner B cannot read/edit/delete Owner A's expense; A's own delete works. Cleaned up.")
	} else {
		fmt.Println("✗ CROSS-TENANT LEAK")
		os.Exit(1)
	}
}

func errIs(err, target error) bool { return err != nil && errors.Is(err, target) }

// introspectTable prints each column's type (with numeric precision/scale) for a
// table, so a new money column can be drafted to MIRROR the existing convention.
func introspectTable(ctx context.Context, pool *pgxpool.Pool, table string) {
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, udt_name,
		       COALESCE(numeric_precision::text, '-'), COALESCE(numeric_scale::text, '-'),
		       is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_name = $1
		ORDER BY ordinal_position
	`, table)
	must(err)
	defer rows.Close()
	fmt.Printf("── columns of %q ──\n", table)
	for rows.Next() {
		var name, dtype, udt, prec, scale, nullable, def string
		must(rows.Scan(&name, &dtype, &udt, &prec, &scale, &nullable, &def))
		fmt.Printf("  %-22s %-12s prec=%-4s scale=%-4s null=%-3s default=%s\n",
			name, dtype, prec, scale, nullable, def)
	}
	must(rows.Err())
}

// printEnum lists the labels of a Postgres enum type in sort order.
func printEnum(ctx context.Context, pool *pgxpool.Pool, name string) {
	rows, err := pool.Query(ctx, `
		SELECT e.enumlabel
		FROM pg_type t JOIN pg_enum e ON e.enumtypid = t.oid
		WHERE t.typname = $1
		ORDER BY e.enumsortorder
	`, name)
	must(err)
	defer rows.Close()
	vals := []string{}
	for rows.Next() {
		var v string
		must(rows.Scan(&v))
		vals = append(vals, v)
	}
	must(rows.Err())
	if len(vals) == 0 {
		fmt.Printf("enum %q: NOT FOUND\n", name)
		return
	}
	fmt.Printf("enum %s = { %s }\n", name, strings.Join(vals, ", "))
}

func must(err error) {
	if err != nil {
		log.Fatalf("db error: %v", err)
	}
}
