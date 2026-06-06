package notification

// LogOnlySink is a NotificationChannel that does NOT deliver to the recipient: it
// records (via structured logging) the message that WOULD have gone out. During
// the closed beta, booking-lifecycle notifications route here so they cost zero
// messaging budget while we still observe exactly what would be sent.
//
// It implements the marker LogSink (see service.go) so the routing safety check
// can forbid an OTP route from resolving to it — a silent OTP→log route is a total
// login outage. It is designed so an In-App sink can later REPLACE it behind the
// same NotificationChannel interface without touching the router.

import (
	"context"
	"log/slog"
)

// LogOnlySink records intended sends instead of delivering them.
type LogOnlySink struct {
	logger *slog.Logger
}

var (
	_ NotificationChannel = (*LogOnlySink)(nil)
	_ LogSink             = (*LogOnlySink)(nil)
)

// NewLogOnlySink builds the sink. A nil logger falls back to slog.Default().
func NewLogOnlySink(logger *slog.Logger) *LogOnlySink {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogOnlySink{logger: logger}
}

// logSinkMarker marks this as a non-delivering sink (LogSink).
func (*LogOnlySink) logSinkMarker() {}

// Send logs the full intended message — kind, masked recipient, and the rendered
// body — and reports success. It performs no I/O to any provider, so it is
// trivially idempotent: logging the same message twice has no external effect and
// the outbox marks the job succeeded exactly as for a real delivery.
func (s *LogOnlySink) Send(_ context.Context, msg OutboundMessage) (DeliveryResult, error) {
	s.logger.Info("notification.log_only: intended send (not delivered)",
		slog.String("kind", string(msg.Kind)),
		slog.String("recipient", maskPhone(msg.Recipient)),
		slog.String("body", renderLogBody(msg)),
	)
	// A synthetic, deterministic id so downstream tracking has a non-empty handle.
	return DeliveryResult{Status: DeliverySent, ProviderMessageID: "log_only"}, nil
}

// renderLogBody produces a human-readable, NON-SECRET summary of the message for
// the log. It deliberately does NOT log an OTP code (OTP never routes here, but
// defence in depth): only the message kind's shape is surfaced.
func renderLogBody(msg OutboundMessage) string {
	switch p := msg.Payload.(type) {
	case OTPPayload:
		// Never log the code — even though OTP must not route to a sink.
		return "[otp code redacted]"
	case BookingConfirmedPayload:
		return "booking confirmed: " + p.PitchName
	case BookingReminderPayload:
		return "booking reminder: " + p.PitchName
	case BookingCancelledPayload:
		return "booking cancelled: " + p.PitchName
	case BookingRejectedPayload:
		return "booking rejected: " + p.PitchName
	default:
		return string(msg.Kind)
	}
}
