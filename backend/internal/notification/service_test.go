package notification

import (
	"context"
	"errors"
	"testing"
	"time"
)

// sampleMessage builds a valid OutboundMessage for the given kind so each test
// case exercises a real payload <-> kind pairing.
func sampleMessage(kind MessageKind) OutboundMessage {
	const recipient = "+962790000000"
	start := time.Date(2026, 6, 2, 18, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	var p Payload
	switch kind {
	case KindOTP:
		p = OTPPayload{Code: "123456", ExpiresInSeconds: 300}
	case KindBookingConfirmed:
		p = BookingConfirmedPayload{BookingID: 1, PitchName: "Pitch A", StartTime: start, EndTime: end}
	case KindBookingRejected:
		p = BookingRejectedPayload{BookingID: 1, PitchName: "Pitch A", StartTime: start, EndTime: end, Reason: "slot taken"}
	case KindBookingCancelled:
		p = BookingCancelledPayload{BookingID: 1, PitchName: "Pitch A", StartTime: start, EndTime: end, Reason: "owner cancelled"}
	default:
		p = nil
	}
	return OutboundMessage{Recipient: recipient, Kind: kind, Payload: p}
}

// allowAll / denyAll are opt-in checkers with fixed answers.
func allowAll() OptInChecker {
	return OptInFunc(func(context.Context, string) (bool, error) { return true, nil })
}
func denyAll() OptInChecker {
	return OptInFunc(func(context.Context, string) (bool, error) { return false, nil })
}

// newFakeService wires a Service over a silent FakeChannel and returns both.
func newFakeService(t *testing.T, opts ...Option) (*Service, *FakeChannel) {
	t.Helper()
	fake := NewFakeChannel(FakeSilent())
	base := []Option{WithChannel(ChannelFake, fake)}
	svc := NewService(ChannelFake, append(base, opts...)...)
	return svc, fake
}

// TestService_SendEachKind_ThroughFake is the core acceptance check: every
// message kind delivers successfully through the Fake channel.
func TestService_SendEachKind_ThroughFake(t *testing.T) {
	kinds := []MessageKind{KindOTP, KindBookingConfirmed, KindBookingRejected, KindBookingCancelled}

	// allowAll opt-in so the OTP kind clears the gate.
	svc, fake := newFakeService(t, WithOptInChecker(allowAll()))

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			res, err := svc.Send(context.Background(), sampleMessage(kind))
			if err != nil {
				t.Fatalf("Send(%s) returned error: %v", kind, err)
			}
			if res.Status != DeliverySent {
				t.Errorf("Status = %q, want %q", res.Status, DeliverySent)
			}
			if res.ProviderMessageID == "" {
				t.Error("ProviderMessageID is empty, want a generated id")
			}
			if res.Err != nil {
				t.Errorf("DeliveryResult.Err = %v, want nil", res.Err)
			}
		})
	}

	if got := fake.Count(); got != len(kinds) {
		t.Fatalf("fake recorded %d messages, want %d", got, len(kinds))
	}
}

// TestService_OptInGate enforces the opt-in gate across every message kind:
// OTP is the only kind blocked when consent is absent; booking events always
// pass regardless of opt-in state.
func TestService_OptInGate(t *testing.T) {
	cases := []struct {
		name      string
		kind      MessageKind
		checker   OptInChecker
		wantErr   error // nil means success
		wantSends int
	}{
		{"otp denied is refused", KindOTP, denyAll(), ErrOptInRequired, 0},
		{"otp allowed is sent", KindOTP, allowAll(), nil, 1},
		{"otp without checker is refused", KindOTP, nil, ErrNoOptInChecker, 0},

		{"confirmed sent despite deny", KindBookingConfirmed, denyAll(), nil, 1},
		{"confirmed sent without checker", KindBookingConfirmed, nil, nil, 1},
		{"rejected sent despite deny", KindBookingRejected, denyAll(), nil, 1},
		{"rejected sent without checker", KindBookingRejected, nil, nil, 1},
		{"cancelled sent despite deny", KindBookingCancelled, denyAll(), nil, 1},
		{"cancelled sent without checker", KindBookingCancelled, nil, nil, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opts []Option
			if c.checker != nil {
				opts = append(opts, WithOptInChecker(c.checker))
			}
			svc, fake := newFakeService(t, opts...)

			res, err := svc.Send(context.Background(), sampleMessage(c.kind))

			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want errors.Is(_, %v)", err, c.wantErr)
				}
				if res.Status != DeliveryFailed {
					t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
				}
				if !errors.Is(res.Err, c.wantErr) {
					t.Errorf("DeliveryResult.Err = %v, want errors.Is(_, %v)", res.Err, c.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("Send returned unexpected error: %v", err)
				}
				if res.Status != DeliverySent {
					t.Errorf("Status = %q, want %q", res.Status, DeliverySent)
				}
			}

			if got := fake.Count(); got != c.wantSends {
				t.Errorf("fake recorded %d sends, want %d (gate must block before delegating)", got, c.wantSends)
			}
		})
	}
}

// TestService_OptInLookupError ensures a failing consent lookup is surfaced and
// the message is not delivered.
func TestService_OptInLookupError(t *testing.T) {
	boom := errors.New("db unreachable")
	checker := OptInFunc(func(context.Context, string) (bool, error) { return false, boom })

	svc, fake := newFakeService(t, WithOptInChecker(checker))

	res, err := svc.Send(context.Background(), sampleMessage(KindOTP))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
	}
	if fake.Count() != 0 {
		t.Errorf("fake recorded %d sends, want 0", fake.Count())
	}
}

// TestService_UnknownActiveChannel verifies routing fails clearly when the
// active channel has no registered adapter.
func TestService_UnknownActiveChannel(t *testing.T) {
	// Active is WHATSAPP but only FAKE is registered.
	fake := NewFakeChannel(FakeSilent())
	svc := NewService(ChannelWhatsApp, WithChannel(ChannelFake, fake))

	res, err := svc.Send(context.Background(), sampleMessage(KindBookingConfirmed))
	if !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("err = %v, want errors.Is(_, ErrUnknownChannel)", err)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
	}
}

// TestService_Validation rejects structurally invalid messages before any
// channel or opt-in work happens.
func TestService_Validation(t *testing.T) {
	cases := []struct {
		name string
		msg  OutboundMessage
	}{
		{"empty recipient", OutboundMessage{Recipient: "", Kind: KindBookingConfirmed, Payload: BookingConfirmedPayload{}}},
		{"nil payload", OutboundMessage{Recipient: "+962790000000", Kind: KindBookingConfirmed, Payload: nil}},
		{"kind mismatch", OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: BookingConfirmedPayload{}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := newFakeService(t, WithOptInChecker(allowAll()))
			res, err := svc.Send(context.Background(), c.msg)
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("err = %v, want errors.Is(_, ErrInvalidMessage)", err)
			}
			if res.Status != DeliveryFailed {
				t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
			}
			if fake.Count() != 0 {
				t.Errorf("fake recorded %d sends, want 0", fake.Count())
			}
		})
	}
}

// TestService_SatisfiesChannel is a compile-time assertion that *Service itself
// satisfies NotificationChannel, so it can be layered/decorated later.
var _ NotificationChannel = (*Service)(nil)

func TestActiveChannelFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		set     bool
		value   string
		want    ChannelName
		wantErr bool
	}{
		{"unset defaults to FAKE", false, "", ChannelFake, false},
		{"empty defaults to FAKE", true, "", ChannelFake, false},
		{"explicit FAKE", true, "FAKE", ChannelFake, false},
		{"explicit SMS", true, "SMS", ChannelSMS, false},
		{"explicit WHATSAPP", true, "WHATSAPP", ChannelWhatsApp, false},
		{"unknown value errors", true, "carrier-pigeon", "", true},
		{"case sensitive lower fake errors", true, "fake", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv(EnvChannel, c.value)
			} else {
				// Ensure no ambient value leaks in.
				t.Setenv(EnvChannel, "")
			}

			got, err := ActiveChannelFromEnv()
			if c.wantErr {
				if !errors.Is(err, ErrInvalidChannel) {
					t.Fatalf("err = %v, want errors.Is(_, ErrInvalidChannel)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
