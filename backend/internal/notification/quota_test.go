package notification

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// ── test doubles ─────────────────────────────────────────────────────────────

// fakeGuard returns a fixed count for every Reserve and records call count, or an
// error to exercise the fail-open path.
type fakeGuard struct {
	count int
	err   error
	calls int
	waba  string
}

func (f *fakeGuard) Reserve(_ context.Context, wabaID string) (int, error) {
	f.calls++
	f.waba = wabaID
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

// recordingChannel records that it was invoked and with what, returning a scripted
// result. Stands in for the WhatsApp channel and the SMS fallback.
type recordingChannel struct {
	called int
	last   OutboundMessage
	result DeliveryResult
	err    error
}

func (r *recordingChannel) Send(_ context.Context, msg OutboundMessage) (DeliveryResult, error) {
	r.called++
	r.last = msg
	return r.result, r.err
}

// capturingHandler is a minimal slog.Handler that records emitted records so tests
// can assert that (and only when) a warn fires.
type capturingHandler struct{ recs []slog.Record }

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.recs = append(h.recs, r)
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) warnCount() int {
	n := 0
	for _, r := range h.recs {
		if r.Level == slog.LevelWarn {
			n++
		}
	}
	return n
}

func bookingMsg() OutboundMessage {
	return OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindBookingConfirmed,
		Payload:   BookingConfirmedPayload{BookingID: 1, PitchName: "Pitch"},
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

// OTP (and any non-booking kind) must bypass the guard completely: no Reserve, no
// block, regardless of how full the bucket is.
func TestQuota_OTPBypassesGuard(t *testing.T) {
	guard := &fakeGuard{count: 9999} // way over cap — must be irrelevant for OTP
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.1"}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	otp := OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: OTPPayload{Code: "123456"}}
	res, err := q.Send(context.Background(), otp)
	if err != nil {
		t.Fatalf("OTP send errored: %v", err)
	}
	if guard.calls != 0 {
		t.Fatalf("OTP must not touch the quota guard; Reserve called %d times", guard.calls)
	}
	if wa.called != 1 || res.Status != DeliverySent {
		t.Fatalf("OTP must pass straight through to WhatsApp (called=%d status=%s)", wa.called, res.Status)
	}
}

// Under the warn threshold: send, no warn.
func TestQuota_UnderThreshold_SendsNoWarn(t *testing.T) {
	guard := &fakeGuard{count: 200} // count <= 200 → send, no warn
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	h := &capturingHandler{}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(h))

	if _, err := q.Send(context.Background(), bookingMsg()); err != nil {
		t.Fatalf("send errored: %v", err)
	}
	if wa.called != 1 {
		t.Fatalf("expected WhatsApp send, called=%d", wa.called)
	}
	if h.warnCount() != 0 {
		t.Fatalf("count=200 must NOT warn; got %d warns", h.warnCount())
	}
}

// Warn band (201..249): still sends, emits a warn.
func TestQuota_WarnBand_SendsAndWarns(t *testing.T) {
	guard := &fakeGuard{count: 201}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	h := &capturingHandler{}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(h))

	if _, err := q.Send(context.Background(), bookingMsg()); err != nil {
		t.Fatalf("send errored: %v", err)
	}
	if wa.called != 1 {
		t.Fatalf("warn-band send must still reach WhatsApp; called=%d", wa.called)
	}
	if h.warnCount() != 1 {
		t.Fatalf("count=201 must warn exactly once; got %d", h.warnCount())
	}
}

// At/over the cap: refuse — WhatsApp is NOT called, result is DeliveryFailed wrapping
// ErrWhatsAppDailyCapReached (the signal FallbackChannel routes on).
func TestQuota_AtCap_RefusesWhatsApp(t *testing.T) {
	guard := &fakeGuard{count: 250}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	h := &capturingHandler{}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(h))

	res, err := q.Send(context.Background(), bookingMsg())
	if wa.called != 0 {
		t.Fatalf("at cap, WhatsApp must NOT be called; called=%d", wa.called)
	}
	if res.Status != DeliveryFailed {
		t.Fatalf("at cap, result must be DeliveryFailed; got %s", res.Status)
	}
	if !errors.Is(err, ErrWhatsAppDailyCapReached) {
		t.Fatalf("at cap, error must wrap ErrWhatsAppDailyCapReached; got %v", err)
	}
}

// A guard (DB) error must fail OPEN: send proceeds through WhatsApp.
func TestQuota_GuardError_FailsOpen(t *testing.T) {
	guard := &fakeGuard{err: errors.New("db down")}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	res, err := q.Send(context.Background(), bookingMsg())
	if err != nil {
		t.Fatalf("fail-open must not surface the guard error: %v", err)
	}
	if wa.called != 1 || res.Status != DeliverySent {
		t.Fatalf("fail-open must still send via WhatsApp (called=%d status=%s)", wa.called, res.Status)
	}
}

// End-to-end: composed INSIDE FallbackChannel, a cap refusal transparently routes
// the SAME message to SMS in the same request (not deferred).
func TestQuota_AtCap_FallsBackToSMS(t *testing.T) {
	guard := &fakeGuard{count: 300}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.X"}}
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "SM123"}}

	guarded := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))
	channel := NewFallbackChannel(guarded, sms)

	msg := bookingMsg()
	res, err := channel.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("fallback send errored: %v", err)
	}
	if wa.called != 0 {
		t.Fatalf("WhatsApp must be refused at cap; called=%d", wa.called)
	}
	if sms.called != 1 {
		t.Fatalf("SMS fallback must be invoked exactly once; called=%d", sms.called)
	}
	if sms.last.Recipient != msg.Recipient || sms.last.Kind != msg.Kind {
		t.Fatalf("fallback must carry the SAME message; got recipient=%s kind=%s", sms.last.Recipient, sms.last.Kind)
	}
	if res.ProviderMessageID != "SM123" {
		t.Fatalf("result must be the SMS delivery; got id=%q", res.ProviderMessageID)
	}
}

// Guards the gated-kind set precisely: the three booking kinds are gated; OTP and
// booking_rejected are not.
func TestQuota_GatedKindSet(t *testing.T) {
	gated := map[MessageKind]bool{
		KindBookingConfirmed: true,
		KindBookingCancelled: true,
		KindBookingReminder:  true,
		KindOTP:              false,
		KindBookingRejected:  false,
	}
	for k, want := range gated {
		if got := isQuotaGated(k); got != want {
			t.Errorf("isQuotaGated(%s) = %v, want %v", k, got, want)
		}
	}
}

// Sanity: the typed cap error reads clearly when wrapped.
func TestQuota_CapErrorMessage(t *testing.T) {
	err := ErrWhatsAppDailyCapReached
	if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("cap error should mention the cap: %q", err.Error())
	}
}
