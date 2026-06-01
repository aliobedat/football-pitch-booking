package notification

import (
	"context"
	"testing"
)

// TestPayloadKinds locks each payload type to its MessageKind so the typed
// payload <-> kind mapping cannot silently drift.
func TestPayloadKinds(t *testing.T) {
	cases := []struct {
		name string
		p    Payload
		want MessageKind
	}{
		{"otp", OTPPayload{}, KindOTP},
		{"confirmed", BookingConfirmedPayload{}, KindBookingConfirmed},
		{"rejected", BookingRejectedPayload{}, KindBookingRejected},
		{"cancelled", BookingCancelledPayload{}, KindBookingCancelled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Kind(); got != c.want {
				t.Fatalf("Kind() = %q, want %q", got, c.want)
			}
		})
	}
}

// Compile-time assertions that the contracts are implementable. These stubs are
// test-only — they contain no provider or business logic and exist solely to
// prove the interfaces can be satisfied by later adapters.
type stubChannel struct{}

func (stubChannel) Send(context.Context, OutboundMessage) (DeliveryResult, error) {
	return DeliveryResult{}, nil
}

type stubOtp struct{}

func (stubOtp) Request(context.Context, string) error                { return nil }
func (stubOtp) Verify(context.Context, string, string) (bool, error) { return false, nil }

var (
	_ NotificationChannel = stubChannel{}
	_ OtpService          = stubOtp{}
)
