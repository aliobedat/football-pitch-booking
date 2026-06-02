package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// Default retry budget for enqueued jobs when the caller does not override it.
const defaultMaxAttempts = 5

// Enqueuer is a notification.NotificationChannel that, instead of delivering
// synchronously, PERSISTS each message to the outbox for the Worker to drain.
// Because it satisfies the same Send contract as the channels and the Service,
// it drops into any seam that expects a notifier — notably booking.Service —
// turning best-effort fire-and-forget notifications into durable, retried ones
// without changing the caller.
//
// Enqueuer does NOT evaluate the opt-out/opt-in gates: those live in the Service
// the Worker dispatches through, so a queued message for an opted-out recipient
// is refused (and the job blocked) at dispatch time, in exactly one place.
type Enqueuer struct {
	store       Store
	maxAttempts int
	now         func() time.Time
}

var _ notification.NotificationChannel = (*Enqueuer)(nil)

// EnqueuerOption configures an Enqueuer.
type EnqueuerOption func(*Enqueuer)

// WithMaxAttempts sets the retry budget stamped on each enqueued job.
func WithMaxAttempts(n int) EnqueuerOption {
	return func(e *Enqueuer) {
		if n > 0 {
			e.maxAttempts = n
		}
	}
}

// WithEnqueueClock overrides the time source (tests inject a fake clock).
func WithEnqueueClock(now func() time.Time) EnqueuerOption {
	return func(e *Enqueuer) {
		if now != nil {
			e.now = now
		}
	}
}

// NewEnqueuer builds an Enqueuer backed by the given Store.
func NewEnqueuer(store Store, opts ...EnqueuerOption) *Enqueuer {
	e := &Enqueuer{store: store, maxAttempts: defaultMaxAttempts, now: time.Now}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Send serialises the message and persists it as a pending job available
// immediately. It returns a DeliveryQueued result whose ProviderMessageID
// references the job id — delivery itself happens asynchronously in the Worker.
// A malformed message is rejected synchronously so the caller learns of the
// programming error rather than queuing an undeliverable job.
func (e *Enqueuer) Send(ctx context.Context, msg notification.OutboundMessage) (notification.DeliveryResult, error) {
	envelope, err := notification.MarshalOutbound(msg)
	if err != nil {
		return notification.DeliveryResult{Status: notification.DeliveryFailed, Err: err}, err
	}

	id, err := e.store.Enqueue(ctx, NewJob{
		Recipient:   msg.Recipient,
		Kind:        string(msg.Kind),
		Envelope:    envelope,
		MaxAttempts: e.maxAttempts,
		AvailableAt: e.now(),
	})
	if err != nil {
		wrapped := fmt.Errorf("outbox: enqueue: %w", err)
		return notification.DeliveryResult{Status: notification.DeliveryFailed, Err: wrapped}, wrapped
	}

	return notification.DeliveryResult{
		Status:            notification.DeliveryQueued,
		ProviderMessageID: fmt.Sprintf("job:%d", id),
	}, nil
}
