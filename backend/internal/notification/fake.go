package notification

// FakeChannel is the default development/test adapter. It performs no real
// delivery: it records every message in an in-memory store, logs it to the
// console, and reports success with a synthetic provider message id. It is the
// channel selected when NOTIFICATION_CHANNEL is unset (default FAKE), letting the
// whole notification path run end-to-end without any provider credentials.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
)

// FakeChannel implements NotificationChannel for development and tests. It is
// safe for concurrent use.
type FakeChannel struct {
	mu     sync.Mutex
	sent   []OutboundMessage
	silent bool
}

// FakeOption configures a FakeChannel.
type FakeOption func(*FakeChannel)

// FakeSilent suppresses console logging. Useful in tests that assert on the
// in-memory store and don't want log noise.
func FakeSilent() FakeOption {
	return func(f *FakeChannel) { f.silent = true }
}

// NewFakeChannel constructs an in-memory FakeChannel.
func NewFakeChannel(opts ...FakeOption) *FakeChannel {
	f := &FakeChannel{}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Send records the message, logs it (unless silent), and returns a successful
// DeliveryResult carrying a randomly generated provider message id. It never
// returns an error — the fake always "succeeds".
func (f *FakeChannel) Send(_ context.Context, msg OutboundMessage) (DeliveryResult, error) {
	id := "fake_" + randomID()

	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()

	if !f.silent {
		log.Printf("[NOTIFY:FAKE] kind=%s recipient=%s provider_id=%s", msg.Kind, maskPhone(msg.Recipient), id)
		// Dev affordance: this adapter only runs locally (NOTIFICATION_CHANNEL
		// unset/FAKE). The OTP code itself is never logged, in any environment —
		// a developer completing local login must read it from the fake
		// channel's in-memory Sent()/Last() store, not the console.
		if _, ok := msg.Payload.(OTPPayload); ok {
			log.Printf("[NOTIFY:FAKE] OTP delivery invoked for %s; code redacted", maskPhone(msg.Recipient))
		}
	}

	return DeliveryResult{
		Status:            DeliverySent,
		ProviderMessageID: id,
	}, nil
}

// Sent returns a copy of every message handed to the channel, in send order.
func (f *FakeChannel) Sent() []OutboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]OutboundMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

// Count reports how many messages the channel has accepted.
func (f *FakeChannel) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// Last returns the most recently sent message and true, or the zero message and
// false when nothing has been sent.
func (f *FakeChannel) Last() (OutboundMessage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return OutboundMessage{}, false
	}
	return f.sent[len(f.sent)-1], true
}

// Reset clears the in-memory store.
func (f *FakeChannel) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}

// randomID returns a 16-hex-character random token used as a synthetic provider
// message id. crypto/rand.Read does not fail in practice on supported platforms.
func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; surface it rather than emit a misleading empty id.
		panic("notification: failed to read random bytes for fake provider id: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
