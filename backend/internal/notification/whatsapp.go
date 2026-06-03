package notification

// PART 4 scope: the WhatsApp delivery adapter. This is the ONLY file in the
// codebase permitted to know the shape of the Meta WhatsApp Cloud API — per the
// notification-abstraction rule, no Meta SDK call or request shape may leak
// outside this file. The adapter maps each channel-agnostic OutboundMessage onto
// the provider-specific template structure, performs the HTTP POST, and parses
// the response into a DeliveryResult.
//
// Meta business verification is pending, so this runs against DUMMY credentials;
// the wire format below is the real Cloud API contract regardless. AUTHENTICATION
// (OTP) and UTILITY (booking) templates are referenced by name from config —
// their bodies are fixed/approved on Meta's side, we only supply the variable
// parameters.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ali/football-pitch-api/internal/config"
)

// Errors surfaced by the WhatsApp adapter. Callers (notably FallbackChannel) can
// match these with errors.Is to decide whether to fall back to another channel.
var (
	// ErrWhatsAppNotConfigured means the token or phone id is absent. Construction
	// fails loudly so a WHATSAPP-selected deployment cannot silently no-op.
	ErrWhatsAppNotConfigured = errors.New("notification/whatsapp: token and phone id are required")
	// ErrWhatsAppNoTemplate means no approved template name is configured for the
	// message kind being sent. Treated as a delivery failure (→ SMS fallback),
	// modelling the real-world case of an unapproved AUTHENTICATION template.
	ErrWhatsAppNoTemplate = errors.New("notification/whatsapp: no template configured for message kind")
	// ErrWhatsAppUnsupportedKind means the adapter has no mapping for the kind.
	ErrWhatsAppUnsupportedKind = errors.New("notification/whatsapp: unsupported message kind")
	// ErrWhatsAppAPI wraps a non-2xx response from the Meta Cloud API.
	ErrWhatsAppAPI = errors.New("notification/whatsapp: meta cloud api returned an error")
)

const (
	defaultWhatsAppBaseURL    = "https://graph.facebook.com"
	defaultWhatsAppAPIVersion = "v21.0"
	defaultWhatsAppTimeout    = 10 * time.Second
	// bookingTimeLayout renders booking timestamps for UTILITY template params.
	bookingTimeLayout = "Mon 02 Jan 2006 15:04"
)

// WhatsAppChannel delivers through the Meta WhatsApp Cloud API. It satisfies
// NotificationChannel and is safe for concurrent use (the underlying http.Client
// is). Construct it with NewWhatsAppChannel.
type WhatsAppChannel struct {
	cfg    config.WhatsAppConfig
	client *http.Client
}

var _ NotificationChannel = (*WhatsAppChannel)(nil)

// WhatsAppOption configures a WhatsAppChannel at construction time.
type WhatsAppOption func(*WhatsAppChannel)

// WithHTTPClient overrides the HTTP client used for Cloud API calls. Primarily
// for tests, which point the client at an httptest.Server. A nil client is
// ignored so the default (with timeout) is retained.
func WithHTTPClient(c *http.Client) WhatsAppOption {
	return func(w *WhatsAppChannel) {
		if c != nil {
			w.client = c
		}
	}
}

// NewWhatsAppChannel builds a WhatsApp adapter from configuration. It fails with
// ErrWhatsAppNotConfigured when the token or phone id is missing; endpoint
// coordinates and template language fall back to sane defaults.
func NewWhatsAppChannel(cfg config.WhatsAppConfig, opts ...WhatsAppOption) (*WhatsAppChannel, error) {
	if strings.TrimSpace(cfg.Token) == "" || strings.TrimSpace(cfg.PhoneID) == "" {
		return nil, ErrWhatsAppNotConfigured
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultWhatsAppBaseURL
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = defaultWhatsAppAPIVersion
	}
	if cfg.Templates.Language == "" {
		cfg.Templates.Language = "en"
	}

	w := &WhatsAppChannel{
		cfg:    cfg,
		client: &http.Client{Timeout: defaultWhatsAppTimeout},
	}
	for _, o := range opts {
		o(w)
	}
	return w, nil
}

// Send renders msg into the appropriate Cloud API template request, POSTs it, and
// parses the response. On any failure it returns a DeliveryFailed result whose
// Err mirrors the returned error, so a caller may use either; this is also the
// signal FallbackChannel uses to route to the SMS fallback.
func (w *WhatsAppChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	body, err := w.buildRequest(msg)
	if err != nil {
		return failedWhatsApp(err)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return failedWhatsApp(fmt.Errorf("notification/whatsapp: marshal request: %w", err))
	}

	endpoint := fmt.Sprintf("%s/%s/%s/messages",
		strings.TrimRight(w.cfg.APIBaseURL, "/"), w.cfg.APIVersion, w.cfg.PhoneID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return failedWhatsApp(fmt.Errorf("notification/whatsapp: build http request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return failedWhatsApp(fmt.Errorf("notification/whatsapp: http call failed: %w", err))
	}
	defer resp.Body.Close()

	// Cap the read: a misbehaving upstream should not let us allocate unbounded.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr waErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		return failedWhatsApp(fmt.Errorf("%w: status=%d code=%d type=%q message=%q",
			ErrWhatsAppAPI, resp.StatusCode, apiErr.Error.Code, apiErr.Error.Type, apiErr.Error.Message))
	}

	var ok waSuccessResponse
	if err := json.Unmarshal(respBody, &ok); err != nil {
		return failedWhatsApp(fmt.Errorf("notification/whatsapp: decode response: %w", err))
	}

	var id string
	if len(ok.Messages) > 0 {
		id = ok.Messages[0].ID
	}
	return DeliveryResult{Status: DeliverySent, ProviderMessageID: id}, nil
}

// buildRequest maps an OutboundMessage onto a Cloud API template request. Each
// supported kind resolves its configured template name and supplies only the
// variable parameters; the body copy itself is fixed by the approved template.
func (w *WhatsAppChannel) buildRequest(msg OutboundMessage) (waRequest, error) {
	// Meta expects the recipient as an E.164 number without the leading '+'.
	to := strings.TrimPrefix(msg.Recipient, "+")
	lang := waLanguage{Code: w.cfg.Templates.Language}

	switch p := msg.Payload.(type) {
	case OTPPayload:
		name := w.cfg.Templates.OTP
		if name == "" {
			return waRequest{}, fmt.Errorf("%w: %s", ErrWhatsAppNoTemplate, KindOTP)
		}
		// AUTHENTICATION copy-code template: the code is the single body param and
		// is echoed into the copy-code button (index 0). We control only the
		// button — the body text is fixed by Meta.
		return waRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "template",
			Template: waTemplate{
				Name:     name,
				Language: lang,
				Components: []waComponent{
					{Type: "body", Parameters: []waParameter{{Type: "text", Text: p.Code}}},
					{
						Type:       "button",
						SubType:    "url",
						Index:      "0",
						Parameters: []waParameter{{Type: "text", Text: p.Code}},
					},
				},
			},
		}, nil

	case BookingConfirmedPayload:
		name := w.cfg.Templates.BookingConfirmed
		if name == "" {
			return waRequest{}, fmt.Errorf("%w: %s", ErrWhatsAppNoTemplate, KindBookingConfirmed)
		}
		return waRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "template",
			Template: waTemplate{
				Name:     name,
				Language: lang,
				Components: []waComponent{
					{Type: "body", Parameters: []waParameter{
						{Type: "text", Text: p.PitchName},
						{Type: "text", Text: p.StartTime.Format(bookingTimeLayout)},
						{Type: "text", Text: p.EndTime.Format(bookingTimeLayout)},
					}},
				},
			},
		}, nil

	case BookingCancelledPayload:
		name := w.cfg.Templates.BookingCancelled
		if name == "" {
			return waRequest{}, fmt.Errorf("%w: %s", ErrWhatsAppNoTemplate, KindBookingCancelled)
		}
		return waRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "template",
			Template: waTemplate{
				Name:     name,
				Language: lang,
				Components: []waComponent{
					{Type: "body", Parameters: []waParameter{
						{Type: "text", Text: p.PitchName},
						{Type: "text", Text: p.StartTime.Format(bookingTimeLayout)},
						{Type: "text", Text: p.Reason},
					}},
				},
			},
		}, nil

	case BookingReminderPayload:
		// UTILITY-category reminder template (PART 7): variable params are the
		// pitch name and the booking's start/end, echoing the confirmation copy.
		name := w.cfg.Templates.BookingReminder
		if name == "" {
			return waRequest{}, fmt.Errorf("%w: %s", ErrWhatsAppNoTemplate, KindBookingReminder)
		}
		return waRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "template",
			Template: waTemplate{
				Name:     name,
				Language: lang,
				Components: []waComponent{
					{Type: "body", Parameters: []waParameter{
						{Type: "text", Text: p.PitchName},
						{Type: "text", Text: p.StartTime.Format(bookingTimeLayout)},
						{Type: "text", Text: p.EndTime.Format(bookingTimeLayout)},
					}},
				},
			},
		}, nil

	default:
		return waRequest{}, fmt.Errorf("%w: %s", ErrWhatsAppUnsupportedKind, msg.Kind)
	}
}

// failedWhatsApp builds the failure pair, keeping DeliveryResult.Err and the
// returned error in sync (mirrors Service.failed).
func failedWhatsApp(err error) (DeliveryResult, error) {
	return DeliveryResult{Status: DeliveryFailed, Err: err}, err
}

// ── Meta Cloud API wire types ───────────────────────────────────────────────
// These mirror the Cloud API JSON contract and exist ONLY inside this adapter.

type waRequest struct {
	MessagingProduct string     `json:"messaging_product"`
	To               string     `json:"to"`
	Type             string     `json:"type"`
	Template         waTemplate `json:"template"`
}

type waTemplate struct {
	Name       string        `json:"name"`
	Language   waLanguage    `json:"language"`
	Components []waComponent `json:"components,omitempty"`
}

type waLanguage struct {
	Code string `json:"code"`
}

type waComponent struct {
	Type       string        `json:"type"`
	SubType    string        `json:"sub_type,omitempty"`
	Index      string        `json:"index,omitempty"`
	Parameters []waParameter `json:"parameters,omitempty"`
}

type waParameter struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type waSuccessResponse struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
}

type waErrorResponse struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      int    `json:"code"`
		FBTraceID string `json:"fbtrace_id"`
	} `json:"error"`
}
