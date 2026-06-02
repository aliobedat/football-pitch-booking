package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

func TestEnqueuer_Send_PersistsJob(t *testing.T) {
	store := newMemStore()
	enq := NewEnqueuer(store, WithMaxAttempts(7))

	start := time.Now()
	msg := notification.OutboundMessage{
		Recipient: testRecipient,
		Kind:      notification.KindBookingConfirmed,
		Payload: notification.BookingConfirmedPayload{
			BookingID: 42, PitchName: "Pitch A", StartTime: start, EndTime: start.Add(time.Hour),
		},
	}

	res, err := enq.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Status != notification.DeliveryQueued {
		t.Errorf("status = %q, want %q", res.Status, notification.DeliveryQueued)
	}
	if res.ProviderMessageID != "job:1" {
		t.Errorf("provider id = %q, want job:1", res.ProviderMessageID)
	}

	job, ok := store.get(1)
	if !ok {
		t.Fatal("no job persisted")
	}
	if job.Status != StatusPending {
		t.Errorf("persisted status = %q, want %q", job.Status, StatusPending)
	}
	if job.Recipient != testRecipient || job.Kind != string(notification.KindBookingConfirmed) {
		t.Errorf("denormalised fields wrong: recipient=%q kind=%q", job.Recipient, job.Kind)
	}
	if job.MaxAttempts != 7 {
		t.Errorf("max_attempts = %d, want 7", job.MaxAttempts)
	}

	// The stored envelope must round-trip back to the original message.
	got, err := notification.UnmarshalOutbound(job.Envelope)
	if err != nil {
		t.Fatalf("unmarshal stored envelope: %v", err)
	}
	if got.Kind != msg.Kind || got.Recipient != msg.Recipient {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestEnqueuer_Send_RejectsInvalidMessage(t *testing.T) {
	store := newMemStore()
	enq := NewEnqueuer(store)

	// Nil payload fails validation in MarshalOutbound — must be rejected
	// synchronously, never persisted as an undeliverable job.
	res, err := enq.Send(context.Background(), notification.OutboundMessage{
		Recipient: testRecipient, Kind: notification.KindBookingConfirmed, Payload: nil,
	})
	if !errors.Is(err, notification.ErrInvalidMessage) {
		t.Fatalf("err = %v, want errors.Is(_, ErrInvalidMessage)", err)
	}
	if res.Status != notification.DeliveryFailed {
		t.Errorf("status = %q, want %q", res.Status, notification.DeliveryFailed)
	}
	if _, ok := store.get(1); ok {
		t.Error("an invalid message was persisted; it must be rejected before enqueue")
	}
}

func TestEnqueuer_SatisfiesChannel(t *testing.T) {
	// Compile-time intent, asserted at runtime for clarity: an Enqueuer drops in
	// wherever a NotificationChannel is expected (e.g. booking.Service).
	var _ notification.NotificationChannel = NewEnqueuer(newMemStore())
}
