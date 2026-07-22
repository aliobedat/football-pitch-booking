package outbox

// WO-SECURITY-V1 PR-S2 Part 5/6-G regression: proves the outbox worker re-enters
// the SAME guarded/decorated WhatsApp channel topology used elsewhere (no raw
// provider reference bypasses PaidWhatsAppEnabledGuard/QuotaGuardedChannel/
// FallbackChannel), and that none of the three refusal types (paid disabled,
// quota exhausted, quota datastore unavailable) crash the worker, invoke the
// provider, or trigger SMS fallback — while the booking layer (which enqueues
// via Enqueuer, never touching this channel synchronously) remains entirely
// unaffected, matching "booking success independent of notification delivery."

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// recordingWAChannel stands in for the real WhatsApp provider adapter.
type recordingWAChannel struct {
	called int
	result notification.DeliveryResult
	err    error
}

func (r *recordingWAChannel) Send(context.Context, notification.OutboundMessage) (notification.DeliveryResult, error) {
	r.called++
	return r.result, r.err
}

// recordingSMSChannel stands in for the SMS fallback target.
type recordingSMSChannel struct {
	called int
	result notification.DeliveryResult
}

func (r *recordingSMSChannel) Send(context.Context, notification.OutboundMessage) (notification.DeliveryResult, error) {
	r.called++
	return r.result, nil
}

// fakeReserveGuard implements notification.SendQuotaGuard for the outbox-level
// regression test.
type fakeReserveGuard struct {
	count int
	err   error
	calls int
}

func (f *fakeReserveGuard) Reserve(context.Context, string) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

// buildProductionShapedService composes the WhatsApp channel EXACTLY as
// cmd/api/main.go wires it (PaidWhatsAppEnabledGuard -> QuotaGuardedChannel ->
// provider, wrapped in FallbackChannel with an explicit enabled switch) and
// registers it as the single active channel of a notification.Service — the
// same Sender type main.go hands to outbox.NewWorker.
func buildProductionShapedService(wa *recordingWAChannel, sms *recordingSMSChannel, guard *fakeReserveGuard, paidEnabled, fallbackEnabled bool) *notification.Service {
	quotaGuarded := notification.NewQuotaGuardedChannel(wa, guard, "WABA1", slog.Default())
	paidGuarded := notification.NewPaidWhatsAppEnabledGuard(quotaGuarded, paidEnabled, slog.Default())
	whatsappChannel := notification.NewFallbackChannel(paidGuarded, sms, notification.WithFallbackEnabled(fallbackEnabled))
	return notification.NewService(notification.ChannelWhatsApp,
		notification.WithChannel(notification.ChannelWhatsApp, whatsappChannel))
}

// TestOutbox_UsesGuardedWhatsAppChannel_NormalSend proves an outbox-originated
// notification (enqueued exactly like a booking confirmation) flows through the
// guarded channel and is delivered when nothing refuses it.
func TestOutbox_UsesGuardedWhatsAppChannel_NormalSend(t *testing.T) {
	now := time.Now()
	store := newMemStore(withMemClock(fixedClock(now)))
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})

	wa := &recordingWAChannel{result: notification.DeliveryResult{Status: notification.DeliverySent, ProviderMessageID: "wamid.OK"}}
	sms := &recordingSMSChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	guard := &fakeReserveGuard{count: 5}
	svc := buildProductionShapedService(wa, sms, guard, true, false)

	w, _, _ := newTestWorker(store, svc, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job, _ := store.get(id)
	if job.Status != StatusSucceeded {
		t.Fatalf("status = %q, want %q", job.Status, StatusSucceeded)
	}
	if wa.called != 1 {
		t.Fatalf("provider invoked %d times, want 1 (proves the outbox reached the guarded channel)", wa.called)
	}
	if guard.calls != 1 {
		t.Fatalf("quota reserved %d times, want 1", guard.calls)
	}
	if sms.called != 0 {
		t.Fatalf("fallback invoked %d times, want 0 (no failure occurred)", sms.called)
	}
}

// TestOutbox_PaidWhatsAppDisabled_NoProviderNoFallback_JobHandledGracefully
// proves: (1) the provider is never invoked, (2) SMS fallback is never
// invoked (fallback is enabled here specifically to prove the gate refusal
// still wins), and (3) the worker handles the refusal without crashing —
// exactly the "booking success independent of notification delivery" contract,
// since a booking's own commit already happened before this job was ever
// enqueued/processed.
func TestOutbox_PaidWhatsAppDisabled_NoProviderNoFallback_JobHandledGracefully(t *testing.T) {
	now := time.Now()
	store := newMemStore(withMemClock(fixedClock(now)))
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})

	wa := &recordingWAChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	sms := &recordingSMSChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	guard := &fakeReserveGuard{count: 0}
	svc := buildProductionShapedService(wa, sms, guard, false /* paid disabled */, true /* fallback enabled */)

	w, _, _ := newTestWorker(store, svc, now)
	n, err := w.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch must not error even though the notification was refused: %v", err)
	}
	if n != 1 {
		t.Fatalf("processed %d jobs, want 1", n)
	}

	if wa.called != 0 {
		t.Fatalf("provider invoked %d times, want 0 (paid WhatsApp disabled)", wa.called)
	}
	if guard.calls != 0 {
		t.Fatalf("quota reserve invoked %d times, want 0 (guard checked before quota)", guard.calls)
	}
	if sms.called != 0 {
		t.Fatalf("fallback invoked %d times, want 0 (gate refusal, not a provider failure)", sms.called)
	}

	// The job must land in a sane terminal/retry state, not crash the batch —
	// default classification treats an unrecognised error as retryable.
	job, _ := store.get(id)
	if job.Status != StatusPending && job.Status != StatusDeadLetter {
		t.Fatalf("job status = %q, want %q or %q (graceful handling, no crash)", job.Status, StatusPending, StatusDeadLetter)
	}
}

// TestOutbox_QuotaExhausted_NoProviderNoFallback_JobHandledGracefully mirrors
// the above for quota exhaustion.
func TestOutbox_QuotaExhausted_NoProviderNoFallback_JobHandledGracefully(t *testing.T) {
	now := time.Now()
	store := newMemStore(withMemClock(fixedClock(now)))
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})

	wa := &recordingWAChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	sms := &recordingSMSChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	guard := &fakeReserveGuard{count: 251} // pinned OVER the hard cap (250 is still in-budget)
	svc := buildProductionShapedService(wa, sms, guard, true, true /* fallback enabled */)

	w, _, _ := newTestWorker(store, svc, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch must not error: %v", err)
	}

	if wa.called != 0 {
		t.Fatalf("provider invoked %d times, want 0 (quota exhausted)", wa.called)
	}
	if sms.called != 0 {
		t.Fatalf("fallback invoked %d times, want 0 (quota exhaustion is a gate refusal)", sms.called)
	}

	job, _ := store.get(id)
	if job.Status != StatusPending && job.Status != StatusDeadLetter {
		t.Fatalf("job status = %q, want %q or %q (graceful handling, no crash)", job.Status, StatusPending, StatusDeadLetter)
	}
}

// TestOutbox_QuotaDatastoreUnavailable_NoProviderNoFallback_JobHandledGracefully
// mirrors the above for a quota-datastore failure (the fail-open bug this PR
// closes) — the provider must NOT be invoked, unlike the pre-fix behavior.
func TestOutbox_QuotaDatastoreUnavailable_NoProviderNoFallback_JobHandledGracefully(t *testing.T) {
	now := time.Now()
	store := newMemStore(withMemClock(fixedClock(now)))
	id, _ := store.Enqueue(context.Background(), NewJob{
		Recipient: testRecipient, Kind: "booking_confirmed", Envelope: validEnvelope(t), MaxAttempts: 3,
	})

	wa := &recordingWAChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	sms := &recordingSMSChannel{result: notification.DeliveryResult{Status: notification.DeliverySent}}
	guard := &fakeReserveGuard{err: errors.New("db down")}
	svc := buildProductionShapedService(wa, sms, guard, true, true /* fallback enabled */)

	w, _, _ := newTestWorker(store, svc, now)
	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch must not error: %v", err)
	}

	if wa.called != 0 {
		t.Fatalf("provider invoked %d times, want 0 (quota datastore unavailable must fail CLOSED)", wa.called)
	}
	if sms.called != 0 {
		t.Fatalf("fallback invoked %d times, want 0 (quota-unavailable is a gate refusal)", sms.called)
	}

	job, _ := store.get(id)
	if job.Status != StatusPending && job.Status != StatusDeadLetter {
		t.Fatalf("job status = %q, want %q or %q (graceful handling, no crash)", job.Status, StatusPending, StatusDeadLetter)
	}
}
