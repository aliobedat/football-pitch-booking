package notification

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// Gate 2 / PR-1: OTP is NO LONGER exempt. Under cap it reserves one slot then sends
// (WhatsApp AUTHENTICATION templates count against the WABA limit).
func TestQuota_OTPUnderCap_ReservesAndSends(t *testing.T) {
	guard := &fakeGuard{count: 10}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "infobip:1"}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	otp := OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: OTPPayload{Code: "123456"}}
	res, err := q.Send(context.Background(), otp)
	if err != nil {
		t.Fatalf("OTP send errored: %v", err)
	}
	if guard.calls != 1 {
		t.Fatalf("OTP must now be quota-gated; Reserve called %d times (want 1)", guard.calls)
	}
	if wa.called != 1 || res.Status != DeliverySent {
		t.Fatalf("OTP under cap must reserve then send (called=%d status=%s)", wa.called, res.Status)
	}
}

// Gate 2 / PR-1: OTP at/over the cap is refused exactly like a booking kind, so the
// real provider limit can't be silently exceeded.
func TestQuota_OTPAtCap_Refused(t *testing.T) {
	guard := &fakeGuard{count: quotaHardCap}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	otp := OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: OTPPayload{Code: "123456"}}
	res, err := q.Send(context.Background(), otp)
	if guard.calls != 1 {
		t.Fatalf("OTP must be quota-gated; Reserve called %d times (want 1)", guard.calls)
	}
	if wa.called != 0 {
		t.Fatalf("OTP at cap must NOT reach WhatsApp; called=%d", wa.called)
	}
	if res.Status != DeliveryFailed || !errors.Is(err, ErrWhatsAppDailyCapReached) {
		t.Fatalf("OTP at cap must fail with the cap error; status=%s err=%v", res.Status, err)
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

// GATE 2.1 PROOF: the REAL Meta WhatsApp adapter, wrapped exactly as production
// wires it — QuotaGuardedChannel(meta) inside FallbackChannel(_, sms) — must NOT
// lock out OTP at the quota cap. At cap the guard refuses WhatsApp and the OTP is
// delivered through the SMS fallback instead. This exercises the actual chain
// (real adapter + real guard + real fallback), not a stand-in for the WhatsApp leg,
// so it proves admission control is separated from accounting on the Meta path.
func TestQuota_MetaOTP_AtCap_FallsBackToSMS(t *testing.T) {
	// A real Meta adapter pointed at a server that records whether it is ever hit.
	// At cap it must NOT be: the quota guard refuses before any HTTP call.
	var whatsappHit bool
	waSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		whatsappHit = true
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.SHOULD_NOT_HAPPEN"}]}`)
	}))
	defer waSrv.Close()

	meta, err := NewWhatsAppChannel(testWhatsAppConfig(waSrv.URL), WithHTTPClient(waSrv.Client()))
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}

	guard := &fakeGuard{count: quotaHardCap} // pinned at the hard cap
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "SM-OTP-1"}}

	// Exactly the production composition for the Meta provider.
	guarded := NewQuotaGuardedChannel(meta, guard, "WABA1", slog.New(&capturingHandler{}))
	channel := NewFallbackChannel(guarded, sms)

	otp := OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindOTP,
		Payload:   OTPPayload{Code: "123456", ExpiresInSeconds: 300},
	}
	res, sendErr := channel.Send(context.Background(), otp)
	if sendErr != nil {
		t.Fatalf("chain returned an error; OTP must be delivered via SMS fallback: %v", sendErr)
	}

	// 1) OTP was counted against the WABA quota (accounting preserved).
	if guard.calls != 1 {
		t.Fatalf("OTP must be counted against the WhatsApp quota; Reserve called %d times (want 1)", guard.calls)
	}
	// 2) WhatsApp was REFUSED by the guard — the real adapter never made a call.
	if whatsappHit {
		t.Fatalf("at cap the quota guard must refuse before the Meta adapter sends; WhatsApp endpoint was hit")
	}
	// 3) The OTP was actually delivered through the SMS fallback (no lockout).
	if sms.called != 1 {
		t.Fatalf("OTP must fall through to SMS exactly once; sms.called=%d", sms.called)
	}
	if sms.last.Kind != KindOTP || sms.last.Recipient != otp.Recipient {
		t.Fatalf("SMS fallback must carry the SAME OTP message; got kind=%s recipient=%s", sms.last.Kind, sms.last.Recipient)
	}
	if res.Status != DeliverySent || res.ProviderMessageID != "SM-OTP-1" {
		t.Fatalf("result must be the successful SMS delivery; got status=%s id=%q", res.Status, res.ProviderMessageID)
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

// Guards the gated-kind set precisely: OTP and the three booking kinds are gated;
// booking_rejected (unsupported by the WhatsApp adapters) is not.
func TestQuota_GatedKindSet(t *testing.T) {
	gated := map[MessageKind]bool{
		KindBookingConfirmed: true,
		KindBookingCancelled: true,
		KindBookingReminder:  true,
		KindOTP:              true,
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
