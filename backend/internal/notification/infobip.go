package notification

// GATE 2 / PR-1: the Infobip WhatsApp delivery adapter — a SECOND provider behind
// the same NotificationChannel seam as the Meta-direct adapter (whatsapp.go). Like
// every other adapter in this package it is the ONLY file permitted to know its
// provider's wire contract: no Infobip request/response struct leaks past Send.
//
// It maps each channel-agnostic OutboundMessage onto an Infobip "send WhatsApp
// template" request (AUTHENTICATION template for OTP, UTILITY templates for booking
// events), performs the HTTP POST, and parses the response into a DeliveryResult.
// Provider message ids are normalised to the opaque form "infobip:<messageId>" so
// the rest of the system (outbox linkage, future status webhooks) treats them as
// provider-tagged opaque strings and never assumes a Meta wamid shape.
//
// SCOPE (PR-1): OUTBOUND sending + provider selection only. Infobip INBOUND delivery
// webhooks are PR-2 and deliberately absent here. No SMS fallback is wired for this
// provider in this PR.

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

// Errors surfaced by the Infobip adapter. Callers/log sites match these with
// errors.Is; each wraps the provider's diagnostics without ever logging the API key.
var (
	// ErrInfobipNotConfigured means base URL, API key, or sender is absent.
	// Construction fails loudly so an infobip-selected deployment cannot silently
	// no-op (fail-closed, see NewInfobipWhatsAppChannel).
	ErrInfobipNotConfigured = errors.New("notification/infobip: base url, api key and sender are required")
	// ErrInfobipNoTemplate means no approved template name is configured for the
	// message kind being sent. Treated as a delivery failure.
	ErrInfobipNoTemplate = errors.New("notification/infobip: no template configured for message kind")
	// ErrInfobipUnsupportedKind means the adapter has no mapping for the kind
	// (parity with the Meta adapter, which supports OTP + the three booking kinds).
	ErrInfobipUnsupportedKind = errors.New("notification/infobip: unsupported message kind")
	// ErrInfobipAPI wraps a non-2xx (or unaccepted) response from Infobip.
	ErrInfobipAPI = errors.New("notification/infobip: api returned an error")
)

const (
	defaultInfobipTimeout = 10 * time.Second
	// infobipTemplateSendPath is Infobip's "send WhatsApp template message" endpoint.
	infobipTemplateSendPath = "/whatsapp/1/message/template"
	// infobipProviderIDPrefix tags every returned message id so downstream code
	// treats it as an opaque, provider-scoped identifier.
	infobipProviderIDPrefix = "infobip:"
	// infobipOTPButtonType models the WhatsApp authentication copy-code/one-tap
	// button: Infobip carries the OTP as the parameter of a URL-type button, exactly
	// mirroring the Meta adapter's copy-code button. Verify against your approved
	// Infobip template before go-live (see manual ops steps).
	infobipOTPButtonType = "URL"
)

// InfobipWhatsAppChannel delivers through the Infobip WhatsApp API. It satisfies
// NotificationChannel and is safe for concurrent use (the http.Client is).
// Construct it with NewInfobipWhatsAppChannel.
type InfobipWhatsAppChannel struct {
	cfg    config.InfobipConfig
	client *http.Client
}

var _ NotificationChannel = (*InfobipWhatsAppChannel)(nil)

// InfobipOption configures an InfobipWhatsAppChannel at construction time.
type InfobipOption func(*InfobipWhatsAppChannel)

// WithInfobipHTTPClient overrides the HTTP client (tests point it at httptest).
// A nil client is ignored so the default (with timeout) is retained.
func WithInfobipHTTPClient(c *http.Client) InfobipOption {
	return func(i *InfobipWhatsAppChannel) {
		if c != nil {
			i.client = c
		}
	}
}

// WithInfobipBaseURL overrides the API base URL (tests point it at httptest).
func WithInfobipBaseURL(u string) InfobipOption {
	return func(i *InfobipWhatsAppChannel) {
		if u != "" {
			i.cfg.BaseURL = u
		}
	}
}

// NewInfobipWhatsAppChannel builds the adapter from configuration. It fails with
// ErrInfobipNotConfigured when the base URL, API key, or sender is missing — a
// fail-closed startup error so provider=infobip cannot run half-configured. The
// template language falls back to "en".
func NewInfobipWhatsAppChannel(cfg config.InfobipConfig, opts ...InfobipOption) (*InfobipWhatsAppChannel, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" ||
		strings.TrimSpace(cfg.APIKey) == "" ||
		strings.TrimSpace(cfg.Sender) == "" {
		return nil, ErrInfobipNotConfigured
	}
	if cfg.Templates.Language == "" {
		cfg.Templates.Language = "en"
	}
	i := &InfobipWhatsAppChannel{
		cfg:    cfg,
		client: &http.Client{Timeout: defaultInfobipTimeout},
	}
	for _, o := range opts {
		o(i)
	}
	return i, nil
}

// Send renders msg into the appropriate Infobip template request, POSTs it, and
// parses the response. On any failure it returns a DeliveryFailed result whose Err
// mirrors the returned error (never a panic), matching the convention used across
// the notification channels.
func (i *InfobipWhatsAppChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	body, err := i.buildRequest(msg)
	if err != nil {
		return failedInfobip(err)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return failedInfobip(fmt.Errorf("notification/infobip: marshal request: %w", err))
	}

	endpoint := strings.TrimRight(i.cfg.BaseURL, "/") + infobipTemplateSendPath

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return failedInfobip(fmt.Errorf("notification/infobip: build http request: %w", err))
	}
	// Infobip authenticates with the "App <apiKey>" Authorization scheme. The key is
	// a secret: it is set on the wire only, never logged.
	req.Header.Set("Authorization", "App "+i.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := i.client.Do(req)
	if err != nil {
		return failedInfobip(fmt.Errorf("notification/infobip: http call failed: %w", err))
	}
	defer resp.Body.Close()

	// Cap the read: a misbehaving upstream should not let us allocate unbounded.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr infobipErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		return failedInfobip(fmt.Errorf("%w: status=%d messageId=%q text=%q",
			ErrInfobipAPI, resp.StatusCode,
			apiErr.RequestError.ServiceException.MessageID,
			apiErr.RequestError.ServiceException.Text))
	}

	var ok infobipSendResponse
	if err := json.Unmarshal(respBody, &ok); err != nil {
		return failedInfobip(fmt.Errorf("notification/infobip: decode response: %w", err))
	}

	if len(ok.Messages) == 0 || strings.TrimSpace(ok.Messages[0].MessageID) == "" {
		// A 2xx with no message id means the send was not accepted — surface it as a
		// failure rather than reporting a phantom success with an empty provider id.
		return failedInfobip(fmt.Errorf("%w: 2xx response carried no messageId", ErrInfobipAPI))
	}

	return DeliveryResult{
		Status:            DeliverySent,
		ProviderMessageID: infobipProviderIDPrefix + ok.Messages[0].MessageID,
	}, nil
}

// buildRequest maps an OutboundMessage onto an Infobip template send request. Each
// supported kind resolves its configured template name and supplies only the
// variable placeholders; the body copy itself is fixed by the approved template.
// Kind parity with the Meta adapter: OTP + booking confirmed/cancelled/reminder.
func (i *InfobipWhatsAppChannel) buildRequest(msg OutboundMessage) (infobipTemplateRequest, error) {
	// Infobip expects the recipient in international format without the leading '+'.
	to := strings.TrimPrefix(msg.Recipient, "+")
	lang := i.cfg.Templates.Language

	switch p := msg.Payload.(type) {
	case OTPPayload:
		name := i.cfg.Templates.OTP
		if name == "" {
			return infobipTemplateRequest{}, fmt.Errorf("%w: %s", ErrInfobipNoTemplate, KindOTP)
		}
		// AUTHENTICATION template: the code is the single body placeholder and is
		// echoed into the copy-code button. We control only the parameters — the body
		// text is fixed by the approved template.
		return i.wrap(to, infobipTemplateContent{
			TemplateName: name,
			Language:     lang,
			TemplateData: infobipTemplateData{
				Body:    infobipTemplateBody{Placeholders: []string{p.Code}},
				Buttons: []infobipTemplateButton{{Type: infobipOTPButtonType, Parameter: p.Code}},
			},
		}), nil

	case BookingConfirmedPayload:
		name := i.cfg.Templates.BookingConfirmed
		if name == "" {
			return infobipTemplateRequest{}, fmt.Errorf("%w: %s", ErrInfobipNoTemplate, KindBookingConfirmed)
		}
		return i.wrap(to, infobipTemplateContent{
			TemplateName: name,
			Language:     lang,
			TemplateData: infobipTemplateData{Body: infobipTemplateBody{Placeholders: []string{
				p.PitchName, fmtBookingTime(p.StartTime), fmtBookingTime(p.EndTime),
			}}},
		}), nil

	case BookingCancelledPayload:
		name := i.cfg.Templates.BookingCancelled
		if name == "" {
			return infobipTemplateRequest{}, fmt.Errorf("%w: %s", ErrInfobipNoTemplate, KindBookingCancelled)
		}
		return i.wrap(to, infobipTemplateContent{
			TemplateName: name,
			Language:     lang,
			TemplateData: infobipTemplateData{Body: infobipTemplateBody{Placeholders: []string{
				p.PitchName, fmtBookingTime(p.StartTime), p.Reason,
			}}},
		}), nil

	case BookingReminderPayload:
		name := i.cfg.Templates.BookingReminder
		if name == "" {
			return infobipTemplateRequest{}, fmt.Errorf("%w: %s", ErrInfobipNoTemplate, KindBookingReminder)
		}
		return i.wrap(to, infobipTemplateContent{
			TemplateName: name,
			Language:     lang,
			TemplateData: infobipTemplateData{Body: infobipTemplateBody{Placeholders: []string{
				p.PitchName, fmtBookingTime(p.StartTime), fmtBookingTime(p.EndTime),
			}}},
		}), nil

	default:
		return infobipTemplateRequest{}, fmt.Errorf("%w: %s", ErrInfobipUnsupportedKind, msg.Kind)
	}
}

// wrap assembles the single-message envelope Infobip expects.
func (i *InfobipWhatsAppChannel) wrap(to string, content infobipTemplateContent) infobipTemplateRequest {
	return infobipTemplateRequest{
		Messages: []infobipTemplateMessage{{
			From:    i.cfg.Sender,
			To:      to,
			Content: content,
		}},
	}
}

// failedInfobip builds the failure pair, keeping DeliveryResult.Err and the
// returned error in sync (mirrors failedWhatsApp / failedTwilio).
func failedInfobip(err error) (DeliveryResult, error) {
	return DeliveryResult{Status: DeliveryFailed, Err: err}, err
}

// ── Infobip WhatsApp template wire types (local to this adapter) ─────────────
// These mirror the Infobip JSON contract and exist ONLY inside this adapter.

type infobipTemplateRequest struct {
	Messages []infobipTemplateMessage `json:"messages"`
}

type infobipTemplateMessage struct {
	From    string                 `json:"from"`
	To      string                 `json:"to"`
	Content infobipTemplateContent `json:"content"`
}

type infobipTemplateContent struct {
	TemplateName string              `json:"templateName"`
	TemplateData infobipTemplateData `json:"templateData"`
	Language     string              `json:"language"`
}

type infobipTemplateData struct {
	Body    infobipTemplateBody     `json:"body"`
	Buttons []infobipTemplateButton `json:"buttons,omitempty"`
}

type infobipTemplateBody struct {
	Placeholders []string `json:"placeholders"`
}

type infobipTemplateButton struct {
	Type      string `json:"type"`
	Parameter string `json:"parameter"`
}

type infobipSendResponse struct {
	Messages []struct {
		MessageID string `json:"messageId"`
		Status    struct {
			GroupName string `json:"groupName"`
			Name      string `json:"name"`
		} `json:"status"`
	} `json:"messages"`
}

type infobipErrorResponse struct {
	RequestError struct {
		ServiceException struct {
			MessageID string `json:"messageId"`
			Text      string `json:"text"`
		} `json:"serviceException"`
	} `json:"requestError"`
}

// ── Provider selection ───────────────────────────────────────────────────────
// The WhatsApp seam now has two interchangeable providers. Selection is config-
// driven (WHATSAPP_PROVIDER) and defaults to Meta, the fallback-safe provider.

// WhatsAppProvider is the configured WhatsApp delivery provider.
type WhatsAppProvider string

const (
	ProviderMeta    WhatsAppProvider = "meta"
	ProviderInfobip WhatsAppProvider = "infobip"
)

// ErrUnknownWhatsAppProvider means WHATSAPP_PROVIDER held an unrecognised value.
var ErrUnknownWhatsAppProvider = errors.New("notification: unknown WHATSAPP_PROVIDER (want meta|infobip)")

// ParseWhatsAppProvider normalises the configured provider string. Empty/unset
// defaults to Meta (the existing, fallback-safe provider); an unrecognised value is
// an error so misconfiguration fails loudly rather than silently picking a default.
func ParseWhatsAppProvider(s string) (WhatsAppProvider, error) {
	switch WhatsAppProvider(strings.ToLower(strings.TrimSpace(s))) {
	case "", ProviderMeta:
		return ProviderMeta, nil
	case ProviderInfobip:
		return ProviderInfobip, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownWhatsAppProvider, s)
	}
}

// ErrUnsafeWhatsAppOTP is returned when OTP is routed to WhatsApp for a provider
// that has no registered safe OTP fallback. At the WhatsApp quota cap such an OTP
// would be hard-refused with nowhere to fall through to — an authentication
// lockout. The error wraps the offending provider name (see ValidateOTPFallbackSafety).
var ErrUnsafeWhatsAppOTP = errors.New("WhatsApp OTP is only allowed for providers with a safe OTP fallback")

// providerHasOTPFallback is the ALLOWLIST of WhatsApp providers whose OTP can
// survive a quota-cap refusal because a safe fallback is wired behind them. It is
// fail-CLOSED: only providers explicitly listed here return true; everything else —
// Infobip today, and any future/registered-but-unwired provider — returns false by
// default. A new provider added to the parser enum but NOT added here is therefore
// refused over WhatsApp OTP automatically, until its fallback is wired and listed.
//
// Meta is the only safe provider: main wires it as FallbackChannel(QuotaGuarded(Meta), SMS),
// so an OTP refused at the cap transparently falls through to SMS.
func providerHasOTPFallback(p WhatsAppProvider) bool {
	switch p {
	case ProviderMeta:
		return true
	default:
		return false
	}
}

// ValidateOTPFallbackSafety is the defense-in-depth guard (distinct from
// ParseWhatsAppProvider, which fails closed on unknown STRINGS at boot) that catches
// a parseable/registered provider with no safe OTP fallback. It only matters when
// OTP is routed to WhatsApp: OTP over SMS/Twilio/FAKE never touches the WhatsApp
// quota guard, so it is always allowed. Call at startup and fail closed on error.
func ValidateOTPFallbackSafety(provider WhatsAppProvider, otpRoute ChannelName) error {
	if otpRoute != ChannelWhatsApp {
		return nil // OTP does not go through the WhatsApp quota guard
	}
	if providerHasOTPFallback(provider) {
		return nil
	}
	return fmt.Errorf("%w. Provider %q has no registered OTP fallback and can lock out authentication at quota cap.",
		ErrUnsafeWhatsAppOTP, provider)
}

// NewWhatsAppChannelFor constructs the WhatsApp channel for the selected provider.
// The returned channel is a bare NotificationChannel; the caller wraps it in the
// shared quota/fallback layers. Construction errors (missing credentials) are
// returned so the caller can fail closed when WhatsApp is the active channel.
func NewWhatsAppChannelFor(p WhatsAppProvider, meta config.WhatsAppConfig, infobip config.InfobipConfig) (NotificationChannel, error) {
	switch p {
	case ProviderInfobip:
		ch, err := NewInfobipWhatsAppChannel(infobip)
		if err != nil {
			return nil, err
		}
		return ch, nil
	default: // ProviderMeta
		ch, err := NewWhatsAppChannel(meta)
		if err != nil {
			return nil, err
		}
		return ch, nil
	}
}
