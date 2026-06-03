package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─────────────────────────────────────────────────────────────────────────────
// Reminder claim (PART 7)
//
// The automated 24-hour reminder worker needs to (a) find confirmed bookings
// starting inside the next 24h that have not yet been reminded, (b) mark each
// reminded, and (c) enqueue a durable booking_reminder onto the notification
// outbox — and (b)+(c) must be ATOMIC per booking so a reminder is queued
// exactly once or not at all. All three steps therefore run in ONE transaction
// here, in the repository, where the SQL and the transaction boundary live.
//
// The claim uses SELECT ... FOR UPDATE SKIP LOCKED so multiple worker instances
// (future horizontal scaling) never pick the same booking: a row another worker
// has locked is simply skipped rather than blocked on.
//
// The notification_jobs INSERT is the only place outside the outbox package that
// writes that table; it is duplicated here deliberately, because folding the
// enqueue into the booking-update transaction is the whole point — the outbox
// Enqueuer runs against the pool, not this tx, so it cannot provide atomicity.
// ─────────────────────────────────────────────────────────────────────────────

// DueReminder is a confirmed booking eligible for its 24h reminder, joined with
// the contact details a notification needs (the player's E.164 phone and the
// pitch name).
type DueReminder struct {
	BookingID int64
	Phone     string
	PitchName string
	StartTime time.Time
	EndTime   time.Time
}

// ReminderJob is the durable outbox row to enqueue for a due reminder. The
// caller (the reminder worker) builds it — marshalling the channel-agnostic
// notification envelope — so the repository stays free of notification payload
// knowledge and only persists opaque bytes, exactly as the outbox does.
type ReminderJob struct {
	Recipient   string
	Kind        string
	Envelope    []byte
	MaxAttempts int
}

// ReminderBuildFunc turns a claimed booking into its outbox job. It is invoked
// inside the claim transaction, once per due booking; returning an error aborts
// the whole batch (the transaction rolls back, so nothing is marked reminded).
type ReminderBuildFunc func(DueReminder) (ReminderJob, error)

// ReminderRepository claims due bookings and atomically marks them reminded
// while enqueuing their reminder messages onto the outbox.
type ReminderRepository interface {
	// ClaimDueReminders claims up to limit confirmed, not-yet-reminded bookings
	// whose start falls in the window (now, now+horizon], locking them with
	// FOR UPDATE SKIP LOCKED. For each, build produces the outbox job; the
	// booking is flipped reminder_sent=true and the job inserted into
	// notification_jobs — all in one transaction. It returns how many bookings
	// were processed.
	ClaimDueReminders(ctx context.Context, now time.Time, horizon time.Duration, limit int, build ReminderBuildFunc) (int, error)
}

type reminderRepo struct {
	db *pgxpool.Pool
}

// NewReminderRepository builds a Postgres-backed ReminderRepository.
func NewReminderRepository(db *pgxpool.Pool) ReminderRepository {
	return &reminderRepo{db: db}
}

// defaultReminderMaxAttempts mirrors the outbox default so a reminder job gets
// the same retry budget as any other queued message when the caller omits one.
const defaultReminderMaxAttempts = 5

func (r *reminderRepo) ClaimDueReminders(
	ctx context.Context,
	now time.Time,
	horizon time.Duration,
	limit int,
	build ReminderBuildFunc,
) (int, error) {

	if limit <= 0 {
		return 0, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("ClaimDueReminders: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// booking_range stores UTC wall-clock instants typed as `timestamp` (no tz);
	// compare against the same representation by passing the UTC bounds and
	// casting to ::timestamp, mirroring how CreateBooking inserts the range.
	lower := now.UTC()
	upper := lower.Add(horizon)

	rows, err := tx.Query(ctx, `
		SELECT b.id,
		       lower(b.booking_range) AS start_time,
		       upper(b.booking_range) AS end_time,
		       u.phone,
		       COALESCE(p.name, '') AS pitch_name
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		JOIN users   u ON u.id = b.player_id
		WHERE b.status = 'confirmed'
		  AND b.reminder_sent = FALSE
		  AND u.phone IS NOT NULL AND u.phone <> ''
		  AND lower(b.booking_range) >  $1::timestamp
		  AND lower(b.booking_range) <= $2::timestamp
		ORDER BY lower(b.booking_range)
		LIMIT $3
		FOR UPDATE OF b SKIP LOCKED
	`, lower, upper, limit)
	if err != nil {
		return 0, fmt.Errorf("ClaimDueReminders: claim query: %w", err)
	}

	// Drain all locked rows into memory before issuing further statements on this
	// transaction's single connection (pgx forbids interleaving). The FOR UPDATE
	// locks are held until commit regardless of when the cursor is closed.
	var due []DueReminder
	for rows.Next() {
		var d DueReminder
		if err := rows.Scan(&d.BookingID, &d.StartTime, &d.EndTime, &d.Phone, &d.PitchName); err != nil {
			rows.Close()
			return 0, fmt.Errorf("ClaimDueReminders: scan: %w", err)
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("ClaimDueReminders: rows error: %w", err)
	}

	for _, d := range due {
		job, err := build(d)
		if err != nil {
			return 0, fmt.Errorf("ClaimDueReminders: build job for booking %d: %w", d.BookingID, err)
		}

		maxAttempts := job.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = defaultReminderMaxAttempts
		}

		// Mark reminded and enqueue in the SAME transaction — the once-only guard
		// and the durable job commit together or not at all.
		if _, err := tx.Exec(ctx, `
			UPDATE bookings SET reminder_sent = TRUE WHERE id = $1
		`, d.BookingID); err != nil {
			return 0, fmt.Errorf("ClaimDueReminders: mark booking %d reminded: %w", d.BookingID, err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO notification_jobs (recipient, kind, envelope, max_attempts, status)
			VALUES ($1, $2, $3, $4, 'pending')
		`, job.Recipient, job.Kind, job.Envelope, maxAttempts); err != nil {
			return 0, fmt.Errorf("ClaimDueReminders: enqueue reminder for booking %d: %w", d.BookingID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("ClaimDueReminders: commit: %w", err)
	}

	return len(due), nil
}
