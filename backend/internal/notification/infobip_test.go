package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/config"
)

// testInfobipCfg builds a fully-populated Infobip config pointed at baseURL.
func testInfobipCfg(baseURL string) config.InfobipConfig {
	return config.InfobipConfig{
		BaseURL: baseURL,
		APIKey:  "secret-key",
		Sender:  "447860099299",
		Templates: config.InfobipTemplates{
			Language:         "en",
			OTP:              "malaeb_otp",
			BookingConfirmed: "malaeb_booking_confirmed",
			BookingCancelled: "malaeb_booking_cancelled",
			BookingReminder:  "malaeb_booking_reminder",
		},
	}
}

// newInfobipTestServer captures the last request body/headers and returns a scripted
// response. It wires an InfobipWhatsAppChannel pointed at the test server.
func newInfobipTestServer(t *testing.T, status int, respBody string) (*InfobipWhatsAppChannel, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		cap.body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)

	ch, err := NewInfobipWhatsAppChannel(testInfobipCfg(srv.URL),
		WithInfobipHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("construct infobip channel: %v", err)
	}
	return ch, cap
}

type capturedRequest struct {
	method     string
	path       string
	authHeader string
	body       []byte
}

func (c *capturedRequest) decode(t *testing.T) infobipTemplateRequest {
	t.Helper()
	var req infobipTemplateRequest
	if err := json.Unmarshal(c.body, &req); err != nil {
		t.Fatalf("decode captured request: %v\nbody=%s", err, c.body)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected exactly one message, got %d", len(req.Messages))
	}
	return req
}

const infobipOKResp = `{"messages":[{"messageId":"abc-123","status":{"groupName":"PENDING","name":"PENDING_ENROUTE"}}]}`

// 1. OTP authentication payload → correct request body (placeholders + copy-code button).
func TestInfobip_BuildsOTPRequest(t *testing.T) {
	ch, cap := newInfobipTestServer(t, http.StatusOK, infobipOKResp)

	msg := OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindOTP,
		Payload:   OTPPayload{Code: "123456", ExpiresInSeconds: 300},
	}
	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	if cap.method != http.MethodPost || cap.path != infobipTemplateSendPath {
		t.Fatalf("unexpected endpoint: %s %s", cap.method, cap.path)
	}
	if cap.authHeader != "App secret-key" {
		t.Fatalf("auth header must be \"App <key>\"; got %q", cap.authHeader)
	}

	req := cap.decode(t)
	m := req.Messages[0]
	if m.From != "447860099299" {
		t.Errorf("from = %q, want sender", m.From)
	}
	if m.To != "962790000000" { // leading '+' stripped
		t.Errorf("to = %q, want stripped E.164", m.To)
	}
	if m.Content.TemplateName != "malaeb_otp" {
		t.Errorf("templateName = %q", m.Content.TemplateName)
	}
	if m.Content.Language != "en" {
		t.Errorf("language = %q", m.Content.Language)
	}
	if got := m.Content.TemplateData.Body.Placeholders; len(got) != 1 || got[0] != "123456" {
		t.Errorf("body placeholders = %v, want [123456]", got)
	}
	btns := m.Content.TemplateData.Buttons
	if len(btns) != 1 || btns[0].Parameter != "123456" {
		t.Errorf("copy-code button must carry the OTP; got %+v", btns)
	}
}

// T1 (adapter): booking confirmation → the approved Arabic template with 8 body
// placeholders in the correct ORDER and VALUES, language "ar", no buttons.
// 17:00–18:00 UTC on 2026-07-15 is 20:00–21:00 Asia/Amman (UTC+3, no DST).
func TestInfobip_BuildsBookingConfirmationAR(t *testing.T) {
	ch, cap := newInfobipTestServer(t, http.StatusOK, infobipOKResp)

	start := time.Date(2026, 7, 15, 17, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	msg := OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindBookingConfirmed,
		Payload: BookingConfirmedPayload{
			BookingID:  7,
			PlayerName: "Sami",
			PitchName:  "Al Waha — Court 1",
			Location:   "Amman",
			StartTime:  start,
			EndTime:    end,
			Amount:     15.5,
		},
	}
	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	req := cap.decode(t)
	c := req.Messages[0].Content
	if c.TemplateName != "booking_confirmation_ar" {
		t.Errorf("templateName = %q, want booking_confirmation_ar", c.TemplateName)
	}
	if c.Language != "ar" {
		t.Errorf("language = %q, want ar", c.Language)
	}
	if len(c.TemplateData.Buttons) != 0 {
		t.Errorf("utility template must carry no buttons; got %+v", c.TemplateData.Buttons)
	}
	want := []string{
		"Sami",              // {{1}} player
		"Al Waha — Court 1", // {{2}} pitch (composite passed through verbatim)
		"Amman",             // {{3}} location
		"Wed 15 Jul 2026",   // {{4}} date (R6c)
		"20:00",             // {{5}} start (R6c, Asia/Amman 24h)
		"21:00",             // {{6}} end
		"15.5",              // {{7}} amount, number only
		"MRM-7",             // {{8}} reference
	}
	got := c.TemplateData.Body.Placeholders
	if len(got) != 8 {
		t.Fatalf("placeholders = %v (len %d), want 8", got, len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("placeholder[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// T10 (adapter half): the confirmation template is the FIXED booking_confirmation_ar
// — NOT the legacy INFOBIP_BOOKING_CONFIRMED_TEMPLATE. Even with the legacy name
// configured, the adapter must ignore it for this kind. Pins that the old template
// can never fire.
func TestInfobip_ConfirmationIgnoresLegacyTemplate(t *testing.T) {
	ch, cap := newInfobipTestServer(t, http.StatusOK, infobipOKResp) // cfg.Templates.BookingConfirmed = "malaeb_booking_confirmed"

	msg := OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindBookingConfirmed,
		Payload: BookingConfirmedPayload{
			BookingID: 1, PlayerName: "A", PitchName: "P", Location: "L",
			StartTime: time.Now(), EndTime: time.Now().Add(time.Hour), Amount: 10,
		},
	}
	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := cap.decode(t).Messages[0].Content.TemplateName; got == "malaeb_booking_confirmed" {
		t.Fatalf("confirmation used the LEGACY template %q — the old confirmation must never fire", got)
	}
}

// T3: amount is a BARE number (no currency symbol) with trailing zeros trimmed.
func TestFmtAmountNumber(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{15, "15"},
		{15.5, "15.5"},
		{12.75, "12.75"},
		{12.750, "12.75"},
		{100, "100"},
		{0, "0"},
	}
	for _, c := range cases {
		got := fmtAmountNumber(c.in)
		if got != c.want {
			t.Errorf("fmtAmountNumber(%v) = %q, want %q", c.in, got, c.want)
		}
		// The template renders the currency itself — the number must be bare.
		if strings.ContainsAny(got, "دأ") || strings.Contains(got, "JOD") {
			t.Errorf("fmtAmountNumber(%v) must not carry a currency symbol; got %q", c.in, got)
		}
	}
}

// T4: reference is MRM-<id> composed from the booking id — a small integer, never
// a raw UUID.
func TestBookingReference(t *testing.T) {
	if got := bookingReference(42); got != "MRM-42" {
		t.Errorf("bookingReference(42) = %q, want MRM-42", got)
	}
	if got := bookingReference(1); got != "MRM-1" {
		t.Errorf("bookingReference(1) = %q, want MRM-1", got)
	}
}

// 3. Success response → DeliveryResult with ProviderMessageID = infobip:<id>.
func TestInfobip_SuccessMapsToTaggedID(t *testing.T) {
	ch, _ := newInfobipTestServer(t, http.StatusOK, infobipOKResp)

	res, err := ch.Send(context.Background(), OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindOTP,
		Payload:   OTPPayload{Code: "999000"},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("status = %s, want sent", res.Status)
	}
	if res.ProviderMessageID != "infobip:abc-123" {
		t.Errorf("ProviderMessageID = %q, want infobip:abc-123", res.ProviderMessageID)
	}
}

// 4. Error response → failed DeliveryResult, non-nil error, no panic.
func TestInfobip_ErrorResponseFailsCleanly(t *testing.T) {
	const errBody = `{"requestError":{"serviceException":{"messageId":"BAD_REQUEST","text":"Invalid 'to'"}}}`
	ch, _ := newInfobipTestServer(t, http.StatusBadRequest, errBody)

	res, err := ch.Send(context.Background(), OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindOTP,
		Payload:   OTPPayload{Code: "111222"},
	})
	if err == nil {
		t.Fatal("expected an error for a 4xx response")
	}
	if res.Status != DeliveryFailed {
		t.Errorf("status = %s, want failed", res.Status)
	}
	if res.ProviderMessageID != "" {
		t.Errorf("failed send must not carry a provider id; got %q", res.ProviderMessageID)
	}
}

// Missing template for a kind is a clean failure, not a panic.
func TestInfobip_MissingTemplateFails(t *testing.T) {
	cfg := testInfobipCfg("https://example.invalid")
	cfg.Templates.BookingReminder = "" // unset
	ch, err := NewInfobipWhatsAppChannel(cfg)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	res, sendErr := ch.Send(context.Background(), OutboundMessage{
		Recipient: "+962790000000",
		Kind:      KindBookingReminder,
		Payload:   BookingReminderPayload{PitchName: "P"},
	})
	if sendErr == nil || res.Status != DeliveryFailed {
		t.Fatalf("missing template must fail cleanly; status=%s err=%v", res.Status, sendErr)
	}
}

// Construction fails closed when required credentials are absent.
func TestInfobip_NotConfigured(t *testing.T) {
	if _, err := NewInfobipWhatsAppChannel(config.InfobipConfig{}); err != ErrInfobipNotConfigured {
		t.Fatalf("empty config must return ErrInfobipNotConfigured; got %v", err)
	}
}

// ── Provider selection ───────────────────────────────────────────────────────

// 5. Selector defaults to Meta when WHATSAPP_PROVIDER is unset/empty.
func TestProviderSelector_DefaultsToMeta(t *testing.T) {
	p, err := ParseWhatsAppProvider("")
	if err != nil || p != ProviderMeta {
		t.Fatalf("empty provider must default to meta; got %q err=%v", p, err)
	}

	meta := config.WhatsAppConfig{Token: "t", PhoneID: "p"}
	ch, err := NewWhatsAppChannelFor(p, meta, config.InfobipConfig{})
	if err != nil {
		t.Fatalf("construct meta channel: %v", err)
	}
	if _, ok := ch.(*WhatsAppChannel); !ok {
		t.Fatalf("default provider must build the Meta adapter; got %T", ch)
	}
}

// 6. Selector chooses Infobip when configured.
func TestProviderSelector_ChoosesInfobip(t *testing.T) {
	p, err := ParseWhatsAppProvider("infobip")
	if err != nil || p != ProviderInfobip {
		t.Fatalf("provider must parse to infobip; got %q err=%v", p, err)
	}

	ch, err := NewWhatsAppChannelFor(p, config.WhatsAppConfig{}, testInfobipCfg("https://example.invalid"))
	if err != nil {
		t.Fatalf("construct infobip channel: %v", err)
	}
	if _, ok := ch.(*InfobipWhatsAppChannel); !ok {
		t.Fatalf("infobip provider must build the Infobip adapter; got %T", ch)
	}
}

// An unrecognised provider value is an error (fail-loud).
func TestProviderSelector_UnknownErrors(t *testing.T) {
	if _, err := ParseWhatsAppProvider("twilio"); err == nil {
		t.Fatal("unknown provider must error")
	}
}

// ── OTP fallback-safety guardrail: ALLOWLIST / fail-closed (Gate 2.2) ─────────

// fixtureUnregisteredProvider is enum-shaped (same type) but is NOT in the
// providerHasOTPFallback allowlist. It exercises the default-false branch — the
// whole point of the denylist→allowlist reshape. It deliberately does NOT go through
// ParseWhatsAppProvider (unknown STRINGS are rejected upstream at boot); this is the
// second, defense-in-depth layer for a parseable/registered-but-unwired provider.
const fixtureUnregisteredProvider WhatsAppProvider = "__test_unregistered_provider__"

// 1. Meta + WhatsApp OTP → ALLOWED (Meta has SMS fallback wired).
func TestOTPFallbackSafety_MetaWhatsAppOTP_Allowed(t *testing.T) {
	if err := ValidateOTPFallbackSafety(ProviderMeta, ChannelWhatsApp); err != nil {
		t.Fatalf("meta + OTP→WHATSAPP must be allowed; got %v", err)
	}
}

// 2. Infobip + WhatsApp OTP → REFUSED.
func TestOTPFallbackSafety_InfobipWhatsAppOTP_Refused(t *testing.T) {
	err := ValidateOTPFallbackSafety(ProviderInfobip, ChannelWhatsApp)
	if !errors.Is(err, ErrUnsafeWhatsAppOTP) {
		t.Fatalf("infobip + OTP→WHATSAPP must be refused with ErrUnsafeWhatsAppOTP; got %v", err)
	}
	// The message must name the provider and the failure mode.
	for _, want := range []string{string(ProviderInfobip), "no registered OTP fallback", "lock out authentication"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must mention %q; got %q", want, err.Error())
		}
	}
}

// 3. Allowlist membership asserted directly.
func TestProviderHasOTPFallback_Membership(t *testing.T) {
	if !providerHasOTPFallback(ProviderMeta) {
		t.Errorf("providerHasOTPFallback(Meta) = false, want true")
	}
	if providerHasOTPFallback(ProviderInfobip) {
		t.Errorf("providerHasOTPFallback(Infobip) = true, want false")
	}
}

// 4. DEFAULT-DENY: a provider value NOT in the allowlist + WhatsApp OTP → REFUSED.
// This is the real fail-closed property of the reshape.
func TestOTPFallbackSafety_UnregisteredProvider_DefaultDeny(t *testing.T) {
	if providerHasOTPFallback(fixtureUnregisteredProvider) {
		t.Fatalf("an unregistered provider must default to NO fallback")
	}
	err := ValidateOTPFallbackSafety(fixtureUnregisteredProvider, ChannelWhatsApp)
	if !errors.Is(err, ErrUnsafeWhatsAppOTP) {
		t.Fatalf("unregistered provider + OTP→WHATSAPP must be refused (default-deny); got %v", err)
	}
}

// 5. Non-WhatsApp OTP routes pass regardless of provider (OTP never touches the
// WhatsApp quota guard there).
func TestOTPFallbackSafety_NonWhatsAppRoutes_OK(t *testing.T) {
	cases := []struct {
		name     string
		provider WhatsAppProvider
		route    ChannelName
	}{
		{"infobip + OTP→Twilio SMS", ProviderInfobip, ChannelTwilioSMS},
		{"infobip + OTP→SMS", ProviderInfobip, ChannelSMS},
		{"infobip + OTP→FAKE", ProviderInfobip, ChannelFake},
		{"meta + OTP→Twilio SMS", ProviderMeta, ChannelTwilioSMS},
		{"unregistered + OTP→Twilio SMS", fixtureUnregisteredProvider, ChannelTwilioSMS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateOTPFallbackSafety(c.provider, c.route); err != nil {
				t.Fatalf("non-WhatsApp OTP route must pass; got %v", err)
			}
		})
	}
}
