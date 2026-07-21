package notification

// WO-SECURITY-V1 PR-S1 regression: proves the dev/fake and SMS-stub adapters
// never write a plaintext OTP code or a full/unmasked phone number to logs,
// while the masked form is still present where expected.

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
)

// captureLog redirects the standard logger's output for the duration of fn and
// returns everything written to it.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)
	fn()
	return buf.String()
}

func TestFakeChannel_LogsNeverContainPlaintextOTPOrFullPhone(t *testing.T) {
	const knownCode = "123456"
	const recipient = "+962790000000"

	fake := NewFakeChannel() // NOT silent — this is exactly the path under test.
	msg := sampleMessage(KindOTP)
	if msg.Recipient != recipient {
		t.Fatalf("sampleMessage recipient = %q, want %q (test assumption)", msg.Recipient, recipient)
	}
	if p, ok := msg.Payload.(OTPPayload); !ok || p.Code != knownCode {
		t.Fatalf("sampleMessage OTP payload = %+v, want Code=%q (test assumption)", msg.Payload, knownCode)
	}

	out := captureLog(t, func() {
		if _, err := fake.Send(context.Background(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	})

	if strings.Contains(out, knownCode) {
		t.Fatalf("FakeChannel log output contains the plaintext OTP code: %s", out)
	}
	if strings.Contains(out, recipient) {
		t.Fatalf("FakeChannel log output contains the full unmasked phone number: %s", out)
	}
	if !strings.Contains(out, maskPhone(recipient)) {
		t.Errorf("FakeChannel log output does not contain the expected masked phone %q: %s", maskPhone(recipient), out)
	}
}

func TestSmsChannel_LogsNeverContainFullPhone(t *testing.T) {
	const recipient = "+962790000000"

	sms := NewSmsChannel() // NOT silent — this is exactly the path under test.
	msg := sampleMessage(KindBookingConfirmed)
	if msg.Recipient != recipient {
		t.Fatalf("sampleMessage recipient = %q, want %q (test assumption)", msg.Recipient, recipient)
	}

	out := captureLog(t, func() {
		if _, err := sms.Send(context.Background(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	})

	if strings.Contains(out, recipient) {
		t.Fatalf("SmsChannel log output contains the full unmasked phone number: %s", out)
	}
	if !strings.Contains(out, maskPhone(recipient)) {
		t.Errorf("SmsChannel log output does not contain the expected masked phone %q: %s", maskPhone(recipient), out)
	}
}

func TestFallbackChannel_LogsNeverContainFullPhone(t *testing.T) {
	const recipient = "+962790000000"

	primary := &failingChannel{}
	fallback := NewSmsChannel(SmsSilent())
	fc := NewFallbackChannel(primary, fallback)

	msg := sampleMessage(KindBookingConfirmed)
	if msg.Recipient != recipient {
		t.Fatalf("sampleMessage recipient = %q, want %q (test assumption)", msg.Recipient, recipient)
	}

	out := captureLog(t, func() {
		if _, err := fc.Send(context.Background(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	})

	if strings.Contains(out, recipient) {
		t.Fatalf("FallbackChannel log output contains the full unmasked phone number: %s", out)
	}
	if !strings.Contains(out, maskPhone(recipient)) {
		t.Errorf("FallbackChannel log output does not contain the expected masked phone %q: %s", maskPhone(recipient), out)
	}
}

// failingChannel always reports a delivery failure, forcing FallbackChannel to
// log its "falling back" line and invoke the fallback channel.
type failingChannel struct{}

func (failingChannel) Send(context.Context, OutboundMessage) (DeliveryResult, error) {
	return DeliveryResult{Status: DeliveryFailed}, nil
}
