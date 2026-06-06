package notification

// Tests for config-driven type→sink routing, the budget guard (booking events
// never reach the real sender), the OTP-safety startup assertion, the fail-safe
// default for unknown kinds, and the LogOnly sink.

import (
	"context"
	"errors"
	"testing"
)

// spyChannel records every message it is asked to send.
type spyChannel struct {
	name  string
	calls []OutboundMessage
}

func (s *spyChannel) Send(_ context.Context, msg OutboundMessage) (DeliveryResult, error) {
	s.calls = append(s.calls, msg)
	return DeliveryResult{Status: DeliverySent, ProviderMessageID: s.name + "_id"}, nil
}

// spySink is a spyChannel that is ALSO a non-delivering LogSink.
type spySink struct{ spyChannel }

func (*spySink) logSinkMarker() {}

// defaultBetaRoutes mirrors the production closed-beta policy.
func defaultBetaRoutes() map[MessageKind]ChannelName {
	return map[MessageKind]ChannelName{
		KindOTP:              ChannelTwilioSMS,
		KindBookingConfirmed: ChannelLogOnly,
		KindBookingReminder:  ChannelLogOnly,
	}
}

func TestRouting_OTPGoesToRealSender(t *testing.T) {
	twilio := &spyChannel{name: "twilio"}
	logsink := &spySink{spyChannel{name: "log"}}
	svc := NewService(ChannelFake,
		WithChannel(ChannelTwilioSMS, twilio),
		WithChannel(ChannelLogOnly, logsink),
		WithOptInChecker(OptInFunc(func(context.Context, string) (bool, error) { return true, nil })),
		WithRoutingPolicy(defaultBetaRoutes(), ChannelLogOnly),
	)

	if _, err := svc.Send(context.Background(), sampleMessage(KindOTP)); err != nil {
		t.Fatalf("send OTP: %v", err)
	}
	if len(twilio.calls) != 1 {
		t.Fatalf("twilio received %d OTPs, want 1", len(twilio.calls))
	}
	if len(logsink.calls) != 0 {
		t.Errorf("log sink received %d OTPs, want 0", len(logsink.calls))
	}
}

// The budget guard, provable: booking events route to the log sink and the real
// sender (Twilio) is called ZERO times.
func TestRouting_BookingEventsNeverHitTwilio(t *testing.T) {
	twilio := &spyChannel{name: "twilio"}
	logsink := &spySink{spyChannel{name: "log"}}
	svc := NewService(ChannelFake,
		WithChannel(ChannelTwilioSMS, twilio),
		WithChannel(ChannelLogOnly, logsink),
		WithOptInChecker(OptInFunc(func(context.Context, string) (bool, error) { return true, nil })),
		WithRoutingPolicy(defaultBetaRoutes(), ChannelLogOnly),
	)

	for _, kind := range []MessageKind{KindBookingConfirmed, KindBookingReminder} {
		if _, err := svc.Send(context.Background(), sampleMessage(kind)); err != nil {
			t.Fatalf("send %s: %v", kind, err)
		}
	}
	if len(logsink.calls) != 2 {
		t.Errorf("log sink received %d booking events, want 2", len(logsink.calls))
	}
	if len(twilio.calls) != 0 {
		t.Fatalf("BUDGET GUARD VIOLATED: twilio received %d booking events, want 0", len(twilio.calls))
	}
}

// An UNMAPPED kind falls through to the fail-safe default (the log sink), never to
// a real sender — a new message type cannot accidentally burn budget.
func TestRouting_UnknownKindFallsToDefaultSink(t *testing.T) {
	twilio := &spyChannel{name: "twilio"}
	logsink := &spySink{spyChannel{name: "log"}}
	svc := NewService(ChannelFake,
		WithChannel(ChannelTwilioSMS, twilio),
		WithChannel(ChannelLogOnly, logsink),
		WithOptInChecker(OptInFunc(func(context.Context, string) (bool, error) { return true, nil })),
		// KindBookingCancelled is intentionally NOT mapped.
		WithRoutingPolicy(defaultBetaRoutes(), ChannelLogOnly),
	)

	if _, err := svc.Send(context.Background(), sampleMessage(KindBookingCancelled)); err != nil {
		t.Fatalf("send unmapped kind: %v", err)
	}
	if len(logsink.calls) != 1 || len(twilio.calls) != 0 {
		t.Errorf("unmapped kind: log=%d twilio=%d, want 1/0", len(logsink.calls), len(twilio.calls))
	}
}

func TestValidateRouting_OTPToRealSenderOK(t *testing.T) {
	svc := NewService(ChannelFake,
		WithChannel(ChannelTwilioSMS, &spyChannel{name: "twilio"}),
		WithChannel(ChannelLogOnly, &spySink{spyChannel{name: "log"}}),
		WithRoutingPolicy(defaultBetaRoutes(), ChannelLogOnly),
	)
	if err := svc.ValidateRouting(); err != nil {
		t.Errorf("ValidateRouting = %v, want nil", err)
	}
}

func TestValidateRouting_OTPToLogSinkFailsBoot(t *testing.T) {
	svc := NewService(ChannelFake,
		WithChannel(ChannelLogOnly, &spySink{spyChannel{name: "log"}}),
		WithRoutingPolicy(map[MessageKind]ChannelName{KindOTP: ChannelLogOnly}, ChannelLogOnly),
	)
	if err := svc.ValidateRouting(); !errors.Is(err, ErrRoutingUnsafe) {
		t.Fatalf("err = %v, want ErrRoutingUnsafe (OTP→log sink must fail boot)", err)
	}
}

func TestValidateRouting_OTPToUnknownSinkFailsBoot(t *testing.T) {
	svc := NewService(ChannelFake,
		WithChannel(ChannelLogOnly, &spySink{spyChannel{name: "log"}}),
		WithRoutingPolicy(map[MessageKind]ChannelName{KindOTP: "does_not_exist"}, ChannelLogOnly),
	)
	if err := svc.ValidateRouting(); !errors.Is(err, ErrRoutingUnsafe) {
		t.Fatalf("err = %v, want ErrRoutingUnsafe (OTP→unknown must fail boot)", err)
	}
}

func TestLogOnlySink_RecordsButDoesNotDeliverAndIsSink(t *testing.T) {
	sink := NewLogOnlySink(nil)
	res, err := sink.Send(context.Background(), sampleMessage(KindBookingConfirmed))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("status = %q, want sent (the sink 'succeeds' so the outbox completes the job)", res.Status)
	}
	// It must be recognisable as a non-delivering sink for the OTP safety check.
	if _, ok := any(sink).(LogSink); !ok {
		t.Error("LogOnlySink does not implement LogSink marker")
	}
}

func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"+962790001234": "+9627****34",
		"+15005550006":  "+1500****06",
		"123":           "****",
		"":              "****",
	}
	for in, want := range cases {
		if got := maskPhone(in); got != want {
			t.Errorf("maskPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
