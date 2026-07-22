package notification

// GATE 2 scope: the WhatsApp unverified-tier daily quota guard. QuotaGuardedChannel
// decorates the WhatsApp channel so an unverified Meta Business Portfolio stays
// under its ~250/day messaging ceiling. It is a plain NotificationChannel, so it
// composes INSIDE the existing FallbackChannel:
//
//	FallbackChannel{ primary: PaidWhatsAppEnabledGuard{ QuotaGuardedChannel{ wrapped: WhatsApp } }, fallback: SMS }
//
// WO-SECURITY-V1 PR-S2 correction: a quota refusal (cap reached OR the quota
// datastore itself unavailable) is a GATE REFUSAL, not a genuine provider
// failure — FallbackChannel recognizes both typed sentinels below and never
// routes them to SMS (see fallback.go's isGateRefusal). Both fail CLOSED: no
// WhatsApp provider call is made once either condition is detected.
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
//
// Reserve returns the POST-increment count, i.e. the ordinal of the CURRENT
// attempt (the 1st reservation of the day returns 1, the 250th returns 250).
// So attempts 1..250 (count <= 250) must be ADMITTED and only attempt 251
// onward (count > 250) refused — the boundary check below is intentionally
// `count > quotaHardCap`, not `count >= quotaHardCap` (WO-SECURITY-V1 PR-S2
// off-by-one fix: the prior `>=` incorrectly refused the 250th, legitimate,
// in-budget attempt).
const (
	quotaWarnThreshold = 200 // sends with count > this (201+) emit a warn-level alert
	quotaHardCap       = 250 // sends with count > this are refused (→ fallback)
)

// ErrWhatsAppDailyCapReached is the typed refusal returned once the daily cap is
// hit. It is a GATE REFUSAL, not a genuine provider failure: FallbackChannel
// must never route it to SMS (WO-SECURITY-V1 PR-S2 — quota exhaustion must not
// trigger fallback). Callers/log sites match it with errors.Is to distinguish a
// cap refusal from a genuine Meta API failure.
var ErrWhatsAppDailyCapReached = errors.New("notification/whatsapp: daily WABA send cap reached")

// ErrWhatsAppQuotaUnavailable is the typed refusal returned when the quota
// datastore itself cannot be reached/queried. A cost-protection datastore
// failure must PREVENT the paid provider call (fail closed), not silently
// admit it — the previous fail-open behavior is the exact defect this sentinel
// closes. It is also a gate refusal: FallbackChannel must never route it to
// SMS, and the booking/outbox layer treats it like any other notification
// refusal (booking success is unaffected).
var ErrWhatsAppQuotaUnavailable = errors.New("notification/whatsapp: quota accounting unavailable")

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
		// Fail CLOSED (WO-SECURITY-V1 PR-S2): a cost-protection datastore failure
		// must prevent the paid provider call, not silently admit it. The prior
		// fail-open behavior let a DB blip bypass quota enforcement entirely — the
		// exact defect this closes. The raw datastore error is logged internally
		// (structured, no phone/OTP/token/secret) but never exposed to the caller;
		// only the typed sentinel crosses that boundary.
		q.logger.Warn("waba quota reserve failed; refusing WhatsApp (fail closed)",
			"kind", msg.Kind, "waba_id", q.wabaID, "error", err)
		return failedWhatsApp(fmt.Errorf("%w", ErrWhatsAppQuotaUnavailable))
	}

	if count > quotaHardCap {
		// Refuse: this is a GATE REFUSAL, not a provider failure (WO-SECURITY-V1
		// PR-S2) — FallbackChannel must not route it to SMS. The outbox/OTP layer
		// still sees a DeliveryFailed result so existing classification (no rapid
		// retry loop) is unchanged. count > cap (not >=): the cap'th attempt
		// itself is still in-budget and must be admitted (see the boundary
		// comment on quotaHardCap above).
		q.logger.Warn("waba daily cap reached; refusing WhatsApp",
			"kind", msg.Kind, "waba_id", q.wabaID, "count", count, "cap", quotaHardCap)
		return failedWhatsApp(fmt.Errorf("%w: count=%d cap=%d", ErrWhatsAppDailyCapReached, count, quotaHardCap))
	}

	if count > quotaWarnThreshold {
		q.logger.Warn("approaching waba daily cap",
			"kind", msg.Kind, "waba_id", q.wabaID, "count", count, "cap", quotaHardCap)
	}

	return q.wrapped.Send(ctx, msg)
}

// WhatsAppQuotaHardCap and WhatsAppQuotaWarnThreshold expose the daily-cap
// constants above read-only, so other packages (e.g. the admin monitoring
// repository) can report the same numbers the guard actually enforces instead
// of duplicating them (WO-MONITORING-V1).
func WhatsAppQuotaHardCap() int       { return quotaHardCap }
func WhatsAppQuotaWarnThreshold() int { return quotaWarnThreshold }

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
