package outbox

import (
	"context"
	"log"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// Sender is the dispatch seam the Worker drains jobs through. *notification.Service
// satisfies it, so the queue routes every message through the same opt-out /
// opt-in gates and active channel (including the WhatsApp→SMS FallbackChannel)
// as the synchronous path — the worker adds durability and retry on top, it does
// not re-implement routing.
type Sender interface {
	Send(ctx context.Context, msg notification.OutboundMessage) (notification.DeliveryResult, error)
}

// Default worker tunables. They are deliberately conservative; production wiring
// overrides them from config/env.
const (
	defaultPollInterval = 5 * time.Second
	defaultBatchSize    = 20
	defaultBaseDelay    = 30 * time.Second
	defaultMaxDelay     = time.Hour
)

// Config tunes the worker's polling cadence and backoff schedule.
type Config struct {
	PollInterval time.Duration // how often to scan for due jobs
	BatchSize    int           // max jobs claimed per scan
	BaseDelay    time.Duration // first retry delay; doubles each subsequent attempt
	MaxDelay     time.Duration // cap on the exponential backoff
}

func (c Config) withDefaults() Config {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = defaultBaseDelay
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = defaultMaxDelay
	}
	return c
}

// Worker drains the outbox: it claims due jobs, dispatches each through the
// Sender, and drives the job to a terminal state (succeeded / blocked /
// dead_letter) or reschedules it with exponential backoff. It is safe to run
// multiple workers against one Store — ClaimDue guarantees exclusive claims.
type Worker struct {
	store      Store
	sender     Sender
	deliveries DeliveryStore   // optional: records 'sent' rows for webhook linkage
	monitor    *FailureMonitor // optional: failure-rate alerting
	cfg        Config
	logger     *log.Logger
	now        func() time.Time
}

// WorkerOption configures a Worker.
type WorkerOption func(*Worker)

// WithDeliveryStore links successful sends into message_deliveries so status
// webhooks can later advance them. Optional.
func WithDeliveryStore(d DeliveryStore) WorkerOption {
	return func(w *Worker) { w.deliveries = d }
}

// WithFailureMonitor installs the elevated-failure-rate alerter. Optional.
func WithFailureMonitor(m *FailureMonitor) WorkerOption {
	return func(w *Worker) { w.monitor = m }
}

// WithLogger overrides the worker's logger. Defaults to log.Default().
func WithLogger(l *log.Logger) WorkerOption {
	return func(w *Worker) {
		if l != nil {
			w.logger = l
		}
	}
}

// WithClock overrides the time source (tests inject a fake clock).
func WithClock(now func() time.Time) WorkerOption {
	return func(w *Worker) {
		if now != nil {
			w.now = now
		}
	}
}

// NewWorker builds a Worker over the given Store and Sender.
func NewWorker(store Store, sender Sender, cfg Config, opts ...WorkerOption) *Worker {
	w := &Worker{
		store:  store,
		sender: sender,
		cfg:    cfg.withDefaults(),
		logger: log.Default(),
		now:    time.Now,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run drains the queue on a ticker until ctx is cancelled. It returns ctx.Err()
// when the context ends, so the caller can log a clean shutdown. Errors from an
// individual scan are logged and the loop continues — a transient DB blip must
// not kill the worker.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	w.logger.Printf("[OUTBOX] worker started (poll=%s batch=%d base=%s max=%s)",
		w.cfg.PollInterval, w.cfg.BatchSize, w.cfg.BaseDelay, w.cfg.MaxDelay)

	for {
		// Drain eagerly on entry so startup does not wait a full tick.
		if _, err := w.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
			w.logger.Printf("[OUTBOX] scan error: %v", err)
		}
		select {
		case <-ctx.Done():
			w.logger.Printf("[OUTBOX] worker stopping: %v", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProcessBatch claims and processes one batch of due jobs, returning how many it
// handled. It is exported so tests can drive the worker deterministically
// without the ticker, and so a caller can trigger an immediate drain.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	jobs, err := w.store.ClaimDue(ctx, w.now(), w.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	for i := range jobs {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		w.process(ctx, jobs[i])
	}
	return len(jobs), nil
}

// process dispatches a single claimed job and drives it to its next state.
func (w *Worker) process(ctx context.Context, job Job) {
	msg, err := notification.UnmarshalOutbound(job.Envelope)
	if err != nil {
		// A job whose payload cannot be decoded can never be delivered.
		w.logger.Printf("[OUTBOX] job %d: undecodable envelope, blocking: %v", job.ID, err)
		_ = w.store.MarkBlocked(ctx, job.ID, "undecodable envelope: "+err.Error())
		return
	}

	res, sendErr := w.sender.Send(ctx, msg)

	// Success: a nil error and a non-failed status. Mirrors the convention used
	// across the notification channels.
	if sendErr == nil && res.Status != notification.DeliveryFailed {
		if err := w.store.MarkSucceeded(ctx, job.ID, res.ProviderMessageID); err != nil {
			w.logger.Printf("[OUTBOX] job %d: mark succeeded failed: %v", job.ID, err)
			return
		}
		w.recordSent(ctx, job, res.ProviderMessageID)
		w.recordOutcome(false)
		return
	}

	// Failure: prefer the returned error, falling back to the result's Err.
	failErr := sendErr
	if failErr == nil {
		failErr = res.Err
	}
	reason := "delivery failed"
	if failErr != nil {
		reason = failErr.Error()
	}

	switch classify(failErr) {
	case dispositionBlocked:
		w.logger.Printf("[OUTBOX] job %d (kind=%s): blocked permanently: %s", job.ID, job.Kind, reason)
		_ = w.store.MarkBlocked(ctx, job.ID, reason)
		// A consent/validation block is not a delivery failure — do not alert.

	case dispositionDeadLetter:
		w.logger.Printf("[OUTBOX] job %d (kind=%s): permanent failure, dead-lettering: %s", job.ID, job.Kind, reason)
		_ = w.store.MarkDeadLetter(ctx, job.ID, reason)
		w.recordOutcome(true)

	default: // dispositionRetry
		if job.Attempts >= job.MaxAttempts {
			w.logger.Printf("[OUTBOX] job %d (kind=%s): exhausted %d attempts, dead-lettering: %s",
				job.ID, job.Kind, job.Attempts, reason)
			_ = w.store.MarkDeadLetter(ctx, job.ID, reason)
		} else {
			next := w.now().Add(backoff(w.cfg.BaseDelay, w.cfg.MaxDelay, job.Attempts))
			w.logger.Printf("[OUTBOX] job %d (kind=%s): attempt %d/%d failed, retrying at %s: %s",
				job.ID, job.Kind, job.Attempts, job.MaxAttempts, next.Format(time.RFC3339), reason)
			_ = w.store.Reschedule(ctx, job.ID, next, reason)
		}
		w.recordOutcome(true)
	}
}

// recordSent best-effort links a delivered provider id into message_deliveries
// so a later status webhook can advance it. A failure here never fails the job.
func (w *Worker) recordSent(ctx context.Context, job Job, providerMessageID string) {
	if w.deliveries == nil || providerMessageID == "" {
		return
	}
	id := job.ID
	if err := w.deliveries.RecordSent(ctx, providerMessageID, &id, job.Recipient); err != nil {
		w.logger.Printf("[OUTBOX] job %d: record delivery 'sent' failed: %v", job.ID, err)
	}
}

func (w *Worker) recordOutcome(failed bool) {
	if w.monitor != nil {
		w.monitor.Record(failed)
	}
}

// backoff returns the delay before the next attempt: an exponential schedule
// base * 2^(attempts-1), capped at max. attempts is the number of attempts
// ALREADY made (the claim that just failed counts), so the first retry waits
// base, the second 2*base, and so on.
func backoff(base, max time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := base
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}
