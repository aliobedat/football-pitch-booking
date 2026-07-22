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

// Gate 2 / PR-1: OTP over the cap is refused exactly like a booking kind, so the
// real provider limit can't be silently exceeded. Reserve returns the
// POST-increment count (this attempt's ordinal), so "over the cap" is
// quotaHardCap+1 — the 251st attempt — not quotaHardCap itself (see the
// exact-boundary test for the cap'th attempt, which must be ADMITTED).
func TestQuota_OTPAtCap_Refused(t *testing.T) {
	guard := &fakeGuard{count: quotaHardCap + 1}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	otp := OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: OTPPayload{Code: "123456"}}
	res, err := q.Send(context.Background(), otp)
	if guard.calls != 1 {
		t.Fatalf("OTP must be quota-gated; Reserve called %d times (want 1)", guard.calls)
	}
	if wa.called != 0 {
		t.Fatalf("OTP over cap must NOT reach WhatsApp; called=%d", wa.called)
	}
	if res.Status != DeliveryFailed || !errors.Is(err, ErrWhatsAppDailyCapReached) {
		t.Fatalf("OTP over cap must fail with the cap error; status=%s err=%v", res.Status, err)
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

// Over the cap (the 251st attempt): refuse — WhatsApp is NOT called, result is
// DeliveryFailed wrapping ErrWhatsAppDailyCapReached. The exact-boundary test
// below proves the cap'th (250th) attempt is admitted, not refused.
func TestQuota_OverCap_RefusesWhatsApp(t *testing.T) {
	guard := &fakeGuard{count: quotaHardCap + 1}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	h := &capturingHandler{}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(h))

	res, err := q.Send(context.Background(), bookingMsg())
	if wa.called != 0 {
		t.Fatalf("over cap, WhatsApp must NOT be called; called=%d", wa.called)
	}
	if res.Status != DeliveryFailed {
		t.Fatalf("over cap, result must be DeliveryFailed; got %s", res.Status)
	}
	if !errors.Is(err, ErrWhatsAppDailyCapReached) {
		t.Fatalf("over cap, error must wrap ErrWhatsAppDailyCapReached; got %v", err)
	}
}

// WO-SECURITY-V1 PR-S2 exact-boundary regression: Reserve returns the
// POST-increment count (this attempt's ordinal). For a configured hard cap of
// 250: attempt 250 (count==250) must be ADMITTED (still in-budget), and
// attempt 251 (count==251) must be REFUSED. This proves the off-by-one fix
// (count > quotaHardCap, not count >= quotaHardCap).
func TestQuota_ExactBoundary_250thAdmitted_251stRefused(t *testing.T) {
	// The 250th attempt: Reserve reports count=250 (this send is #250 today).
	guard250 := &fakeGuard{count: quotaHardCap}
	wa250 := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.250"}}
	q250 := NewQuotaGuardedChannel(wa250, guard250, "WABA1", slog.New(&capturingHandler{}))

	res250, err250 := q250.Send(context.Background(), bookingMsg())
	if err250 != nil {
		t.Fatalf("the 250th (cap'th) attempt must be ADMITTED, not refused: %v", err250)
	}
	if wa250.called != 1 {
		t.Fatalf("the 250th attempt must reach WhatsApp exactly once; called=%d", wa250.called)
	}
	if res250.Status != DeliverySent {
		t.Fatalf("the 250th attempt must succeed; got status=%s", res250.Status)
	}

	// The 251st attempt: Reserve reports count=251 (over budget).
	guard251 := &fakeGuard{count: quotaHardCap + 1}
	wa251 := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	q251 := NewQuotaGuardedChannel(wa251, guard251, "WABA1", slog.New(&capturingHandler{}))

	res251, err251 := q251.Send(context.Background(), bookingMsg())
	if wa251.called != 0 {
		t.Fatalf("the 251st attempt must NOT reach WhatsApp; called=%d", wa251.called)
	}
	if res251.Status != DeliveryFailed || !errors.Is(err251, ErrWhatsAppDailyCapReached) {
		t.Fatalf("the 251st attempt must be refused with the cap error; status=%s err=%v", res251.Status, err251)
	}
}

// WO-SECURITY-V1 PR-S2 correction of the former "GATE 2.1 PROOF": quota
// exhaustion is now a GATE REFUSAL, not a genuine provider failure, so it must
// NOT fall back to SMS even when fallback is enabled on the FallbackChannel.
// This exercises the actual chain (real Meta adapter + real guard + real
// fallback wrapper) to prove the refusal, not just a stand-in for the WhatsApp
// leg.
func TestQuota_MetaOTP_AtCap_RefusesWithoutFallback(t *testing.T) {
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

	guard := &fakeGuard{count: quotaHardCap + 1} // pinned OVER the hard cap (251st attempt)
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "SM-OTP-1"}}

	// Exactly the production composition for the Meta provider, with fallback
	// explicitly enabled — proving the refusal holds EVEN THEN, because a gate
	// refusal is never eligible for fallback regardless of the switch.
	guarded := NewQuotaGuardedChannel(meta, guard, "WABA1", slog.New(&capturingHandler{}))
	channel := NewFallbackChannel(guarded, sms, WithFallbackEnabled(true))

	otp := OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindOTP,
		Payload:   OTPPayload{Code: "123456", ExpiresInSeconds: 300},
	}
	res, sendErr := channel.Send(context.Background(), otp)

	// 1) OTP was counted against the WABA quota (accounting preserved).
	if guard.calls != 1 {
		t.Fatalf("OTP must be counted against the WhatsApp quota; Reserve called %d times (want 1)", guard.calls)
	}
	// 2) WhatsApp was REFUSED by the guard — the real adapter never made a call.
	if whatsappHit {
		t.Fatalf("at cap the quota guard must refuse before the Meta adapter sends; WhatsApp endpoint was hit")
	}
	// 3) The refusal must NOT fall back to SMS — quota exhaustion is a gate
	// refusal, not a provider failure.
	if sms.called != 0 {
		t.Fatalf("quota exhaustion must NOT trigger SMS fallback; sms.called=%d", sms.called)
	}
	if res.Status != DeliveryFailed || !errors.Is(sendErr, ErrWhatsAppDailyCapReached) {
		t.Fatalf("result must be the cap-reached gate refusal; status=%s err=%v", res.Status, sendErr)
	}
}

// A guard (DB) error must fail CLOSED (WO-SECURITY-V1 PR-S2): the provider must
// never be invoked when quota accounting is unavailable, and the typed
// ErrWhatsAppQuotaUnavailable sentinel — not the raw datastore error — is what
// crosses the caller boundary.
func TestQuota_GuardError_FailsClosed(t *testing.T) {
	dbErr := errors.New("db down")
	guard := &fakeGuard{err: dbErr}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent}}
	q := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))

	res, err := q.Send(context.Background(), bookingMsg())
	if wa.called != 0 {
		t.Fatalf("fail-closed must NOT invoke the WhatsApp provider; called=%d", wa.called)
	}
	if res.Status != DeliveryFailed {
		t.Fatalf("fail-closed result must be DeliveryFailed; got %s", res.Status)
	}
	if !errors.Is(err, ErrWhatsAppQuotaUnavailable) {
		t.Fatalf("fail-closed error must wrap ErrWhatsAppQuotaUnavailable; got %v", err)
	}
	if strings.Contains(err.Error(), dbErr.Error()) {
		t.Fatalf("the raw datastore error must not cross the caller boundary; got %v", err)
	}
}

// End-to-end: composed INSIDE FallbackChannel (default-enabled, matching the
// channel-mechanism's own backward-compatible default), a cap refusal must
// STILL not fall back — quota exhaustion is a gate refusal, not a provider
// failure, regardless of the fallback switch.
func TestQuota_AtCap_DoesNotFallBackToSMS(t *testing.T) {
	guard := &fakeGuard{count: 300}
	wa := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "wamid.X"}}
	sms := &recordingChannel{result: DeliveryResult{Status: DeliverySent, ProviderMessageID: "SM123"}}

	guarded := NewQuotaGuardedChannel(wa, guard, "WABA1", slog.New(&capturingHandler{}))
	channel := NewFallbackChannel(guarded, sms)

	msg := bookingMsg()
	res, err := channel.Send(context.Background(), msg)
	if wa.called != 0 {
		t.Fatalf("WhatsApp must be refused at cap; called=%d", wa.called)
	}
	if sms.called != 0 {
		t.Fatalf("quota exhaustion must NOT trigger SMS fallback; called=%d", sms.called)
	}
	if res.Status != DeliveryFailed || !errors.Is(err, ErrWhatsAppDailyCapReached) {
		t.Fatalf("result must be the cap-reached gate refusal; status=%s err=%v", res.Status, err)
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
