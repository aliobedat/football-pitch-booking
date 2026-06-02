package notification

import (
	"context"
	"errors"
	"testing"
)

// optedOut / notOptedOut are opt-out checkers with fixed answers.
func optedOut() OptOutChecker {
	return OptOutFunc(func(context.Context, string) (bool, error) { return true, nil })
}
func notOptedOut() OptOutChecker {
	return OptOutFunc(func(context.Context, string) (bool, error) { return false, nil })
}

// TestService_OptOutGate_BlocksEveryKind is the core PART 6 guarantee: a
// recipient who has withdrawn consent receives NOTHING — OTP and booking events
// alike — and the channel is never reached.
func TestService_OptOutGate_BlocksEveryKind(t *testing.T) {
	kinds := []MessageKind{KindOTP, KindBookingConfirmed, KindBookingRejected, KindBookingCancelled}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			// allowAll opt-in so the OTP kind would otherwise pass — proving the
			// opt-out gate, not the opt-in gate, is what blocks it.
			svc, fake := newFakeService(t, WithOptInChecker(allowAll()), WithOptOutChecker(optedOut()))

			res, err := svc.Send(context.Background(), sampleMessage(kind))
			if !errors.Is(err, ErrOptedOut) {
				t.Fatalf("err = %v, want errors.Is(_, ErrOptedOut)", err)
			}
			if res.Status != DeliveryFailed {
				t.Errorf("status = %q, want %q", res.Status, DeliveryFailed)
			}
			if fake.Count() != 0 {
				t.Errorf("fake recorded %d sends, want 0 (opt-out must block before delegating)", fake.Count())
			}
		})
	}
}

// TestService_OptOutWinsOverOptIn ensures an explicit withdrawal beats a granted
// opt-in: the opt-out gate is evaluated first.
func TestService_OptOutWinsOverOptIn(t *testing.T) {
	svc, fake := newFakeService(t, WithOptInChecker(allowAll()), WithOptOutChecker(optedOut()))

	_, err := svc.Send(context.Background(), sampleMessage(KindOTP))
	if !errors.Is(err, ErrOptedOut) {
		t.Fatalf("err = %v, want ErrOptedOut to win over a granted opt-in", err)
	}
	if fake.Count() != 0 {
		t.Errorf("fake recorded %d sends, want 0", fake.Count())
	}
}

// TestService_NotOptedOut_Delivers confirms the gate is permissive when consent
// has not been withdrawn.
func TestService_NotOptedOut_Delivers(t *testing.T) {
	svc, fake := newFakeService(t, WithOptInChecker(allowAll()), WithOptOutChecker(notOptedOut()))

	res, err := svc.Send(context.Background(), sampleMessage(KindBookingConfirmed))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("status = %q, want %q", res.Status, DeliverySent)
	}
	if fake.Count() != 1 {
		t.Errorf("fake recorded %d sends, want 1", fake.Count())
	}
}

// TestService_OptOutLookupError surfaces a failing opt-out lookup and blocks the
// send (fail closed): if we cannot confirm consent state, we do not deliver.
func TestService_OptOutLookupError(t *testing.T) {
	boom := errors.New("db unreachable")
	checker := OptOutFunc(func(context.Context, string) (bool, error) { return false, boom })

	svc, fake := newFakeService(t, WithOptOutChecker(checker))

	res, err := svc.Send(context.Background(), sampleMessage(KindBookingConfirmed))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("status = %q, want %q", res.Status, DeliveryFailed)
	}
	if fake.Count() != 0 {
		t.Errorf("fake recorded %d sends, want 0", fake.Count())
	}
}
