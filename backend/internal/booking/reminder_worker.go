package booking

// PART 7 scope: the automated 24-hour reminder worker. It is a lightweight
// background runner that periodically asks the ReminderStore to claim confirmed
// bookings starting within the next 24h that have not yet been reminded, mark
// them reminded, and enqueue a durable booking_reminder onto the notification
// outbox — all atomically per booking, inside one transaction owned by the
// store. The worker itself holds NO SQL and NO transaction: it builds the
// channel-agnostic notification envelope for each claimed booking and lets the
// store persist it, so the notification-abstraction boundary is preserved.
//
// Concurrency safety for future horizontal scaling lives in the store's
// SELECT ... FOR UPDATE SKIP LOCKED claim, not here: any number of these workers
// may run against one database without sending a reminder twice.

import (
	"context"
	"log"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/repository"
)

// ReminderStore is the persistence seam the reminder worker drains through. The
// concrete repository.ReminderRepository satisfies it; tests provide an
// in-memory fake.
type ReminderStore interface {
	ClaimDueReminders(ctx context.Context, now time.Time, horizon time.Duration, limit int, build repository.ReminderBuildFunc) (int, error)
}

// Compile-time guarantee that the production repository satisfies ReminderStore.
var _ ReminderStore = (repository.ReminderRepository)(nil)

// Default reminder tunables. Conservative by design; production wiring may
// override them.
const (
	defaultReminderPollInterval = 15 * time.Minute
	defaultReminderHorizon      = 24 * time.Hour
	defaultReminderBatchSize    = 100
	defaultReminderMaxAttempts  = 5
)

// ReminderConfig tunes the reminder worker's cadence and the reminder window.
type ReminderConfig struct {
	PollInterval time.Duration // how often to scan for due bookings (default 15m)
	Horizon      time.Duration // remind bookings starting within this window (default 24h)
	BatchSize    int           // max bookings claimed per scan (default 100)
	MaxAttempts  int           // retry budget stamped on each enqueued reminder job (default 5)
}

func (c ReminderConfig) withDefaults() ReminderConfig {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultReminderPollInterval
	}
	if c.Horizon <= 0 {
		c.Horizon = defaultReminderHorizon
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultReminderBatchSize
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultReminderMaxAttempts
	}
	return c
}

// ReminderWorker periodically enqueues 24h reminders for due confirmed bookings.
type ReminderWorker struct {
	store  ReminderStore
	cfg    ReminderConfig
	logger *log.Logger
	now    func() time.Time
}

// ReminderOption configures a ReminderWorker.
type ReminderOption func(*ReminderWorker)

// WithReminderLogger overrides the worker's logger. Defaults to log.Default().
func WithReminderLogger(l *log.Logger) ReminderOption {
	return func(w *ReminderWorker) {
		if l != nil {
			w.logger = l
		}
	}
}

// WithReminderClock overrides the time source (tests inject a fake clock).
func WithReminderClock(now func() time.Time) ReminderOption {
	return func(w *ReminderWorker) {
		if now != nil {
			w.now = now
		}
	}
}

// NewReminderWorker builds a ReminderWorker over the given store.
func NewReminderWorker(store ReminderStore, cfg ReminderConfig, opts ...ReminderOption) *ReminderWorker {
	w := &ReminderWorker{
		store:  store,
		cfg:    cfg.withDefaults(),
		logger: log.Default(),
		now:    time.Now,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run scans for due reminders on a ticker until ctx is cancelled, returning
// ctx.Err() on shutdown so the caller can log a clean exit. A scan error is
// logged and the loop continues — a transient DB blip must not kill the worker.
func (w *ReminderWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	w.logger.Printf("[REMINDER] worker started (poll=%s horizon=%s batch=%d)",
		w.cfg.PollInterval, w.cfg.Horizon, w.cfg.BatchSize)

	for {
		// Scan eagerly on entry so startup does not wait a full tick.
		if n, err := w.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
			w.logger.Printf("[REMINDER] scan error: %v", err)
		} else if n > 0 {
			w.logger.Printf("[REMINDER] enqueued %d reminder(s)", n)
		}
		select {
		case <-ctx.Done():
			w.logger.Printf("[REMINDER] worker stopping: %v", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProcessBatch claims and enqueues one batch of due reminders, returning how
// many bookings were processed. It is exported so tests can drive the worker
// deterministically without the ticker, and so a caller can trigger an immediate
// scan.
func (w *ReminderWorker) ProcessBatch(ctx context.Context) (int, error) {
	return w.store.ClaimDueReminders(ctx, w.now(), w.cfg.Horizon, w.cfg.BatchSize, w.buildJob)
}

// buildJob turns a claimed booking into its durable outbox job: a UTILITY-
// category booking_reminder addressed to the player, with the booking
// coordinates the reminder template echoes. Marshalling here keeps all
// notification-payload knowledge on this side of the store seam.
func (w *ReminderWorker) buildJob(d repository.DueReminder) (repository.ReminderJob, error) {
	msg := notification.OutboundMessage{
		Recipient: d.Phone,
		Kind:      notification.KindBookingReminder,
		Payload: notification.BookingReminderPayload{
			BookingID: d.BookingID,
			PitchName: d.PitchName,
			StartTime: d.StartTime,
			EndTime:   d.EndTime,
		},
	}
	envelope, err := notification.MarshalOutbound(msg)
	if err != nil {
		return repository.ReminderJob{}, err
	}
	return repository.ReminderJob{
		Recipient:   d.Phone,
		Kind:        string(notification.KindBookingReminder),
		Envelope:    envelope,
		MaxAttempts: w.cfg.MaxAttempts,
	}, nil
}
