package otp

// PostgresStore is the production implementation of both Store and RateLimiter,
// backing the OTP service with durable Postgres state instead of the in-memory
// maps in memory.go. It is the persistence half deferred from PART 3A and wired
// up in PART 3B. The service core is unchanged: it depends only on the Store /
// RateLimiter contracts, so swapping MemoryStore for PostgresStore is purely a
// construction-time decision.
//
// Schema (migration 004): otp_codes holds at most one active code per phone
// (phone PRIMARY KEY); otp_rate_events is an append-only event log the limiter
// counts within a sliding window. MarkPhoneVerified writes users.phone_verified.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore satisfies Store and RateLimiter against a pgx connection pool.
type PostgresStore struct {
	db *pgxpool.Pool
}

// NewPostgresStore constructs a Postgres-backed OTP store/limiter.
func NewPostgresStore(db *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{db: db}
}

// Compile-time assertions that PostgresStore satisfies both seams.
var (
	_ Store       = (*PostgresStore)(nil)
	_ RateLimiter = (*PostgresStore)(nil)
)

// Save upserts the active code for code.Phone, replacing any existing one and
// resetting its attempt count (a resend invalidates the previous code).
func (p *PostgresStore) Save(ctx context.Context, code Code) error {
	_, err := p.db.Exec(ctx, `
		INSERT INTO otp_codes (phone, code_hash, expires_at, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (phone) DO UPDATE SET
			code_hash  = EXCLUDED.code_hash,
			expires_at = EXCLUDED.expires_at,
			attempts   = EXCLUDED.attempts,
			created_at = EXCLUDED.created_at
	`, code.Phone, code.Hash, code.ExpiresAt, code.Attempts, code.CreatedAt)
	if err != nil {
		return fmt.Errorf("otp/postgres: save code: %w", err)
	}
	return nil
}

// Get returns the active code for phone. The bool is false when none exists.
func (p *PostgresStore) Get(ctx context.Context, phone string) (Code, bool, error) {
	var c Code
	err := p.db.QueryRow(ctx, `
		SELECT phone, code_hash, expires_at, attempts, created_at
		FROM   otp_codes
		WHERE  phone = $1
	`, phone).Scan(&c.Phone, &c.Hash, &c.ExpiresAt, &c.Attempts, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Code{}, false, nil
		}
		return Code{}, false, fmt.Errorf("otp/postgres: get code: %w", err)
	}
	return c, true, nil
}

// Delete removes the active code for phone (no error if absent).
func (p *PostgresStore) Delete(ctx context.Context, phone string) error {
	if _, err := p.db.Exec(ctx, `DELETE FROM otp_codes WHERE phone = $1`, phone); err != nil {
		return fmt.Errorf("otp/postgres: delete code: %w", err)
	}
	return nil
}

// IncrementAttempts atomically bumps and returns the failed-attempt count for
// phone's active code. It returns ErrCodeNotFound if none exists. The UPDATE ...
// RETURNING performs the read-modify-write in a single statement, so concurrent
// failed verifications cannot lose an increment.
func (p *PostgresStore) IncrementAttempts(ctx context.Context, phone string) (int, error) {
	var attempts int
	err := p.db.QueryRow(ctx, `
		UPDATE otp_codes
		SET    attempts = attempts + 1
		WHERE  phone = $1
		RETURNING attempts
	`, phone).Scan(&attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCodeNotFound
		}
		return 0, fmt.Errorf("otp/postgres: increment attempts: %w", err)
	}
	return attempts, nil
}

// MarkPhoneVerified sets users.phone_verified = true for phone. A phone with no
// user row yet (verify racing ahead of the request-time upsert) updates zero
// rows and is not an error — the HTTP layer ensures the verified user exists.
func (p *PostgresStore) MarkPhoneVerified(ctx context.Context, phone string) error {
	if _, err := p.db.Exec(ctx, `
		UPDATE users SET phone_verified = TRUE, updated_at = NOW() WHERE phone = $1
	`, phone); err != nil {
		return fmt.Errorf("otp/postgres: mark phone verified: %w", err)
	}
	return nil
}

// Allow implements a sliding-window rate limiter in a single transaction: it
// prunes events older than the window, counts what remains for the bucket, and
// — only while under max — records the new event. Pruning and counting share
// the (bucket_key, created_at) index. The whole check runs in one tx so a burst
// of concurrent requests cannot each read a stale count and over-admit.
func (p *PostgresStore) Allow(ctx context.Context, key string, max int, window time.Duration, now time.Time) (bool, error) {
	cutoff := now.Add(-window)

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx,
		`DELETE FROM otp_rate_events WHERE bucket_key = $1 AND created_at <= $2`,
		key, cutoff,
	); err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit prune: %w", err)
	}

	var count int
	if err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM otp_rate_events WHERE bucket_key = $1 AND created_at > $2`,
		key, cutoff,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit count: %w", err)
	}

	if count >= max {
		// Commit so the prune is persisted even though we reject.
		if err = tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("otp/postgres: rate-limit commit (rejected): %w", err)
		}
		return false, nil
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO otp_rate_events (bucket_key, created_at) VALUES ($1, $2)`,
		key, now,
	); err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit record: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit commit: %w", err)
	}
	return true, nil
}
