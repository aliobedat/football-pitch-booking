package notification

// GATE 2 scope: the WhatsApp unverified-tier daily quota guard. QuotaGuardedChannel
// decorates the WhatsApp channel so an unverified Meta Business Portfolio stays
// under its ~250/day messaging ceiling. It is a plain NotificationChannel, so it
// composes INSIDE the existing FallbackChannel:
//
//	FallbackChannel{ primary: QuotaGuardedChannel{ wrapped: WhatsApp }, fallback: SMS }
//
// That placement is what makes the cap behaviour "same message, same request, now":
// a refusal returns a DeliveryFailed result, which the outer FallbackChannel already
// treats as a primary failure and transparently re-sends through SMS — no deferral,
// no reschedule.
//
// SCOPE: OTP plus the three booking UTILITY kinds (booking_confirmed /
// booking_cancelled / booking_reminder) are counted and gated. OTP is included
// because WhatsApp AUTHENTICATION templates count against Meta/WABA's daily
// unique-recipient limit just like UTILITY templates — exempting it would let the
// real provider cap be silently exceeded (Gate 2 / PR-1 correction). The guard wraps
// ONLY the WhatsApp channel, so OTP routed over SMS/Twilio is unaffected.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Daily quota thresholds for an unverified WABA (Tier 250).
const (
	quotaWarnThreshold = 200 // sends with count > this (201+) emit a warn-level alert
	quotaHardCap       = 250 // sends with count >= this are refused (→ fallback)
)

// ErrWhatsAppDailyCapReached is the typed refusal returned once the daily cap is
// hit. It surfaces as a DeliveryFailed result so FallbackChannel routes to SMS;
// callers/log sites can match it with errors.Is to distinguish a cap refusal from
// a genuine Meta API failure.
var ErrWhatsAppDailyCapReached = errors.New("notification/whatsapp: daily WABA send cap reached")

// SendQuotaGuard counts one gated WhatsApp send against the WABA's daily bucket and
// returns the resulting count. outbox.QuotaStore is the production implementation;
// tests use a fake. It lives here (not in the contracts file) because it is a
// channel-internal guardrail, not part of the channel-agnostic message contract.
type SendQuotaGuard interface {
	Reserve(ctx context.Context, wabaID string) (count int, err error)
}

// QuotaGuardedChannel wraps a delivery channel (WhatsApp) with the daily cap.
type QuotaGuardedChannel struct {
	wrapped NotificationChannel
	guard   SendQuotaGuard
	wabaID  string
	logger  *slog.Logger
}

var _ NotificationChannel = (*QuotaGuardedChannel)(nil)

// NewQuotaGuardedChannel wraps wrapped so that gated booking notifications are
// counted against wabaID's daily bucket via guard. A nil logger defaults to
// slog.Default().
func NewQuotaGuardedChannel(wrapped NotificationChannel, guard SendQuotaGuard, wabaID string, logger *slog.Logger) *QuotaGuardedChannel {
	if logger == nil {
		logger = slog.Default()
	}
	return &QuotaGuardedChannel{wrapped: wrapped, guard: guard, wabaID: wabaID, logger: logger}
}

// Send counts and gates OTP and booking notifications; any other (future) kind
// passes straight through untouched.
func (q *QuotaGuardedChannel) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	if !isQuotaGated(msg.Kind) {
		return q.wrapped.Send(ctx, msg) // ungated kinds: no count, no block
	}

	count, err := q.guard.Reserve(ctx, q.wabaID)
	if err != nil {
		// Fail OPEN: the counter is a guardrail, not a hard gate. A DB blip must not
		// silence booking notifications — proceed to WhatsApp. If we genuinely are
		// over Meta's real limit, that send fails upstream and FallbackChannel still
		// routes to SMS.
		q.logger.Warn("waba quota reserve failed; sending without quota enforcement",
			"kind", msg.Kind, "waba_id", q.wabaID, "error", err)
		return q.wrapped.Send(ctx, msg)
	}

	if count >= quotaHardCap {
		// Refuse: return DeliveryFailed so the outer FallbackChannel re-sends via SMS
		// in this same request (NOT deferred to the next day).
		q.logger.Warn("waba daily cap reached; refusing WhatsApp, falling back",
			"kind", msg.Kind, "waba_id", q.wabaID, "count", count, "cap", quotaHardCap)
		return failedWhatsApp(fmt.Errorf("%w: count=%d cap=%d", ErrWhatsAppDailyCapReached, count, quotaHardCap))
	}

	if count > quotaWarnThreshold {
		q.logger.Warn("approaching waba daily cap",
			"kind", msg.Kind, "waba_id", q.wabaID, "count", count, "cap", quotaHardCap)
	}

	return q.wrapped.Send(ctx, msg)
}

// isQuotaGated reports whether a message kind is counted/capped by this guard:
// OTP (AUTHENTICATION) plus the three booking UTILITY templates — all of which
// count against Meta/WABA's daily limit. booking_rejected (unsupported by the
// WhatsApp adapters) and any future kind are exempt.
func isQuotaGated(k MessageKind) bool {
	switch k {
	case KindOTP, KindBookingConfirmed, KindBookingCancelled, KindBookingReminder:
		return true
	default:
		return false
	}
}
