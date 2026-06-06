package notification

// PART 2 scope: the routing core. NotificationService is the single entry point
// every outbound message flows through. It enforces the opt-in gate for
// AUTHENTICATION-category (OTP) messages, selects the active channel from config,
// and delegates delivery to the chosen adapter. It contains NO provider code —
// concrete SMS/WhatsApp adapters arrive in PART 4 and plug in as channels here.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// ChannelName identifies a registered delivery adapter or sink. Adapters and
// sinks are registered by name and resolved per message kind by the routing
// policy at send time.
type ChannelName string

const (
	ChannelFake     ChannelName = "FAKE"
	ChannelSMS      ChannelName = "SMS"
	ChannelWhatsApp ChannelName = "WHATSAPP"
	// ChannelTwilioSMS is the Twilio Programmable SMS adapter (closed-beta OTP).
	ChannelTwilioSMS ChannelName = "twilio_sms"
	// ChannelLogOnly is the non-delivering LogOnlySink (closed-beta booking events).
	ChannelLogOnly ChannelName = "log_only"
)

// LogSink marks a NotificationChannel that does NOT deliver to the recipient (it
// only logs/records). The routing safety check uses this marker to FORBID an OTP
// route from resolving to such a sink — a silent OTP→log route is a login outage.
type LogSink interface {
	logSinkMarker()
}

// EnvChannel is the environment variable that selects the active channel.
const EnvChannel = "NOTIFICATION_CHANNEL"

// Errors surfaced by the service. Callers can match these with errors.Is.
var (
	// ErrOptedOut means the recipient has explicitly WITHDRAWN consent. Unlike
	// the opt-in gate (which only guards AUTHENTICATION/OTP messages), opt-out
	// blocks EVERY message kind — booking events included. It is a permanent,
	// non-retryable refusal: the outbox worker dead-letters rather than retries
	// a job that fails with it.
	ErrOptedOut = errors.New("notification: recipient has opted out of all messages")
	// ErrOptInRequired means the recipient has not granted opt-in consent and
	// an AUTHENTICATION-category (OTP) message was refused.
	ErrOptInRequired = errors.New("notification: recipient has not opted in for authentication messages")
	// ErrNoOptInChecker means an OTP message was requested but no OptInChecker
	// was configured. We refuse rather than send without verifying consent.
	ErrNoOptInChecker = errors.New("notification: opt-in checker is not configured")
	// ErrUnknownChannel means the active channel is not registered on the service.
	ErrUnknownChannel = errors.New("notification: active channel is not registered")
	// ErrInvalidMessage means the OutboundMessage failed structural validation.
	ErrInvalidMessage = errors.New("notification: invalid outbound message")
	// ErrInvalidChannel means NOTIFICATION_CHANNEL held an unrecognised value.
	ErrInvalidChannel = errors.New("notification: unrecognised channel name")
	// ErrRoutingUnsafe means the routing policy is unsafe — specifically, OTP does
	// not resolve to a registered, real (non-LogSink) delivery adapter. Boot must
	// fail rather than silently drop logins.
	ErrRoutingUnsafe = errors.New("notification: unsafe routing policy")
)

// OptInChecker reports whether a recipient has granted explicit opt-in consent
// to receive AUTHENTICATION-category (OTP) messages. Per Meta's WhatsApp rules
// and our architecture, opt-in is mandatory before any OTP dispatch. The lookup
// (DB-backed in later PARTs) lives behind this seam so the service stays
// storage-agnostic.
type OptInChecker interface {
	HasOptedIn(ctx context.Context, recipient string) (bool, error)
}

// OptInFunc adapts a plain function to the OptInChecker interface.
type OptInFunc func(ctx context.Context, recipient string) (bool, error)

// HasOptedIn calls the underlying function.
func (f OptInFunc) HasOptedIn(ctx context.Context, recipient string) (bool, error) {
	return f(ctx, recipient)
}

// OptOutChecker reports whether a recipient has explicitly withdrawn consent to
// receive ANY message. It is the enforcement seam behind the opt-out endpoint
// (PART 6): when it reports true the service refuses delivery of every message
// kind. Storage (the users.opt_out column) lives behind this seam so the
// service stays storage-agnostic, mirroring OptInChecker.
type OptOutChecker interface {
	HasOptedOut(ctx context.Context, recipient string) (bool, error)
}

// OptOutFunc adapts a plain function to the OptOutChecker interface.
type OptOutFunc func(ctx context.Context, recipient string) (bool, error)

// HasOptedOut calls the underlying function.
func (f OptOutFunc) HasOptedOut(ctx context.Context, recipient string) (bool, error) {
	return f(ctx, recipient)
}

// Service routes an OutboundMessage to the active NotificationChannel after
// enforcing the opt-in gate. It is the concrete implementation of the
// NotificationChannel contract that the rest of the app depends on.
type Service struct {
	active   ChannelName
	channels map[ChannelName]NotificationChannel
	optIn    OptInChecker
	optOut   OptOutChecker

	// routes maps a message kind to the registered channel/sink that delivers it
	// (config-driven, resolved at SEND time so a policy change needs no re-enqueue).
	// When nil, the service falls back to `active` for every kind (legacy/tests).
	routes       map[MessageKind]ChannelName
	routeDefault ChannelName  // fail-safe target for unmapped kinds
	logger       *slog.Logger // optional; logs every routing decision + result
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithChannel registers a delivery adapter under the given name. Registering the
// adapter that matches the active name is what makes the service usable.
func WithChannel(name ChannelName, ch NotificationChannel) Option {
	return func(s *Service) { s.channels[name] = ch }
}

// WithOptInChecker installs the consent lookup used to gate OTP messages.
func WithOptInChecker(c OptInChecker) Option {
	return func(s *Service) { s.optIn = c }
}

// WithOptOutChecker installs the consent-withdrawal lookup used to block ALL
// messages to a recipient who has opted out. When unset, the opt-out gate is
// inactive (no withdrawal can be observed) and only the opt-in gate applies.
func WithOptOutChecker(c OptOutChecker) Option {
	return func(s *Service) { s.optOut = c }
}

// WithRoutingPolicy installs the type→channel routing map and the fail-safe
// default target for any kind not in the map. Once set, Send resolves the target
// per message kind instead of using the single active channel. The default MUST
// be registered too; keep it a non-delivering sink (log_only) so an unknown kind
// can never accidentally burn messaging budget.
func WithRoutingPolicy(routes map[MessageKind]ChannelName, defaultTarget ChannelName) Option {
	return func(s *Service) {
		s.routes = routes
		s.routeDefault = defaultTarget
	}
}

// WithServiceLogger installs a structured logger for routing decisions and send
// results. Optional; when unset the service logs nothing.
func WithServiceLogger(l *slog.Logger) Option {
	return func(s *Service) { s.logger = l }
}

// NewService builds a Service that delivers through the channel registered under
// active. Channels and the opt-in checker are supplied via options so the same
// constructor serves production wiring and tests.
func NewService(active ChannelName, opts ...Option) *Service {
	s := &Service{
		active:   active,
		channels: make(map[ChannelName]NotificationChannel),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Send enforces the opt-in gate, selects the active channel, and delegates
// delivery. The returned error mirrors DeliveryResult.Err on failure so callers
// may use either; on success Err is nil and Status reflects the channel outcome.
//
// Send satisfies the NotificationChannel interface, so a Service can itself be
// passed anywhere a channel is expected (e.g. for layering/decoration later).
func (s *Service) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	if err := validate(msg); err != nil {
		return failed(err)
	}

	// Opt-out gate: a recipient who has withdrawn consent receives NOTHING,
	// regardless of message kind. This is checked before the opt-in gate so an
	// explicit withdrawal always wins. A missing checker leaves the gate open.
	if s.optOut != nil {
		out, err := s.optOut.HasOptedOut(ctx, msg.Recipient)
		if err != nil {
			return failed(fmt.Errorf("notification: opt-out lookup failed: %w", err))
		}
		if out {
			return failed(ErrOptedOut)
		}
	}

	// Opt-in gate: AUTHENTICATION-category (OTP) messages require explicit
	// consent. UTILITY-category booking events do not.
	if requiresOptIn(msg.Kind) {
		if s.optIn == nil {
			return failed(ErrNoOptInChecker)
		}
		ok, err := s.optIn.HasOptedIn(ctx, msg.Recipient)
		if err != nil {
			return failed(fmt.Errorf("notification: opt-in lookup failed: %w", err))
		}
		if !ok {
			return failed(ErrOptInRequired)
		}
	}

	// Resolve the delivery target for this message KIND via the routing policy
	// (config-driven, evaluated here at send time). Unmapped kinds fall through to
	// the fail-safe default (a non-delivering sink), so a new kind never silently
	// burns budget.
	target := s.ResolveRoute(msg.Kind)
	ch, ok := s.channels[target]
	if !ok {
		err := fmt.Errorf("%w: %q", ErrUnknownChannel, target)
		s.logRoute(msg, target, DeliveryResult{Status: DeliveryFailed, Err: err}, err)
		return failed(err)
	}

	res, sendErr := ch.Send(ctx, msg)
	s.logRoute(msg, target, res, sendErr)
	return res, sendErr
}

// ResolveRoute returns the channel/sink name a message kind routes to. With a
// routing policy installed, it is policy[kind] or the fail-safe default for an
// unmapped kind; without one it is the single active channel (legacy/tests).
func (s *Service) ResolveRoute(kind MessageKind) ChannelName {
	if s.routes == nil {
		return s.active
	}
	if name, ok := s.routes[kind]; ok {
		return name
	}
	return s.routeDefault
}

// ValidateRouting enforces the OTP safety invariant: OTP must resolve to a
// REGISTERED, real delivery adapter — never an unregistered name and never a
// non-delivering LogSink. A violation returns ErrRoutingUnsafe so the caller can
// FAIL BOOT (a silent OTP→log route is a total login outage). It also verifies
// the fail-safe default target is registered. Call once at startup.
func (s *Service) ValidateRouting() error {
	otpTarget := s.ResolveRoute(KindOTP)
	ch, ok := s.channels[otpTarget]
	if !ok {
		return fmt.Errorf("%w: OTP routes to %q which is not a registered delivery adapter",
			ErrRoutingUnsafe, otpTarget)
	}
	if _, isSink := ch.(LogSink); isSink {
		return fmt.Errorf("%w: OTP routes to %q which is a non-delivering log sink (login outage)",
			ErrRoutingUnsafe, otpTarget)
	}
	if s.routes != nil {
		if _, ok := s.channels[s.routeDefault]; !ok {
			return fmt.Errorf("%w: default route %q is not registered", ErrRoutingUnsafe, s.routeDefault)
		}
	}
	return nil
}

// logRoute records the routing decision and send result with the recipient
// masked. No-op when no logger is configured.
func (s *Service) logRoute(msg OutboundMessage, target ChannelName, res DeliveryResult, sendErr error) {
	if s.logger == nil {
		return
	}
	attrs := []any{
		slog.String("kind", string(msg.Kind)),
		slog.String("sink", string(target)),
		slog.String("recipient", maskPhone(msg.Recipient)),
		slog.String("status", string(res.Status)),
	}
	if res.ProviderMessageID != "" {
		attrs = append(attrs, slog.String("provider_id", res.ProviderMessageID))
	}
	if sendErr != nil {
		attrs = append(attrs, slog.String("error", sendErr.Error()))
		s.logger.Error("notification.route: send failed", attrs...)
		return
	}
	s.logger.Info("notification.route: dispatched", attrs...)
}

// maskPhone redacts the middle of an E.164 number for logs, keeping the country
// code prefix and the last two digits (e.g. +962790001234 → +9627****34). Short
// or empty values are fully masked.
func maskPhone(p string) string {
	if len(p) <= 6 {
		return "****"
	}
	return p[:5] + "****" + p[len(p)-2:]
}

// requiresOptIn reports whether a message kind is an AUTHENTICATION-category
// message subject to the mandatory opt-in gate. Only OTP qualifies today.
func requiresOptIn(kind MessageKind) bool {
	return kind == KindOTP
}

// validate checks the structural invariants of an OutboundMessage: a recipient
// is present, a payload is attached, and the payload's self-reported kind agrees
// with the message kind (guarding against mis-paired payloads).
func validate(msg OutboundMessage) error {
	if msg.Recipient == "" {
		return fmt.Errorf("%w: recipient is empty", ErrInvalidMessage)
	}
	if msg.Payload == nil {
		return fmt.Errorf("%w: payload is nil", ErrInvalidMessage)
	}
	if msg.Payload.Kind() != msg.Kind {
		return fmt.Errorf("%w: kind %q does not match payload kind %q",
			ErrInvalidMessage, msg.Kind, msg.Payload.Kind())
	}
	return nil
}

// failed builds the failure pair returned by Send, keeping DeliveryResult.Err
// and the returned error in sync.
func failed(err error) (DeliveryResult, error) {
	return DeliveryResult{Status: DeliveryFailed, Err: err}, err
}

// ActiveChannelFromEnv reads NOTIFICATION_CHANNEL and returns the configured
// channel name, defaulting to FAKE when unset/empty. An unrecognised value is an
// error so misconfiguration fails loudly rather than silently falling back.
func ActiveChannelFromEnv() (ChannelName, error) {
	raw := os.Getenv(EnvChannel)
	if raw == "" {
		return ChannelFake, nil
	}
	switch ChannelName(raw) {
	case ChannelFake, ChannelSMS, ChannelWhatsApp:
		return ChannelName(raw), nil
	default:
		return "", fmt.Errorf("%w: %q (want FAKE | SMS | WHATSAPP)", ErrInvalidChannel, raw)
	}
}
