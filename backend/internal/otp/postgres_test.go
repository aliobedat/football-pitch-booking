package otp

// Round-trip tests for the Postgres-backed Store + RateLimiter. They exercise
// the real SQL against a live database and are SKIPPED unless OTP_TEST_DATABASE_URL
// is set, so the default `go test` run (and CI without a database) stays green.
// The MemoryStore tests cover the contract semantics; these confirm the SQL
// translation honours them.
//
// To run: point OTP_TEST_DATABASE_URL at a database with migration 004 applied:
//
//	OTP_TEST_DATABASE_URL=postgres://... go test ./internal/otp/ -run Postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OTP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OTP_TEST_DATABASE_URL not set; skipping Postgres OTP store integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
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

// newTestPoolSized is newTestPool but with an explicit MaxConns, so a
// concurrency test can guarantee the pool itself never serializes callers that
// should otherwise race at the database level.
func newTestPoolSized(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OTP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OTP_TEST_DATABASE_URL not set; skipping Postgres OTP store integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = maxConns
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

func TestPostgresStoreCodeRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	const phone = "+962799999001"

	// Clean slate.
	if err := store.Delete(ctx, phone); err != nil {
		t.Fatalf("delete (pre): %v", err)
	}

	// Missing code reports not-found, no error.
	if _, found, err := store.Get(ctx, phone); err != nil || found {
		t.Fatalf("Get on empty: found=%v err=%v", found, err)
	}

	now := time.Now().Truncate(time.Second)
	rec := Code{Phone: phone, Hash: "deadbeef", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, found, err := store.Get(ctx, phone)
	if err != nil || !found {
		t.Fatalf("Get after Save: found=%v err=%v", found, err)
	}
	if got.Hash != rec.Hash || got.Attempts != 0 {
		t.Fatalf("Get mismatch: %+v", got)
	}

	// Increment is monotonic.
	if n, err := store.IncrementAttempts(ctx, phone); err != nil || n != 1 {
		t.Fatalf("IncrementAttempts: n=%d err=%v", n, err)
	}
	if n, err := store.IncrementAttempts(ctx, phone); err != nil || n != 2 {
		t.Fatalf("IncrementAttempts(2): n=%d err=%v", n, err)
	}

	// Save replaces in place (resend resets attempts).
	if err := store.Save(ctx, Code{Phone: phone, Hash: "cafe", ExpiresAt: now.Add(time.Minute), CreatedAt: now}); err != nil {
		t.Fatalf("Save (resend): %v", err)
	}
	if got, _, _ := store.Get(ctx, phone); got.Hash != "cafe" || got.Attempts != 0 {
		t.Fatalf("resend did not reset: %+v", got)
	}

	// Delete clears it.
	if err := store.Delete(ctx, phone); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := store.Get(ctx, phone); found {
		t.Fatal("Get after Delete should be not-found")
	}

	// IncrementAttempts on a missing code is a typed not-found.
	if _, err := store.IncrementAttempts(ctx, phone); err != ErrCodeNotFound {
		t.Fatalf("IncrementAttempts on missing: err=%v, want ErrCodeNotFound", err)
	}
}

func TestPostgresRateLimiterWindow(t *testing.T) {
	pool := newTestPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	key := "phone:+962799999002"
	window := time.Minute
	now := time.Now()

	// Purge any leftovers from a prior run by sliding the window forward.
	_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)

	// First two admitted under a quota of 2, third rejected.
	for i := range 2 {
		ok, err := store.Allow(ctx, key, 2, window, now)
		if err != nil || !ok {
			t.Fatalf("Allow #%d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, err := store.Allow(ctx, key, 2, window, now); err != nil || ok {
		t.Fatalf("Allow over quota: ok=%v err=%v (want rejected)", ok, err)
	}

	// A request far in the future falls outside the window of the earlier events,
	// so it is admitted again.
	if ok, err := store.Allow(ctx, key, 2, window, now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("Allow after window: ok=%v err=%v", ok, err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)
}

// TestPostgresRateLimiterAllow_ConcurrencyBoundary is the WO-SECURITY-V1
// PR-S1 regression proof: it calls the ACTUAL PostgresStore.Allow (not an
// external reproduction) with 4 events pre-seeded and a limit of 5, then fires
// 20 concurrent callers at the same bucket. Before the pg_advisory_xact_lock
// fix, this proved racy (up to 20 of 20 admitted instead of exactly 1). After
// the fix, exactly one call may be admitted, the rest rejected, and the final
// event count must land exactly on the configured limit.
func TestPostgresRateLimiterAllow_ConcurrencyBoundary(t *testing.T) {
	const concurrency = 20
	pool := newTestPoolSized(t, concurrency+5)
	store := NewPostgresStore(pool)
	ctx := context.Background()

	const rounds = 5
	for round := 0; round < rounds; round++ {
		key := "concurrency-test:" + time.Now().Format(time.RFC3339Nano) + "-" +
			string(rune('a'+round))

		_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)

		now := time.Now()
		// Pre-seed exactly 4 valid events (within the window).
		for i := 0; i < 4; i++ {
			if _, err := pool.Exec(ctx,
				`INSERT INTO otp_rate_events (bucket_key, created_at) VALUES ($1, $2)`,
				key, now.Add(-time.Duration(i)*time.Second)); err != nil {
				t.Fatalf("round %d: seed insert: %v", round, err)
			}
		}

		var wg sync.WaitGroup
		admitted := make([]bool, concurrency)
		errs := make([]error, concurrency)
		start := make(chan struct{})

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				ok, err := store.Allow(ctx, key, 5, time.Hour, time.Now())
				admitted[idx] = ok
				errs[idx] = err
			}(i)
		}
		close(start)
		wg.Wait()

		admittedCount, rejectedCount, unexpectedErrCount := 0, 0, 0
		for i := 0; i < concurrency; i++ {
			if errs[i] != nil {
				// A lock_timeout under this much contention is a plausible,
				// fail-closed outcome for the advisory lock, not a bug — but it
				// must never be silently counted as an admission or rejection.
				if errors.Is(errs[i], ErrRateLimiterBusy) {
					continue
				}
				unexpectedErrCount++
				t.Errorf("round %d: unexpected error from Allow: %v", round, errs[i])
				continue
			}
			if admitted[i] {
				admittedCount++
			} else {
				rejectedCount++
			}
		}

		if unexpectedErrCount != 0 {
			t.Fatalf("round %d: %d unexpected (non-ErrRateLimiterBusy) errors", round, unexpectedErrCount)
		}
		if admittedCount != 1 {
			t.Errorf("round %d: admitted=%d, want exactly 1 (limit=5, pre-seeded=4)", round, admittedCount)
		}

		var finalCount int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM otp_rate_events WHERE bucket_key = $1`, key).Scan(&finalCount); err != nil {
			t.Fatalf("round %d: count final events: %v", round, err)
		}
		if finalCount != 5 {
			t.Errorf("round %d: final event count=%d, want exactly 5 (limit)", round, finalCount)
		}

		t.Logf("round %d: admitted=%d rejected=%d final_count=%d", round, admittedCount, rejectedCount, finalCount)

		_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)
	}
}

// TestPostgresRateLimiterAllow_AdvisoryLockTimeout proves the lock-timeout
// fail-closed path: a held advisory lock on the same bucket_key forces the
// real Allow call to time out acquiring pg_advisory_xact_lock, which must
// surface as ErrRateLimiterBusy (via errors.Is) and must NOT insert an
// otp_rate_events row for the bucket.
func TestPostgresRateLimiterAllow_AdvisoryLockTimeout(t *testing.T) {
	pool := newTestPoolSized(t, 5)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	key := "lock-timeout-test:" + time.Now().Format(time.RFC3339Nano)

	_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)

	// Hold the same advisory lock in a separate, long-lived transaction.
	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holder.Release()

	holderTx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	defer holderTx.Rollback(ctx) //nolint:errcheck

	if _, err := holderTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		t.Fatalf("holder acquire lock: %v", err)
	}

	// The real Allow call must now contend for the same lock and time out.
	before, countErr := countEvents(ctx, pool, key)
	if countErr != nil {
		t.Fatalf("count before: %v", countErr)
	}

	ok, err := store.Allow(ctx, key, 5, time.Hour, time.Now())
	if ok {
		t.Fatal("Allow reported admitted while the bucket's advisory lock was held elsewhere")
	}
	if !errors.Is(err, ErrRateLimiterBusy) {
		t.Fatalf("Allow error = %v, want errors.Is(err, ErrRateLimiterBusy)", err)
	}

	after, countErr := countEvents(ctx, pool, key)
	if countErr != nil {
		t.Fatalf("count after: %v", countErr)
	}
	if after != before {
		t.Fatalf("event count changed on lock-timeout: before=%d after=%d, want unchanged", before, after)
	}

	// Release the holder (idempotent with the deferred Rollback/Release above);
	// a normal Allow call must now succeed.
	if err := holderTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback holder tx: %v", err)
	}

	if ok, err := store.Allow(ctx, key, 5, time.Hour, time.Now()); err != nil || !ok {
		t.Fatalf("Allow after lock released: ok=%v err=%v, want ok=true err=nil", ok, err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM otp_rate_events WHERE bucket_key = $1`, key)
}

func countEvents(ctx context.Context, pool *pgxpool.Pool, key string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM otp_rate_events WHERE bucket_key = $1`, key).Scan(&n)
	return n, err
}
