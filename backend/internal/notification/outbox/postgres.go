package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store + DeliveryStore, backing the queue with
// the notification_jobs and message_deliveries tables from migration 005.
type PostgresStore struct {
	db *pgxpool.Pool
}

// NewPostgresStore constructs a Postgres-backed outbox store.
func NewPostgresStore(db *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{db: db}
}

// Compile-time assertions that PostgresStore satisfies both seams.
var (
	_ Store         = (*PostgresStore)(nil)
	_ DeliveryStore = (*PostgresStore)(nil)
)

// Enqueue inserts a new pending job and returns its id.
func (p *PostgresStore) Enqueue(ctx context.Context, j NewJob) (int64, error) {
	available := j.AvailableAt
	if available.IsZero() {
		available = time.Now()
	}
	maxAttempts := j.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	var id int64
	err := p.db.QueryRow(ctx, `
		INSERT INTO notification_jobs (recipient, kind, envelope, max_attempts, next_attempt_at, status)
		VALUES ($1, $2, $3, $4, $5, 'pending')
		RETURNING id
	`, j.Recipient, j.Kind, j.Envelope, maxAttempts, available).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("outbox/postgres: enqueue: %w", err)
	}
	return id, nil
}

// ClaimDue atomically claims up to limit due pending jobs. The inner SELECT ...
// FOR UPDATE SKIP LOCKED reserves rows no other worker can take; the outer
// UPDATE flips them to 'processing' and bumps attempts in the same statement, so
// a claimed job's Attempts already reflects the attempt about to be made.
func (p *PostgresStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	rows, err := p.db.Query(ctx, `
		UPDATE notification_jobs
		SET    status = 'processing', attempts = attempts + 1, updated_at = NOW()
		WHERE  id IN (
			SELECT id FROM notification_jobs
			WHERE  status = 'pending' AND next_attempt_at <= $1
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, recipient, kind, envelope, status, attempts, max_attempts,
		          next_attempt_at, COALESCE(last_error, ''), COALESCE(provider_message_id, ''),
		          created_at, updated_at
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox/postgres: claim due: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.Recipient, &j.Kind, &j.Envelope, &j.Status, &j.Attempts, &j.MaxAttempts,
			&j.NextAttemptAt, &j.LastError, &j.ProviderMessageID, &j.CreatedAt, &j.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox/postgres: scan claimed job: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox/postgres: claim due rows: %w", err)
	}
	return jobs, nil
}

// MarkSucceeded terminates a job as succeeded.
func (p *PostgresStore) MarkSucceeded(ctx context.Context, id int64, providerMessageID string) error {
	return p.terminate(ctx, id, `
		UPDATE notification_jobs
		SET    status = 'succeeded', provider_message_id = $2, last_error = NULL, updated_at = NOW()
		WHERE  id = $1
	`, id, providerMessageID)
}

// Reschedule returns a job to pending for a future retry.
func (p *PostgresStore) Reschedule(ctx context.Context, id int64, nextAttemptAt time.Time, lastErr string) error {
	return p.terminate(ctx, id, `
		UPDATE notification_jobs
		SET    status = 'pending', next_attempt_at = $2, last_error = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, nextAttemptAt, lastErr)
}

// MarkDeadLetter terminates a job whose retries are exhausted.
func (p *PostgresStore) MarkDeadLetter(ctx context.Context, id int64, lastErr string) error {
	return p.terminate(ctx, id, `
		UPDATE notification_jobs
		SET    status = 'dead_letter', last_error = $2, updated_at = NOW()
		WHERE  id = $1
	`, id, lastErr)
}

// MarkBlocked terminates a job refused permanently for policy/validation reasons.
func (p *PostgresStore) MarkBlocked(ctx context.Context, id int64, reason string) error {
	return p.terminate(ctx, id, `
		UPDATE notification_jobs
		SET    status = 'blocked', last_error = $2, updated_at = NOW()
		WHERE  id = $1
	`, id, reason)
}

// terminate runs a single-row state mutation and maps a zero-row result to
// ErrJobNotFound, so a mutation against a missing/duplicate-finalised job is a
// detectable error rather than a silent no-op.
func (p *PostgresStore) terminate(ctx context.Context, id int64, sql string, args ...any) error {
	tag, err := p.db.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("outbox/postgres: update job %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrJobNotFound
	}
	return nil
}

// ── DeliveryStore ───────────────────────────────────────────────────────────

// RecordSent upserts a 'sent' delivery row for a provider message id, linking it
// to the originating job. It never downgrades a row that a webhook has already
// advanced to a later status — the UPDATE branch only refreshes the job linkage,
// leaving the status as-is.
func (p *PostgresStore) RecordSent(ctx context.Context, providerMessageID string, jobID *int64, recipient string) error {
	_, err := p.db.Exec(ctx, `
		INSERT INTO message_deliveries (provider_message_id, job_id, recipient, status)
		VALUES ($1, $2, $3, 'sent')
		ON CONFLICT (provider_message_id) DO UPDATE SET
			job_id     = COALESCE(message_deliveries.job_id, EXCLUDED.job_id),
			recipient  = COALESCE(message_deliveries.recipient, EXCLUDED.recipient),
			updated_at = NOW()
	`, providerMessageID, jobID, recipient)
	if err != nil {
		return fmt.Errorf("outbox/postgres: record sent: %w", err)
	}
	return nil
}

// ApplyStatus upserts a delivery-status update from a webhook callback. The row
// is keyed on provider_message_id so the callback can arrive before or after the
// worker's own RecordSent.
func (p *PostgresStore) ApplyStatus(ctx context.Context, u DeliveryUpdate) error {
	var errCode *int
	if u.ErrorCode != 0 {
		errCode = &u.ErrorCode
	}
	var errTitle *string
	if u.ErrorTitle != "" {
		errTitle = &u.ErrorTitle
	}
	var recipient *string
	if u.Recipient != "" {
		recipient = &u.Recipient
	}

	_, err := p.db.Exec(ctx, `
		INSERT INTO message_deliveries (provider_message_id, recipient, status, error_code, error_title, raw)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider_message_id) DO UPDATE SET
			status      = EXCLUDED.status,
			recipient   = COALESCE(message_deliveries.recipient, EXCLUDED.recipient),
			error_code  = EXCLUDED.error_code,
			error_title = EXCLUDED.error_title,
			raw         = EXCLUDED.raw,
			updated_at  = NOW()
	`, u.ProviderMessageID, recipient, string(u.Status), errCode, errTitle, rawOrNil(u.Raw))
	if err != nil {
		return fmt.Errorf("outbox/postgres: apply status: %w", err)
	}
	return nil
}

// rawOrNil returns nil for empty raw bytes so the JSONB column stores SQL NULL
// rather than an invalid empty document.
func rawOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
