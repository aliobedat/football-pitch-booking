package notification

// PART 4 scope: the fallback decorator. FallbackChannel wraps a primary channel
// (WhatsApp) and a fallback channel (SMS). If the primary fails — an API error,
// an unapproved template, a network problem — delivery transparently retries
// through the fallback. This keeps the "interchangeable adapters" contract intact:
// FallbackChannel is itself just a NotificationChannel, so the Service registers
// it under ChannelWhatsApp and is none the wiser.

import (
	"context"
	"log"
)

// FallbackChannel delivers through primary, falling back to fallback when primary
// reports failure. Both are plain NotificationChannels, so any pair composes.
type FallbackChannel struct {
	primary  NotificationChannel
	fallback NotificationChannel
	onFall   func(msg OutboundMessage, primaryErr error)
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

// NewFallbackChannel builds a channel that tries primary first and falls back to
// fallback on failure.
func NewFallbackChannel(primary, fallback NotificationChannel, opts ...FallbackOption) *FallbackChannel {
	f := &FallbackChannel{primary: primary, fallback: fallback}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Send attempts delivery through the primary channel. A failure — signalled by a
// non-nil error or a DeliveryFailed status — triggers a transparent retry through
// the fallback channel, whose result is returned. A successful primary delivery
// is returned as-is and the fallback is never touched.
func (f *FallbackChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	res, err := f.primary.Send(ctx, msg)
	if err == nil && res.Status != DeliveryFailed {
		return res, nil
	}

	primaryErr := err
	if primaryErr == nil {
		primaryErr = res.Err
	}

	if f.onFall != nil {
		f.onFall(msg, primaryErr)
	} else {
		log.Printf("[NOTIFY:FALLBACK] primary delivery failed (kind=%s recipient=%s): %v — falling back",
			msg.Kind, msg.Recipient, primaryErr)
	}

	return f.fallback.Send(ctx, msg)
}
