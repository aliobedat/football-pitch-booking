package outbox

import (
	"context"

	"github.com/ali/football-pitch-api/internal/notification"
)

// DeliveryUpdate is a single provider delivery-status report, as parsed from a
// Cloud API status webhook. Raw carries the original status object for audit.
type DeliveryUpdate struct {
	ProviderMessageID string
	Status            notification.DeliveryStatus // sent | delivered | read | failed
	Recipient         string
	ErrorCode         int
	ErrorTitle        string
	Raw               []byte
}

// DeliveryStore persists per-provider-message delivery status into the
// message_deliveries table. It is written from two places: the worker records a
// 'sent' row when the provider accepts a message, and the status webhook
// advances/finalises that row. Both paths UPSERT on provider_message_id so the
// two can arrive in any order.
type DeliveryStore interface {
	// RecordSent registers (or refreshes) a message as accepted by the provider,
	// optionally linking it to the queue job that produced it.
	RecordSent(ctx context.Context, providerMessageID string, jobID *int64, recipient string) error

	// ApplyStatus records a delivery-status update from a webhook callback.
	ApplyStatus(ctx context.Context, u DeliveryUpdate) error
}
