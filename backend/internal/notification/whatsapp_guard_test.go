package notification

// WO-SECURITY-V1 PR-S2 Part 6 regression tests (A, B, E, F, H) for the paid-send
// kill switch and the corrected fallback-eligibility rules. Quota-specific cases
// (C, D) live in quota_test.go alongside the guard they extend.

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"
)

// A. Normal allowed WhatsApp attempt: paid enabled, quota below limit — provider
// invoked exactly once, quota reserved exactly once.
func TestPaidWhatsAppGuard_Enabled_UnderCap_SendsOnce(t *testing.T) {
	guard := &fakeGuard{count: 10}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.1"}}
	quotaGuarded := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))
	paidGuarded := NewPaidWhatsAppEnabledGuard(quotaGuarded, true, slog.New(&capturingHandler{}))

	res, err := paidGuarded.Send(context.Background(), bookingMsg())
	if err != nil {
		t.Fatalf("Send errored: %v", err)
	}
	if wa.called != 1 {
		t.Fatalf("provider invoked %d times, want 1", wa.called)
	}
	if guard.calls != 1 {
		t.Fatalf("quota reserved %d times, want 1", guard.calls)
	}
	if res.Status != DeliverySent {
		t.Fatalf("Status = %q, want %q", res.Status, DeliverySent)
	}
}

// B. Paid WhatsApp disabled: provider invoked zero times, quota reserve invoked
// zero times, SMS fallback invoked zero times, typed disabled error returned.
func TestPaidWhatsAppGuard_Disabled_RefusesBeforeQuotaOrProvider(t *testing.T) {
	guard := &fakeGuard{count: 0}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}

	quotaGuarded := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))
	paidGuarded := NewPaidWhatsAppEnabledGuard(quotaGuarded, false, slog.New(&capturingHandler{}))
	channel := NewFallbackChannel(paidGuarded, sms, WithFallbackEnabled(true)) // enabled — must still not fall back

	res, err := channel.Send(context.Background(), bookingMsg())

	if wa.called != 0 {
		t.Fatalf("provider invoked %d times, want 0", wa.called)
	}
	if guard.calls != 0 {
		t.Fatalf("quota reserve invoked %d times, want 0", guard.calls)
	}
	if sms.called != 0 {
		t.Fatalf("SMS fallback invoked %d times, want 0", sms.called)
	}
	if res.Status != DeliveryFailed || !errors.Is(err, ErrPaidWhatsAppDisabled) {
		t.Fatalf("result must be the paid-disabled gate refusal; status=%s err=%v", res.Status, err)
	}
}

// E. Genuine eligible provider failure with fallback disabled: WhatsApp provider
// invoked once, SMS fallback invoked zero times.
func TestFallback_EligibleFailure_Disabled_NoFallback(t *testing.T) {
	wa := &recordingChannel{result: DeliveryResult{Status: DeliveryFailed, Err: errors.New("meta 500")}, err: errors.New("meta 500")}
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}

	channel := NewFallbackChannel(wa, sms, WithFallbackEnabled(false))
	res, err := channel.Send(context.Background(), bookingMsg())

	if wa.called != 1 {
		t.Fatalf("provider invoked %d times, want 1", wa.called)
	}
	if sms.called != 0 {
		t.Fatalf("fallback invoked %d times, want 0 (fallback disabled)", sms.called)
	}
	if res.Status != DeliveryFailed || err == nil {
		t.Fatalf("result must remain the primary's failure; status=%s err=%v", res.Status, err)
	}
}

// F. Genuine eligible provider failure with fallback enabled: this test proves
// EXISTING optional behavior only — provider invoked once, fallback invoked
// once, using a fake/spy fallback channel (never a real SMS/provider call).
func TestFallback_EligibleFailure_Enabled_FallsBackOnce(t *testing.T) {
	wa := &recordingChannel{result: DeliveryResult{Status: DeliveryFailed, Err: errors.New("meta 500")}, err: errors.New("meta 500")}
	spyFallback := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "spy-1"}}

	channel := NewFallbackChannel(wa, spyFallback, WithFallbackEnabled(true))
	res, err := channel.Send(context.Background(), bookingMsg())
	if err != nil {
		t.Fatalf("Send errored: %v", err)
	}
	if wa.called != 1 {
		t.Fatalf("provider invoked %d times, want 1", wa.called)
	}
	if spyFallback.called != 1 {
		t.Fatalf("fallback invoked %d times, want 1", spyFallback.called)
	}
	if res.ProviderMessageID != "spy-1" {
		t.Fatalf("result must be the fallback's delivery; got id=%q", res.ProviderMessageID)
	}
}

// H. Sensitive logging: neither the paid-disabled guard's log line nor the
// quota fail-closed log line may contain an OTP value, a full phone number, a
// token, a cookie, an authorization header, or a provider secret.
func TestPaidWhatsAppGuard_And_QuotaFailClosed_LogsNeverContainSensitiveData(t *testing.T) {
	const recipient = "+962790000000"
	const otpCode = "123456"

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, nil)
	logger := slog.New(handler)

	// Paid-disabled path.
	guard := &fakeGuard{count: 0}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	quotaGuarded := NewQuotaGuardedChannel(wa, guard, "WABA1", logger)
	paidGuarded := NewPaidWhatsAppEnabledGuard(quotaGuarded, false, logger)

	otpMsg := OutboundMessage{Recipient: recipient, Kind: KindOTP, Payload: OTPPayload{Code: otpCode}}
	if _, err := paidGuarded.Send(context.Background(), otpMsg); !errors.Is(err, ErrPaidWhatsAppDisabled) {
		t.Fatalf("expected ErrPaidWhatsAppDisabled, got %v", err)
	}

	// Quota fail-closed path (paid enabled, guard errors).
	failGuard := &fakeGuard{err: errors.New("db down")}
	quotaGuarded2 := NewQuotaGuardedChannel(wa, failGuard, "WABA1", logger)
	paidGuarded2 := NewPaidWhatsAppEnabledGuard(quotaGuarded2, true, logger)
	if _, err := paidGuarded2.Send(context.Background(), otpMsg); !errors.Is(err, ErrWhatsAppQuotaUnavailable) {
		t.Fatalf("expected ErrWhatsAppQuotaUnavailable, got %v", err)
	}

	out := buf.String()
	if strings.Contains(out, recipient) {
		t.Fatalf("log output contains the full unmasked phone number: %s", out)
	}
	if strings.Contains(out, otpCode) {
		t.Fatalf("log output contains the plaintext OTP code: %s", out)
	}
	for _, forbidden := range []string{"token", "cookie", "authorization", "secret"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Fatalf("log output unexpectedly mentions %q: %s", forbidden, out)
		}
	}
}

// Sanity: also confirm no leak through the stdlib `log` package used by
// FallbackChannel's default (non-hook) log line when an eligible failure falls
// back — the recipient must be masked there too (pre-existing behavior,
// reconfirmed after this PR's changes to Send's control flow).
func TestFallbackChannel_DefaultLog_MasksRecipient(t *testing.T) {
	const recipient = "+962790000000"
	wa := &recordingChannel{result: DeliveryResult{Status: DeliveryFailed, Err: errors.New("meta 500")}, err: errors.New("meta 500")}
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	channel := NewFallbackChannel(wa, sms, WithFallbackEnabled(true))

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	msg := OutboundMessage{Recipient: recipient, Kind: KindBookingConfirmed, Payload: BookingConfirmedPayload{BookingID: 1, PitchName: "Pitch"}}
	if _, err := channel.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, recipient) {
		t.Fatalf("fallback log contains the full unmasked phone number: %s", out)
	}
	if !strings.Contains(out, maskPhone(recipient)) {
		t.Errorf("fallback log does not contain the expected masked phone %q: %s", maskPhone(recipient), out)
	}
}
