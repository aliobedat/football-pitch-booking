// Package outbox is the Postgres-backed notification queue and retry layer
// (PART 6). Every async outbound message is persisted as a job, then drained by
// a background Worker that dispatches it through the NotificationService, retries
// transient failures with exponential backoff, and terminates permanent failures
// in a dead-letter state. There is deliberately NO external broker (Redis,
// RabbitMQ): durability and ordering come from Postgres alone, per the PART 6
// constraints.
//
// The package depends on internal/notification for the message contracts and the
// payload (de)serialization, but notification does NOT depend on outbox — the
// queue is a layer ABOVE the routing core, not part of it.
package outbox

import (
	"context"
	"errors"
	"time"
)

// ErrJobNotFound is returned by Store mutations that target a missing job id.
var ErrJobNotFound = errors.New("outbox: job not found")

// Status is the lifecycle state of a queued job. See migration 005 for the
// authoritative description of each value and the CHECK constraint that mirrors
// this set.
type Status string

const (
	// StatusPending — the job is waiting for its (back-off) attempt time.
	StatusPending Status = "pending"
	// StatusProcessing — the job has been claimed by a worker and is in flight.
	StatusProcessing Status = "processing"
	// StatusSucceeded — the provider accepted the message.
	StatusSucceeded Status = "succeeded"
	// StatusDeadLetter — delivery failed and max_attempts is exhausted.
	StatusDeadLetter Status = "dead_letter"
	// StatusBlocked — delivery was refused permanently for a policy/validation
	// reason (opt-out, malformed message). Never retried, never alerted as a
	// delivery failure.
	StatusBlocked Status = "blocked"
)

// Job is one persisted outbound message and its retry bookkeeping. Envelope is
// the opaque, notification.MarshalOutbound-encoded message; the worker decodes
// it with notification.UnmarshalOutbound just before dispatch.
type Job struct {
	ID                int64
	Recipient         string
	Kind              string
	Envelope          []byte
	Status            Status
	Attempts          int
	MaxAttempts       int
	NextAttemptAt     time.Time
	LastError         string
	ProviderMessageID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewJob is the input to Store.Enqueue: a message to persist plus its retry
// budget and earliest attempt time.
type NewJob struct {
	Recipient   string
	Kind        string
	Envelope    []byte
	MaxAttempts int
	AvailableAt time.Time
}

// Store is the persistence seam for the queue. The concrete PostgresStore
// satisfies it; tests provide an in-memory fake. All claim/finalize methods
// operate by job id so the worker can drive a job through its lifecycle.
type Store interface {
	// Enqueue persists a new pending job and returns its id.
	Enqueue(ctx context.Context, j NewJob) (int64, error)

	// ClaimDue atomically claims up to limit pending jobs whose next_attempt_at
	// has arrived, transitioning each to 'processing' and incrementing its
	// attempt count. Concurrent workers never receive the same job. The returned
	// jobs already reflect the incremented Attempts.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]Job, error)

	// MarkSucceeded terminates a job as succeeded, recording the provider id.
	MarkSucceeded(ctx context.Context, id int64, providerMessageID string) error

	// Reschedule returns a job to 'pending' for a future retry after a transient
	// failure, recording the next attempt time and the error.
	Reschedule(ctx context.Context, id int64, nextAttemptAt time.Time, lastErr string) error

	// MarkDeadLetter terminates a job whose retries are exhausted.
	MarkDeadLetter(ctx context.Context, id int64, lastErr string) error

	// MarkBlocked terminates a job refused permanently for policy/validation
	// reasons (e.g. recipient opted out). Distinct from dead-letter so consent
	// blocks are not counted or alerted as delivery failures.
	MarkBlocked(ctx context.Context, id int64, reason string) error
}
