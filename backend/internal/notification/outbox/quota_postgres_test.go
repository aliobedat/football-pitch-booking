package outbox

// WO-SECURITY-V1 PR-S2 Part 6.I: proves the existing atomic waba_daily_sends
// counter still admits no more than the configured cap under concurrent
// reservations — this PR changes ONLY the caller's fail-open/fail-closed and
// fallback-eligibility behavior, never the counter design/schema/semantics
// (per strict scope: "do not change the atomic PostgreSQL counter design").
//
// Skipped unless QUOTA_TEST_DATABASE_URL is set, so the default `go test` run
// (and CI without a database) stays green — same convention as
// internal/otp/postgres_test.go.
//
//	QUOTA_TEST_DATABASE_URL=postgres://... go test ./internal/notification/outbox/ -run QuotaStore

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newQuotaTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("QUOTA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("QUOTA_TEST_DATABASE_URL not set; skipping Postgres quota-store integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = 30
	cfg.MinConns = 25
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestQuotaStore_ConcurrentReserve_NoLostIncrements fires many concurrent
// Reserve calls against the SAME (wabaID, day) bucket and asserts every
// returned count is unique — i.e., the atomic INSERT ... ON CONFLICT DO UPDATE
// never loses an increment and never hands out a duplicate count, which is
// exactly the property QuotaGuardedChannel's cap check (count >= quotaHardCap)
// depends on to admit no more than the configured cap under real concurrency.
func TestQuotaStore_ConcurrentReserve_NoLostIncrements(t *testing.T) {
	pool := newQuotaTestPool(t)
	wabaID := "gate1-pr-s2-concurrency-test"
	fixedDay := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)

	_, _ = pool.Exec(context.Background(), `DELETE FROM waba_daily_sends WHERE waba_id = $1`, wabaID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM waba_daily_sends WHERE waba_id = $1`, wabaID)
	})

	store := NewQuotaStore(pool).WithClock(func() time.Time { return fixedDay })

	const concurrency = 25
	var wg sync.WaitGroup
	counts := make([]int, concurrency)
	errs := make([]error, concurrency)
	start := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			c, err := store.Reserve(context.Background(), wabaID)
			counts[idx] = c
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[int]int) // count -> how many callers got it
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Reserve #%d errored: %v", i, err)
		}
		seen[counts[i]]++
	}
	for count, n := range seen {
		if n != 1 {
			t.Fatalf("count %d was returned to %d concurrent callers, want exactly 1 (lost/duplicated increment)", count, n)
		}
	}
	if len(seen) != concurrency {
		t.Fatalf("got %d distinct counts, want %d (some increments were lost)", len(seen), concurrency)
	}

	var finalCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count FROM waba_daily_sends WHERE waba_id = $1 AND send_date = $2`,
		wabaID, fixedDay).Scan(&finalCount); err != nil {
		t.Fatalf("read final count: %v", err)
	}
	if finalCount != concurrency {
		t.Fatalf("final stored count = %d, want %d", finalCount, concurrency)
	}
}
