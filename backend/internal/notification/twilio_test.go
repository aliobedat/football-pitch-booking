package notification

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ali/football-pitch-api/internal/config"
)

func testTwilio(t *testing.T, srv *httptest.Server) *TwilioChannel {
	t.Helper()
	ch, err := NewTwilioChannel(
		config.TwilioConfig{AccountSID: "ACtest", AuthToken: "tok", FromNumber: "+15005550006"},
		WithTwilioBaseURL(srv.URL),
		WithTwilioHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewTwilioChannel: %v", err)
	}
	return ch
}

func TestNewTwilioChannel_RequiresCredentials(t *testing.T) {
	for _, c := range []config.TwilioConfig{
		{},
		{AccountSID: "AC", AuthToken: "t"},   // missing from
		{AccountSID: "AC", FromNumber: "+1"}, // missing token
		{AuthToken: "t", FromNumber: "+1"},   // missing sid
	} {
		if _, err := NewTwilioChannel(c); !errors.Is(err, ErrTwilioNotConfigured) {
			t.Errorf("cfg %+v: err = %v, want ErrTwilioNotConfigured", c, err)
		}
	}
}

func TestTwilio_Success(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		// Basic auth must be present.
		if u, p, ok := r.BasicAuth(); !ok || u != "ACtest" || p != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM123","status":"queued"}`))
	}))
	defer srv.Close()

	res, err := testTwilio(t, srv).Send(context.Background(), sampleMessage(KindOTP))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Status != DeliverySent || res.ProviderMessageID != "SM123" {
		t.Errorf("res = %+v, want sent/SM123", res)
	}
	if gotForm.Get("To") != "+962790000000" || gotForm.Get("From") != "+15005550006" {
		t.Errorf("To/From wrong: %v", gotForm)
	}
	if gotForm.Get("Body") == "" {
		t.Error("Body is empty")
	}
}

func TestTwilio_TrialErrorsAreTypedAndDoNotCrash(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		wantErr error
	}{
		{"unverified recipient 21608", 21608, ErrTwilioUnverifiedRecipient},
		{"daily limit 63038", 63038, ErrTwilioDailyLimit},
		{"other error", 30007, ErrTwilioAPI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":` + itoa(c.code) + `,"message":"trial error"}`))
			}))
			defer srv.Close()

			res, err := testTwilio(t, srv).Send(context.Background(), sampleMessage(KindOTP))
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if res.Status != DeliveryFailed {
				t.Errorf("status = %q, want failed", res.Status)
			}
			// Result Err mirrors the returned error (no panic, clean surface).
			if !errors.Is(res.Err, c.wantErr) {
				t.Errorf("res.Err = %v, want %v", res.Err, c.wantErr)
			}
		})
	}
}

// itoa avoids importing strconv just for the test literals above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
