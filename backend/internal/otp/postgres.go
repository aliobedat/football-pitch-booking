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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockTimeout bounds how long Allow waits to acquire the per-bucket
// advisory lock before giving up. It must be short: this lock only ever
// serializes concurrent callers of the SAME bucket key (or, rarely, two
// different keys that collide under hashtextextended), so a legitimate holder
// releases it in the time of one DELETE+COUNT+INSERT — a few milliseconds. A
// value embedded directly in SQL text because SET LOCAL does not accept a bind
// parameter for its value; it is a fixed Go constant, never user input.
const advisoryLockTimeout = "250ms"

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
		UPDATE users
		SET phone_verified    = TRUE,
		    phone_verified_at  = COALESCE(phone_verified_at, NOW()),
		    updated_at         = NOW()
		WHERE phone = $1
	`, phone); err != nil {
		return fmt.Errorf("otp/postgres: mark phone verified: %w", err)
	}
	return nil
}

// Allow implements a sliding-window rate limiter in a single transaction: it
// prunes events older than the window, counts what remains for the bucket, and
// — only while under max — records the new event. Pruning and counting share
// the (bucket_key, created_at) index.
//
// READ COMMITTED alone does not make DELETE+COUNT+INSERT atomic across
// concurrent callers of the same bucket: two transactions can each read a
// count under max and both insert, over-admitting past the limit (proven by a
// dedicated concurrency experiment — see the WO-SECURITY-V1 Gate 0B report).
// To close that race, the whole check-and-record sequence is additionally
// serialized per bucket_key with a transaction-scoped Postgres advisory lock
// (pg_advisory_xact_lock), keyed on a 64-bit hash of bucket_key so unrelated
// buckets essentially never contend and, in the rare case two different keys
// collide under the hash, they are merely serialized against each other —
// never incorrectly admitted. The lock is scoped to this transaction and is
// released automatically on commit or rollback; no unlock call is needed. A
// short lock_timeout bounds how long a caller waits for a contended bucket —
// if it expires (SQLSTATE 55P03) this fails closed: no event is recorded and
// ErrRateLimiterBusy is returned so the caller never proceeds to generate or
// dispatch a code.
func (p *PostgresStore) Allow(ctx context.Context, key string, max int, window time.Duration, now time.Time) (bool, error) {
	cutoff := now.Add(-window)

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout = '`+advisoryLockTimeout+`'`); err != nil {
		return false, fmt.Errorf("otp/postgres: rate-limit set lock_timeout: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		key,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return false, fmt.Errorf("otp/postgres: rate-limit lock timeout: %w", ErrRateLimiterBusy)
		}
		return false, fmt.Errorf("otp/postgres: rate-limit acquire lock: %w", err)
	}

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
