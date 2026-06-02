package outbox

// Worker lifecycle tests: each scenario enqueues a job, runs one ProcessBatch
// against a stubbed Sender, and asserts the job reached the correct terminal (or
// rescheduled) state — plus the delivery-store / failure-monitor side effects.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// fixedClock returns a clock function pinned to t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newTestWorker builds a worker over the given store/sender with a fixed clock,
// a delivery store, and a permissive failure monitor whose alert calls are
// captured via the returned counter pointer.
func newTestWorker(store Store, sender Sender, now time.Time) (*Worker, *memDeliveries, *int) {
	deliveries := &memDeliveries{}
	alerts := new(int)
	monitor := NewFailureMonitor(time.Hour, 0.5, 1,
		WithMonitorClock(fixedClock(now)),
		WithAlertFunc(func(int, int, float64) { *alerts++ }),
	)
	w := NewWorker(store, sender, Config{BaseDelay: time.Minute, MaxDelay: time.Hour},
		WithDeliveryStore(deliveries),
		WithFailureMonitor(monitor),
		WithClock(fixedClock(now)),
	)
	return w, deliveries, alerts
}

func TestWorker_Success(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})
	sender := &stubSender{result: notification.DeliveryResult{Status: notification.DeliverySent, ProviderMessageID: "wamid.123"}}

	w, deliveries, alerts := newTestWorker(store, sender, now)
	n, err := w.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if n != 1 {
		t.Fatalf("processed %d jobs, want 1", n)
	}

	job, _ := store.get(id)
	if job.Status != StatusSucceeded {
		t.Errorf("status = %q, want %q", job.Status, StatusSucceeded)
	}
	if job.ProviderMessageID != "wamid.123" {
		t.Errorf("provider id = %q, want wamid.123", job.ProviderMessageID)
	}
	if deliveries.sentCount() != 1 {
		t.Errorf("recorded %d 'sent' deliveries, want 1", deliveries.sentCount())
	}
	if *alerts != 0 {
		t.Errorf("alerts fired = %d on success, want 0", *alerts)
	}
}

func TestWorker_TransientFailure_Reschedules(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})
	// A generic error is, by default, transient → retry with backoff.
	sender := &stubSender{err: errors.New("provider 503"), result: notification.DeliveryResult{Status: notification.DeliveryFailed}}

	w, _, alerts := newTestWorker(store, sender, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusPending {
		t.Fatalf("status = %q, want %q (rescheduled)", job.Status, StatusPending)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", job.Attempts)
	}
	// First retry waits one BaseDelay (1 minute) from now.
	wantNext := now.Add(time.Minute)
	if !job.NextAttemptAt.Equal(wantNext) {
		t.Errorf("next_attempt_at = %s, want %s", job.NextAttemptAt, wantNext)
	}
	if job.LastError == "" {
		t.Error("last_error not recorded on reschedule")
	}
	if *alerts != 1 {
		t.Errorf("alerts fired = %d on transient failure, want 1", *alerts)
	}
}

func TestWorker_ExhaustedAttempts_DeadLetters(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	// MaxAttempts=1: the single claim bumps attempts to 1 (== max), so a failure
	// has no budget left and must dead-letter rather than reschedule.
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 1,
	})
	sender := &stubSender{err: errors.New("provider 503")}

	w, _, alerts := newTestWorker(store, sender, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusDeadLetter {
		t.Fatalf("status = %q, want %q", job.Status, StatusDeadLetter)
	}
	if *alerts != 1 {
		t.Errorf("alerts fired = %d on dead-letter, want 1", *alerts)
	}
}

func TestWorker_OptedOut_Blocks_NoAlert(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})
	// Service refuses an opted-out recipient: a permanent policy block, NOT a
	// delivery failure — it must not be retried and must not trip the alarm.
	sender := &stubSender{err: notification.ErrOptedOut, result: notification.DeliveryResult{Status: notification.DeliveryFailed}}

	w, deliveries, alerts := newTestWorker(store, sender, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", job.Status, StatusBlocked)
	}
	if deliveries.sentCount() != 0 {
		t.Errorf("recorded %d deliveries for a blocked job, want 0", deliveries.sentCount())
	}
	if *alerts != 0 {
		t.Errorf("alerts fired = %d on consent block, want 0 (not a delivery failure)", *alerts)
	}
}

func TestWorker_UnknownChannel_DeadLettersImmediately(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	// MaxAttempts is generous, but an unregistered channel is a deployment fault
	// that can never self-heal per-recipient → dead-letter at once, no retries.
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 5,
	})
	sender := &stubSender{err: notification.ErrUnknownChannel}

	w, _, alerts := newTestWorker(store, sender, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusDeadLetter {
		t.Fatalf("status = %q, want %q", job.Status, StatusDeadLetter)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retries burned)", job.Attempts)
	}
	if *alerts != 1 {
		t.Errorf("alerts fired = %d, want 1", *alerts)
	}
}

func TestWorker_UndecodableEnvelope_Blocks(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: []byte("}{not json"), MaxAttempts: 3,
	})
	sender := &stubSender{} // never reached

	w, _, _ := newTestWorker(store, sender, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", job.Status, StatusBlocked)
	}
	if sender.calls != 0 {
		t.Errorf("sender called %d times for an undecodable job, want 0", sender.calls)
	}
}

func TestWorker_SkipsJobsNotYetDue(t *testing.T) {
	now := time.Now()
	store := newMemStore()
	// Available one hour in the future — ClaimDue must not pick it up yet.
	store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t),
		MaxAttempts: 3, AvailableAt: now.Add(time.Hour),
	})
	sender := &stubSender{result: notification.DeliveryResult{Status: notification.DeliverySent}}

	w, _, _ := newTestWorker(store, sender, now)
	n, err := w.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if n != 0 {
		t.Errorf("processed %d jobs, want 0 (job is not due)", n)
	}
	if sender.calls != 0 {
		t.Errorf("sender called %d times, want 0", sender.calls)
	}
}

func TestBackoff(t *testing.T) {
	base, max := time.Minute, 10*time.Minute
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, time.Minute},      // clamped to at least 1
		{1, time.Minute},      // first retry: base
		{2, 2 * time.Minute},  // base * 2
		{3, 4 * time.Minute},  // base * 4
		{4, 8 * time.Minute},  // base * 8
		{5, 10 * time.Minute}, // base * 16 → capped at max
		{9, 10 * time.Minute}, // far past cap
	}
	for _, c := range cases {
		if got := backoff(base, max, c.attempts); got != c.want {
			t.Errorf("backoff(attempts=%d) = %s, want %s", c.attempts, got, c.want)
		}
	}
}
