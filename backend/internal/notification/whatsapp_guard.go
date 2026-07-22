package notification

// WO-SECURITY-V1 PR-S2 scope: the paid-WhatsApp-send kill switch. When paid
// sends are disabled (PAID_WHATSAPP_ENABLED=false), no quota reservation is
// consumed, no WhatsApp provider call is made, and — because the returned
// error is a typed gate refusal — no SMS fallback is triggered either. This is
// the outermost decorator in the WhatsApp chain, checked BEFORE quota
// reservation:
//
//	PaidWhatsAppEnabledGuard -> QuotaGuardedChannel -> WhatsApp provider

import (
	"context"
	"errors"
	"log/slog"
)

// ErrPaidWhatsAppDisabled is the typed gate refusal returned when the paid
// WhatsApp switch is off. It is a GATE REFUSAL, not a provider failure:
// FallbackChannel must never route it to SMS (see isGateRefusal).
var ErrPaidWhatsAppDisabled = errors.New("notification/whatsapp: paid WhatsApp sending is disabled")

// PaidWhatsAppEnabledGuard wraps a WhatsApp-bound channel with a static
// enabled/disabled switch. It never consumes quota or invokes the wrapped
// channel when disabled.
type PaidWhatsAppEnabledGuard struct {
	wrapped NotificationChannel
	enabled bool
	logger  *slog.Logger
}

var _ NotificationChannel = (*PaidWhatsAppEnabledGuard)(nil)

// NewPaidWhatsAppEnabledGuard wraps wrapped with the paid-send switch. A nil
// logger defaults to slog.Default().
func NewPaidWhatsAppEnabledGuard(wrapped NotificationChannel, enabled bool, logger *slog.Logger) *PaidWhatsAppEnabledGuard {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaidWhatsAppEnabledGuard{wrapped: wrapped, enabled: enabled, logger: logger}
}

// Send refuses immediately when disabled — before any quota reservation or
// provider call — and otherwise delegates unchanged.
func (g *PaidWhatsAppEnabledGuard) Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error) {
	if !g.enabled {
		g.logger.Warn("paid WhatsApp sending is disabled; refusing send",
			"kind", msg.Kind)
		return failedWhatsApp(ErrPaidWhatsAppDisabled)
	}
	return g.wrapped.Send(ctx, msg)
}
