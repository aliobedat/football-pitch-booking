package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QuotaStore is the Postgres-backed daily WhatsApp send counter (migration 028).
// It implements notification.SendQuotaGuard — the quota-guard decorator calls
// Reserve once per gated WhatsApp send to count it against the WABA's UTC-day
// bucket. The atomic upsert makes concurrent reservations race-safe: each caller
// gets a distinct, monotonic count with no lost increments.
type QuotaStore struct {
	db  *pgxpool.Pool
	now func() time.Time
}

// NewQuotaStore builds the counter over the shared pool. The clock defaults to
// time.Now (UTC is applied inside Reserve); tests inject a fixed clock.
func NewQuotaStore(db *pgxpool.Pool) *QuotaStore {
	return &QuotaStore{db: db, now: time.Now}
}

// WithClock overrides the time source (tests pin "today").
func (q *QuotaStore) WithClock(now func() time.Time) *QuotaStore {
	if now != nil {
		q.now = now
	}
	return q
}

// Reserve atomically counts one send against (wabaID, today-UTC) and returns the
// resulting count. The INSERT ... ON CONFLICT DO UPDATE is a single statement, so
// concurrent reservers serialize on the row and each observes a unique RETURNING
// value — no increment is ever lost. The increment is unconditional (it counts
// refused-over-cap attempts too), matching the agreed contract.
func (q *QuotaStore) Reserve(ctx context.Context, wabaID string) (int, error) {
	day := q.now().UTC().Truncate(24 * time.Hour) // 00:00:00 UTC of today
	var count int
	err := q.db.QueryRow(ctx, `
		INSERT INTO waba_daily_sends (waba_id, send_date, count)
		VALUES ($1, $2, 1)
		ON CONFLICT (waba_id, send_date) DO UPDATE
			SET count = waba_daily_sends.count + 1
		RETURNING count
	`, wabaID, day).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("outbox/quota: reserve (waba=%s): %w", wabaID, err)
	}
	return count, nil
}
