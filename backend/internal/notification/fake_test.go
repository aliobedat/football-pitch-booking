package notification

import (
	"context"
	"sync"
	"testing"
)

// TestFakeChannel_SendEachKind confirms the Fake adapter accepts every message
// kind directly (no opt-in gate at the channel layer — that lives in Service),
// reports success, and returns a non-empty provider id.
func TestFakeChannel_SendEachKind(t *testing.T) {
	kinds := []MessageKind{KindOTP, KindBookingConfirmed, KindBookingRejected, KindBookingCancelled}
	fake := NewFakeChannel(FakeSilent())

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			res, err := fake.Send(context.Background(), sampleMessage(kind))
			if err != nil {
				t.Fatalf("Send(%s) error: %v", kind, err)
			}
			if res.Status != DeliverySent {
				t.Errorf("Status = %q, want %q", res.Status, DeliverySent)
			}
			if res.ProviderMessageID == "" {
				t.Error("ProviderMessageID is empty")
			}
		})
	}

	if fake.Count() != len(kinds) {
		t.Fatalf("Count() = %d, want %d", fake.Count(), len(kinds))
	}
}

// TestFakeChannel_ProviderIDsUnique guards against a degenerate id generator: a
// batch of sends must produce distinct provider message ids.
func TestFakeChannel_ProviderIDsUnique(t *testing.T) {
	fake := NewFakeChannel(FakeSilent())
	seen := make(map[string]struct{})

	const n = 200
	for i := range n {
		res, err := fake.Send(context.Background(), sampleMessage(KindBookingConfirmed))
		if err != nil {
			t.Fatalf("Send error: %v", err)
		}
		if _, dup := seen[res.ProviderMessageID]; dup {
			t.Fatalf("duplicate ProviderMessageID %q on iteration %d", res.ProviderMessageID, i)
		}
		seen[res.ProviderMessageID] = struct{}{}
	}
}

// TestFakeChannel_StoreAccessors exercises Sent/Last/Reset.
func TestFakeChannel_StoreAccessors(t *testing.T) {
	fake := NewFakeChannel(FakeSilent())

	if _, ok := fake.Last(); ok {
		t.Error("Last() on empty store returned ok=true")
	}

	first := sampleMessage(KindOTP)
	second := sampleMessage(KindBookingCancelled)
	_, _ = fake.Send(context.Background(), first)
	_, _ = fake.Send(context.Background(), second)

	sent := fake.Sent()
	if len(sent) != 2 {
		t.Fatalf("Sent() len = %d, want 2", len(sent))
	}
	if sent[0].Kind != KindOTP || sent[1].Kind != KindBookingCancelled {
		t.Errorf("Sent() order wrong: got %q, %q", sent[0].Kind, sent[1].Kind)
	}

	// Sent returns a copy: mutating it must not affect the channel's store.
	sent[0] = OutboundMessage{}
	if again := fake.Sent(); again[0].Kind != KindOTP {
		t.Error("Sent() did not return an independent copy")
	}

	last, ok := fake.Last()
	if !ok || last.Kind != KindBookingCancelled {
		t.Errorf("Last() = (%q, %v), want (booking_cancelled, true)", last.Kind, ok)
	}

	fake.Reset()
	if fake.Count() != 0 {
		t.Errorf("Count() after Reset = %d, want 0", fake.Count())
	}
}

// TestFakeChannel_ConcurrentSend runs sends from many goroutines to prove the
// store is safe for concurrent use under the race detector.
func TestFakeChannel_ConcurrentSend(t *testing.T) {
	fake := NewFakeChannel(FakeSilent())
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = fake.Send(context.Background(), sampleMessage(KindBookingConfirmed))
		}()
	}
	wg.Wait()

	if fake.Count() != goroutines {
		t.Fatalf("Count() = %d, want %d", fake.Count(), goroutines)
	}
}

// Compile-time assertion that FakeChannel satisfies the channel contract.
var _ NotificationChannel = (*FakeChannel)(nil)
