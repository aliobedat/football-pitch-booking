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

	"github.com/ali/football-pitch-api/internal/config"
)

// testWhatsAppConfig returns a fully-populated config pointing the adapter at the
// given base URL (an httptest.Server in tests). Dummy credentials throughout —
// Meta business verification is pending.
func testWhatsAppConfig(baseURL string) config.WhatsAppConfig {
	return config.WhatsAppConfig{
		Token:      "dummy-token",
		PhoneID:    "123456789",
		APIBaseURL: baseURL,
		APIVersion: "v21.0",
		Templates: config.WhatsAppTemplates{
			Language:         "en",
			OTP:              "malaeb_otp",
			BookingConfirmed: "malaeb_booking_confirmed",
			BookingCancelled: "malaeb_booking_cancelled",
			BookingReminder:  "malaeb_booking_reminder",
		},
	}
}

// newTestWhatsApp builds a WhatsAppChannel whose HTTP client targets srv.
func newTestWhatsApp(t *testing.T, srv *httptest.Server, mutate func(*config.WhatsAppConfig)) *WhatsAppChannel {
	t.Helper()
	cfg := testWhatsAppConfig(srv.URL)
	if mutate != nil {
		mutate(&cfg)
	}
	wa, err := NewWhatsAppChannel(cfg, WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}
	return wa
}

func TestNewWhatsAppChannel_RequiresCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.WhatsAppConfig
	}{
		{"missing token", config.WhatsAppConfig{PhoneID: "123"}},
		{"missing phone id", config.WhatsAppConfig{Token: "tok"}},
		{"both missing", config.WhatsAppConfig{}},
		{"whitespace only", config.WhatsAppConfig{Token: "  ", PhoneID: "\t"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewWhatsAppChannel(c.cfg)
			if !errors.Is(err, ErrWhatsAppNotConfigured) {
				t.Fatalf("err = %v, want errors.Is(_, ErrWhatsAppNotConfigured)", err)
			}
		})
	}
}

// TestWhatsAppChannel_OTP_Success is the happy path: it asserts the request hits
// the right endpoint with the right auth header and template/parameter shape, and
// that the provider message id is parsed out of the response.
func TestWhatsAppChannel_OTP_Success(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotReq waRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.HBgABC123"}]}`))
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, nil)

	res, err := wa.Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if res.Status != DeliverySent {
		t.Errorf("Status = %q, want %q", res.Status, DeliverySent)
	}
	if res.ProviderMessageID != "wamid.HBgABC123" {
		t.Errorf("ProviderMessageID = %q, want %q", res.ProviderMessageID, "wamid.HBgABC123")
	}

	// Endpoint: /{version}/{phone_id}/messages
	if want := "/v21.0/123456789/messages"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if want := "Bearer dummy-token"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	// Body shape: template name, recipient without '+', code in body + button.
	if gotReq.MessagingProduct != "whatsapp" {
		t.Errorf("messaging_product = %q, want whatsapp", gotReq.MessagingProduct)
	}
	if gotReq.To != "962790000000" {
		t.Errorf("to = %q, want 962790000000 (leading + stripped)", gotReq.To)
	}
	if gotReq.Template.Name != "malaeb_otp" {
		t.Errorf("template.name = %q, want malaeb_otp", gotReq.Template.Name)
	}
	if gotReq.Template.Language.Code != "en" {
		t.Errorf("template.language.code = %q, want en", gotReq.Template.Language.Code)
	}
	if len(gotReq.Template.Components) != 2 {
		t.Fatalf("components = %d, want 2 (body + button)", len(gotReq.Template.Components))
	}
	body := gotReq.Template.Components[0]
	if body.Type != "body" || len(body.Parameters) != 1 || body.Parameters[0].Text != "123456" {
		t.Errorf("body component = %+v, want code 123456", body)
	}
	btn := gotReq.Template.Components[1]
	if btn.Type != "button" || btn.SubType != "url" || btn.Index != "0" ||
		len(btn.Parameters) != 1 || btn.Parameters[0].Text != "123456" {
		t.Errorf("button component = %+v, want copy-code with 123456", btn)
	}
}

// TestWhatsAppChannel_BookingTemplates checks that confirmed/cancelled messages
// resolve their own template names and pass the expected number of body params.
func TestWhatsAppChannel_BookingTemplates(t *testing.T) {
	cases := []struct {
		kind       MessageKind
		wantTmpl   string
		wantParams int
	}{
		{KindBookingConfirmed, "malaeb_booking_confirmed", 3},
		{KindBookingCancelled, "malaeb_booking_cancelled", 3},
		{KindBookingReminder, "malaeb_booking_reminder", 3},
	}

	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			var gotReq waRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &gotReq)
				_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OK"}]}`))
			}))
			defer srv.Close()

			wa := newTestWhatsApp(t, srv, nil)
			res, err := wa.Send(context.Background(), sampleMessage(c.kind))
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if res.Status != DeliverySent || res.ProviderMessageID != "wamid.OK" {
				t.Errorf("got %+v, want sent/wamid.OK", res)
			}
			if gotReq.Template.Name != c.wantTmpl {
				t.Errorf("template.name = %q, want %q", gotReq.Template.Name, c.wantTmpl)
			}
			if len(gotReq.Template.Components) != 1 {
				t.Fatalf("components = %d, want 1 (body)", len(gotReq.Template.Components))
			}
			if got := len(gotReq.Template.Components[0].Parameters); got != c.wantParams {
				t.Errorf("body params = %d, want %d", got, c.wantParams)
			}
		})
	}
}

// TestWhatsAppChannel_APIError maps a non-2xx Meta response to a failed result
// wrapping ErrWhatsAppAPI (no panic, error surfaced for fallback).
func TestWhatsAppChannel_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token","type":"OAuthException","code":190,"fbtrace_id":"Abc"}}`))
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, nil)
	res, err := wa.Send(context.Background(), sampleMessage(KindOTP))
	if !errors.Is(err, ErrWhatsAppAPI) {
		t.Fatalf("err = %v, want errors.Is(_, ErrWhatsAppAPI)", err)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
	}
	if !errors.Is(res.Err, ErrWhatsAppAPI) {
		t.Errorf("res.Err = %v, want it to wrap ErrWhatsAppAPI", res.Err)
	}
}

// TestWhatsAppChannel_MissingTemplate treats an unconfigured template name (the
// real-world unapproved-template case) as a delivery failure WITHOUT calling the
// API at all.
func TestWhatsAppChannel_MissingTemplate(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"messages":[{"id":"x"}]}`))
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, func(c *config.WhatsAppConfig) {
		c.Templates.OTP = "" // simulate an unapproved/absent OTP template
	})

	res, err := wa.Send(context.Background(), sampleMessage(KindOTP))
	if !errors.Is(err, ErrWhatsAppNoTemplate) {
		t.Fatalf("err = %v, want errors.Is(_, ErrWhatsAppNoTemplate)", err)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
	}
	if called {
		t.Error("API was called despite missing template; should fail before dispatch")
	}
}

// TestWhatsAppChannel_UnsupportedKind ensures kinds the adapter does not map
// (e.g. booking_rejected, absent in the instant-booking flow) fail cleanly.
func TestWhatsAppChannel_UnsupportedKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for an unsupported kind")
	}))
	defer srv.Close()

	wa := newTestWhatsApp(t, srv, nil)
	res, err := wa.Send(context.Background(), sampleMessage(KindBookingRejected))
	if !errors.Is(err, ErrWhatsAppUnsupportedKind) {
		t.Fatalf("err = %v, want errors.Is(_, ErrWhatsAppUnsupportedKind)", err)
	}
	if res.Status != DeliveryFailed {
		t.Errorf("Status = %q, want %q", res.Status, DeliveryFailed)
	}
}
