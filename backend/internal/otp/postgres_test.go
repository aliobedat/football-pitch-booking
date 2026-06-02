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
	"os"
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
