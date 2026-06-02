package notification

// PART 4 scope: the SMS fallback adapter. The WhatsApp Business Platform may
// withhold AUTHENTICATION (OTP) templates from unverified businesses, so the
// architecture mandates an SMS fallback that is always available. Until a real
// SMS provider (e.g. Twilio) is wired in, this is a logger-backed stub: it
// simulates a successful send, records the message in memory for assertions, and
// returns a DeliveryResult with a synthetic provider id. No real network call is
// made, so it cannot fail — exactly what a fallback of last resort needs to be.

import (
	"context"
	"log"
	"sync"
)

// SmsChannel implements NotificationChannel as a simulated SMS sender. It is the
// fallback target for the WhatsApp adapter. Safe for concurrent use.
type SmsChannel struct {
	mu     sync.Mutex
	sent   []OutboundMessage
	silent bool
}

var _ NotificationChannel = (*SmsChannel)(nil)

// SmsOption configures an SmsChannel.
type SmsOption func(*SmsChannel)

// SmsSilent suppresses console logging. Useful in tests asserting on the store.
func SmsSilent() SmsOption {
	return func(s *SmsChannel) { s.silent = true }
}

// NewSmsChannel constructs the simulated SMS channel.
func NewSmsChannel(opts ...SmsOption) *SmsChannel {
	s := &SmsChannel{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Send simulates dispatching an SMS: it records the message, logs it (unless
// silent), and reports success with a synthetic provider id. It never errors —
// as the channel of last resort it must always "succeed".
func (s *SmsChannel) Send(_ context.Context, msg OutboundMessage) (DeliveryResult, error) {
	id := "sms_" + randomID()

	s.mu.Lock()
	s.sent = append(s.sent, msg)
	s.mu.Unlock()

	if !s.silent {
		log.Printf("[NOTIFY:SMS] (simulated) kind=%s recipient=%s provider_id=%s", msg.Kind, msg.Recipient, id)
	}

	return DeliveryResult{Status: DeliverySent, ProviderMessageID: id}, nil
}

// Sent returns a copy of every message handed to the channel, in send order.
func (s *SmsChannel) Sent() []OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OutboundMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

// Count reports how many messages the channel has accepted.
func (s *SmsChannel) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// Last returns the most recently sent message and true, or the zero message and
// false when nothing has been sent.
func (s *SmsChannel) Last() (OutboundMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return OutboundMessage{}, false
	}
	return s.sent[len(s.sent)-1], true
}

// Reset clears the in-memory store.
func (s *SmsChannel) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = nil
}
