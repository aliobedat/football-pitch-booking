package notification

// PART 4 scope: the fallback decorator. FallbackChannel wraps a primary channel
// (WhatsApp) and a fallback channel (SMS). If the primary fails — an API error,
// an unapproved template, a network problem — delivery transparently retries
// through the fallback. This keeps the "interchangeable adapters" contract intact:
// FallbackChannel is itself just a NotificationChannel, so the Service registers
// it under ChannelWhatsApp and is none the wiser.
//
// WO-SECURITY-V1 PR-S2 correction: a GATE REFUSAL (paid WhatsApp disabled, quota
// exhausted, quota datastore unavailable) is not a genuine provider failure —
// only an eligible provider failure may trigger fallback, per isGateRefusal.
// Separately, fallback as a whole is now opt-in: the default (no
// WithFallbackEnabled option) preserves the existing "any failure falls back"
// behavior for backward compatibility with the channel-mechanism's own tests,
// but production wiring (cmd/api/main.go) passes the config-driven
// WHATSAPP_TO_SMS_FALLBACK_ENABLED value explicitly, which defaults to false.

import (
	"context"
	"errors"
	"log"
)

// FallbackChannel delivers through primary, falling back to fallback when primary
// reports failure. Both are plain NotificationChannels, so any pair composes.
type FallbackChannel struct {
	primary  NotificationChannel
	fallback NotificationChannel
	onFall   func(msg OutboundMessage, primaryErr error)
	// enabled gates whether a non-gate-refusal primary failure may fall back at
	// all. Defaults to true (see NewFallbackChannel) so the channel-mechanism's
	// own tests keep exercising fallback without needing the option; production
	// wiring sets it explicitly from config.
	enabled bool
}

var _ NotificationChannel = (*FallbackChannel)(nil)

// FallbackOption configures a FallbackChannel.
type FallbackOption func(*FallbackChannel)

// WithFallbackHook installs a callback invoked whenever the primary fails and the
// fallback is engaged. Primarily for observability and tests; if unset, a default
// log line is emitted instead.
func WithFallbackHook(fn func(msg OutboundMessage, primaryErr error)) FallbackOption {
	return func(f *FallbackChannel) { f.onFall = fn }
}

// WithFallbackEnabled sets whether an eligible (non-gate-refusal) primary
// failure may fall back to the secondary channel. Production wiring passes the
// resolved WHATSAPP_TO_SMS_FALLBACK_ENABLED config value here explicitly.
func WithFallbackEnabled(enabled bool) FallbackOption {
	return func(f *FallbackChannel) { f.enabled = enabled }
}

// NewFallbackChannel builds a channel that tries primary first and falls back to
// fallback on failure. Fallback is enabled by default (see FallbackChannel.enabled);
// pass WithFallbackEnabled(false) to disable it explicitly.
func NewFallbackChannel(primary, fallback NotificationChannel, opts ...FallbackOption) *FallbackChannel {
	f := &FallbackChannel{primary: primary, fallback: fallback, enabled: true}
	for _, o := range opts {
		o(f)
	}
	return f
}

// isGateRefusal reports whether err is one of the typed gate-refusal sentinels
// this package defines — a refusal to send, not a genuine provider failure.
// Gate refusals never trigger fallback, regardless of the enabled switch.
func isGateRefusal(err error) bool {
	return errors.Is(err, ErrPaidWhatsAppDisabled) ||
		errors.Is(err, ErrWhatsAppDailyCapReached) ||
		errors.Is(err, ErrWhatsAppQuotaUnavailable)
}

// Send attempts delivery through the primary channel. A failure — signalled by a
// non-nil error or a DeliveryFailed status — triggers a transparent retry through
// the fallback channel, whose result is returned, but ONLY when ALL of the
// following hold: fallback is enabled, and the failure is a genuine eligible
// provider failure (not one of the typed gate refusals — those are refusals to
// send, not provider failures, and must never be masked by an automatic SMS
// retry). A successful primary delivery is returned as-is and the fallback is
// never touched.
func (f *FallbackChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	res, err := f.primary.Send(ctx, msg)
	if err == nil && res.Status != DeliveryFailed {
		return res, nil
	}

	primaryErr := err
	if primaryErr == nil {
		primaryErr = res.Err
	}

	if isGateRefusal(primaryErr) {
		// Gate refusal: not a provider failure. Never fall back, regardless of
		// the enabled switch.
		return res, err
	}

	if !f.enabled {
		// Fallback is opt-in and currently off: return the primary's failure
		// as-is, never invoking the fallback channel.
		return res, err
	}

	if f.onFall != nil {
		f.onFall(msg, primaryErr)
	} else {
		log.Printf("[NOTIFY:FALLBACK] primary delivery failed (kind=%s recipient=%s): %v — falling back",
			msg.Kind, maskPhone(msg.Recipient), primaryErr)
	}

	return f.fallback.Send(ctx, msg)
}
