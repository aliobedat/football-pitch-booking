package notification

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// scriptedChannel is a controllable NotificationChannel for fallback tests. It
// records calls and returns a scripted result/error.
type scriptedChannel struct {
	calls int
	res   DeliveryResult
	err   error
}

func (s *scriptedChannel) Send(_ context.Context, _ OutboundMessage) (DeliveryResult, error) {
	s.calls++
	return s.res, s.err
}

// TestFallback_PrimarySuccess_SkipsFallback: a healthy primary is returned as-is
// and the fallback is never touched.
func TestFallback_PrimarySuccess_SkipsFallback(t *testing.T) {
	primary := &scriptedChannel{res: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.OK"}}
	sms := NewSmsChannel(SmsSilent())

	fb := NewFallbackChannel(primary, sms)
	res, err := fb.Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ProviderMessageID != "wamid.OK" {
		t.Errorf("ProviderMessageID = %q, want primary's wamid.OK", res.ProviderMessageID)
	}
	if sms.Count() != 0 {
		t.Errorf("fallback SMS sent %d, want 0 (primary succeeded)", sms.Count())
	}
}

// TestFallback_PrimaryError_FallsBack: a primary that returns an error routes to
// the SMS fallback, whose successful result is returned.
func TestFallback_PrimaryError_FallsBack(t *testing.T) {
	primary := &scriptedChannel{
		res: DeliveryResult{Status: DeliveryFailed, Err: errors.New("boom")},
		err: errors.New("boom"),
	}
	sms := NewSmsChannel(SmsSilent())

	fb := NewFallbackChannel(primary, sms)
	res, err := fb.Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("Send returned error after fallback, want success: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("Status = %q, want %q (from SMS)", res.Status, DeliverySent)
	}
	if sms.Count() != 1 {
		t.Fatalf("fallback SMS sent %d, want 1", sms.Count())
	}
	if last, _ := sms.Last(); last.Kind != KindOTP {
		t.Errorf("SMS received kind %q, want %q", last.Kind, KindOTP)
	}
}

// TestFallback_FailedStatusNoError_FallsBack: a primary that returns a
// DeliveryFailed status WITHOUT a Go error still triggers the fallback.
func TestFallback_FailedStatusNoError_FallsBack(t *testing.T) {
	primary := &scriptedChannel{res: DeliveryResult{Status: DeliveryFailed}}
	sms := NewSmsChannel(SmsSilent())

	fb := NewFallbackChannel(primary, sms)
	if _, err := fb.Send(context.Background(), sampleMessage(KindBookingConfirmed)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sms.Count() != 1 {
		t.Errorf("fallback SMS sent %d, want 1", sms.Count())
	}
}

// TestFallback_Hook_FiresWithPrimaryError verifies the observability hook is
// called with the primary's underlying error.
func TestFallback_Hook_FiresWithPrimaryError(t *testing.T) {
	wantErr := errors.New("template rejected")
	primary := &scriptedChannel{res: DeliveryResult{Status: DeliveryFailed, Err: wantErr}}
	sms := NewSmsChannel(SmsSilent())

	var hookErr error
	var hookMsg OutboundMessage
	fb := NewFallbackChannel(primary, sms, WithFallbackHook(func(msg OutboundMessage, err error) {
		hookMsg, hookErr = msg, err
	}))

	if _, err := fb.Send(context.Background(), sampleMessage(KindOTP)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !errors.Is(hookErr, wantErr) {
		t.Errorf("hook err = %v, want %v", hookErr, wantErr)
	}
	if hookMsg.Kind != KindOTP {
		t.Errorf("hook msg kind = %q, want %q", hookMsg.Kind, KindOTP)
	}
}

// TestFallback_RealWhatsAppMetaFailure_FallsBackToSMS is the integration-flavoured
// acceptance test: a REAL WhatsApp adapter pointed at a Meta endpoint that returns
// an error must gracefully fall back to SMS. This exercises the exact production
// wiring (NewFallbackChannel(whatsapp, sms)).
func TestFallback_RealWhatsAppMetaFailure_FallsBackToSMS(t *testing.T) {
	// Simulate Meta rejecting the request (e.g. unapproved AUTHENTICATION template
	// while business verification is pending).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Template not approved","type":"OAuthException","code":132001}}`))
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, nil)
	sms := NewSmsChannel(SmsSilent())
	fb := NewFallbackChannel(wa, sms)

	res, err := fb.Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("fallback should have recovered via SMS, got error: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("Status = %q, want %q (delivered by SMS fallback)", res.Status, DeliverySent)
	}
	if res.ProviderMessageID == "" {
		t.Error("ProviderMessageID is empty; SMS fallback should supply one")
	}
	if sms.Count() != 1 {
		t.Fatalf("SMS fallback received %d messages, want 1", sms.Count())
	}
}

// TestFallback_RealWhatsAppSuccess_NoSMS confirms the inverse: when Meta accepts
// the message, the SMS fallback stays untouched.
func TestFallback_RealWhatsAppSuccess_NoSMS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.LIVE"}]}`))
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, nil)
	sms := NewSmsChannel(SmsSilent())
	fb := NewFallbackChannel(wa, sms)

	res, err := fb.Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ProviderMessageID != "wamid.LIVE" {
		t.Errorf("ProviderMessageID = %q, want wamid.LIVE from WhatsApp", res.ProviderMessageID)
	}
	if sms.Count() != 0 {
		t.Errorf("SMS fallback sent %d, want 0", sms.Count())
	}
}
