package notification

// Twilio Programmable SMS adapter. This is the ONLY file permitted to know the
// Twilio HTTP contract — per the notification-abstraction rule, no provider call
// or wire shape leaks outside its adapter. It satisfies NotificationChannel, so
// the NotificationService routes to it exactly like any other channel.
//
// Decision (locked): we use RAW Programmable SMS (NOT Twilio Verify). The backend
// stays the source of truth for OTP generation/verification; Twilio only carries
// the bytes. We follow the existing thin-HTTP-client convention (see whatsapp.go)
// rather than adding the twilio-go SDK dependency.
//
// Closed-beta / TRIAL realities this adapter is built for:
//   - sends ONLY to numbers verified in the Twilio console (error 21608 otherwise),
//   - a daily message ceiling (error 63038),
//   - no alphanumeric sender IDs,
//   - every body is prefixed by Twilio with "Sent from a Twilio Trial account".
// These provider errors are mapped to typed errors, logged, and surfaced — they
// never crash the request path.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// Twilio numeric error codes we special-case (see twilio.com/docs/api/errors).
const (
	twilioErrUnverifiedRecipient = 21608 // trial: To is not a verified number
	twilioErrDailyLimit          = 63038 // account exceeded its daily message limit
)

// Errors surfaced by the Twilio adapter. Callers/log sites match these with
// errors.Is; each wraps the provider's code/message for diagnostics.
var (
	// ErrTwilioNotConfigured means credentials are absent at construction.
	ErrTwilioNotConfigured = errors.New("notification/twilio: account sid, auth token and from number are required")
	// ErrTwilioUnverifiedRecipient maps code 21608 — on a trial account the
	// recipient must be verified in the Twilio console first. Permanent for that
	// number until verified.
	ErrTwilioUnverifiedRecipient = errors.New("notification/twilio: recipient is not a verified trial number (21608)")
	// ErrTwilioDailyLimit maps code 63038 — the trial daily ceiling is hit. This
	// is transient (it resets daily).
	ErrTwilioDailyLimit = errors.New("notification/twilio: daily message limit exceeded (63038)")
	// ErrTwilioAPI wraps any other non-2xx response from Twilio.
	ErrTwilioAPI = errors.New("notification/twilio: messaging api returned an error")
	// ErrTwilioUnsupportedKind means the adapter has no body renderer for the kind.
	ErrTwilioUnsupportedKind = errors.New("notification/twilio: unsupported message kind")
)

const (
	defaultTwilioBaseURL = "https://api.twilio.com"
	defaultTwilioTimeout = 10 * time.Second
	// twilioSMSTimeLayout renders booking times in SMS bodies (Amman-local).
	twilioSMSTimeLayout = "Mon 02 Jan 15:04"
)

// TwilioChannel delivers SMS through Twilio's Programmable Messaging API. Safe for
// concurrent use (the http.Client is). Construct with NewTwilioChannel.
type TwilioChannel struct {
	accountSID string
	authToken  string
	from       string
	baseURL    string
	client     *http.Client
}

var _ NotificationChannel = (*TwilioChannel)(nil)

// TwilioOption configures a TwilioChannel at construction.
type TwilioOption func(*TwilioChannel)

// WithTwilioHTTPClient overrides the HTTP client (tests point it at httptest).
// A nil client is ignored so the default (with timeout) is retained.
func WithTwilioHTTPClient(c *http.Client) TwilioOption {
	return func(t *TwilioChannel) {
		if c != nil {
			t.client = c
		}
	}
}

// WithTwilioBaseURL overrides the API base URL (tests point it at httptest).
func WithTwilioBaseURL(u string) TwilioOption {
	return func(t *TwilioChannel) {
		if u != "" {
			t.baseURL = u
		}
	}
}

// NewTwilioChannel builds the adapter from configuration. It fails with
// ErrTwilioNotConfigured when any credential is missing, matching the loud
// failure of the other configured channels.
func NewTwilioChannel(cfg config.TwilioConfig, opts ...TwilioOption) (*TwilioChannel, error) {
	if strings.TrimSpace(cfg.AccountSID) == "" ||
		strings.TrimSpace(cfg.AuthToken) == "" ||
		strings.TrimSpace(cfg.FromNumber) == "" {
		return nil, ErrTwilioNotConfigured
	}
	t := &TwilioChannel{
		accountSID: cfg.AccountSID,
		authToken:  cfg.AuthToken,
		from:       cfg.FromNumber,
		baseURL:    defaultTwilioBaseURL,
		client:     &http.Client{Timeout: defaultTwilioTimeout},
	}
	for _, o := range opts {
		o(t)
	}
	return t, nil
}

// twilioMessageResponse is the subset of Twilio's Message resource / error body
// we read. On success Sid/Status are populated; on error Code/Message are.
type twilioMessageResponse struct {
	Sid     string `json:"sid"`
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Send renders msg to an SMS body and POSTs it to Twilio. Provider errors are
// mapped to typed errors and returned as a failed DeliveryResult (never a panic),
// so the caller (synchronous OTP path or outbox worker) logs and surfaces a clean
// error rather than crashing.
func (t *TwilioChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	body, err := renderTwilioBody(msg)
	if err != nil {
		return failedTwilio(err)
	}

	form := url.Values{}
	form.Set("To", msg.Recipient)
	form.Set("From", t.from)
	form.Set("Body", body)

	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json",
		strings.TrimRight(t.baseURL, "/"), t.accountSID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return failedTwilio(fmt.Errorf("notification/twilio: build request: %w", err))
	}
	req.SetBasicAuth(t.accountSID, t.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// A network/transport failure is transient — surface it for retry/logging.
		return failedTwilio(fmt.Errorf("notification/twilio: http call failed: %w", err))
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var parsed twilioMessageResponse
	_ = json.Unmarshal(raw, &parsed)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return failedTwilio(mapTwilioError(resp.StatusCode, parsed))
	}

	return DeliveryResult{Status: DeliverySent, ProviderMessageID: parsed.Sid}, nil
}

// mapTwilioError translates a non-2xx Twilio response into a typed error, wrapping
// the trial-specific codes so callers can react (and logs read clearly).
func mapTwilioError(httpStatus int, r twilioMessageResponse) error {
	switch r.Code {
	case twilioErrUnverifiedRecipient:
		return fmt.Errorf("%w: %s", ErrTwilioUnverifiedRecipient, r.Message)
	case twilioErrDailyLimit:
		return fmt.Errorf("%w: %s", ErrTwilioDailyLimit, r.Message)
	default:
		return fmt.Errorf("%w: http=%d code=%d message=%q",
			ErrTwilioAPI, httpStatus, r.Code, r.Message)
	}
}

// renderTwilioBody turns a channel-agnostic message into an SMS body. SMS is
// free-form (unlike WhatsApp templates), so we control the copy. Booking times
// render in Asia/Amman civil time (the instants are UTC).
func renderTwilioBody(msg OutboundMessage) (string, error) {
	switch p := msg.Payload.(type) {
	case OTPPayload:
		mins := max(p.ExpiresInSeconds/60, 1)
		return fmt.Sprintf("رمز التحقق في ملاعب: %s — صالح لمدة %s دقيقة. لا تشاركه مع أحد.",
			p.Code, strconv.Itoa(mins)), nil
	case BookingConfirmedPayload:
		return fmt.Sprintf("تم تأكيد حجزك في %s يوم %s.",
			p.PitchName, twilioBookingTime(p.StartTime)), nil
	case BookingReminderPayload:
		return fmt.Sprintf("تذكير: لديك حجز في %s يوم %s.",
			p.PitchName, twilioBookingTime(p.StartTime)), nil
	case BookingCancelledPayload:
		return fmt.Sprintf("تم إلغاء حجزك في %s يوم %s.",
			p.PitchName, twilioBookingTime(p.StartTime)), nil
	case BookingRejectedPayload:
		return fmt.Sprintf("تعذّر تأكيد حجزك في %s يوم %s.",
			p.PitchName, twilioBookingTime(p.StartTime)), nil
	default:
		return "", fmt.Errorf("%w: %s", ErrTwilioUnsupportedKind, msg.Kind)
	}
}

func twilioBookingTime(t time.Time) string {
	return timeutil.InAmman(t).Format(twilioSMSTimeLayout)
}

func failedTwilio(err error) (DeliveryResult, error) {
	return DeliveryResult{Status: DeliveryFailed, Err: err}, err
}
