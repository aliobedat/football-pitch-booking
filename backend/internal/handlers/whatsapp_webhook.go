package handlers

// PART 6: the Meta WhatsApp Cloud API delivery-status webhook. Meta calls this
// endpoint to report the lifecycle of a previously sent message (sent →
// delivered → read, or failed). We parse the callback and persist each status
// against its provider message id so the rest of the system has an accurate,
// queryable view of delivery outcomes.
//
// This file only PARSES inbound webhook JSON; it makes no outbound Meta SDK call,
// so it does not breach the "Meta SDK only inside the adapter" rule (that rule
// governs OUTBOUND message construction, which still lives solely in
// notification/whatsapp.go). The inbound wire types below are local to this
// handler.

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/notification/outbox"
)

// WhatsAppDeliveryStore is the narrow persistence seam the webhook needs: record
// a parsed delivery-status update. outbox.PostgresStore satisfies it; tests use
// a recording fake.
type WhatsAppDeliveryStore interface {
	ApplyStatus(ctx context.Context, u outbox.DeliveryUpdate) error
}

// WhatsAppWebhookHandler serves the Cloud API status webhook (GET verification +
// POST status callbacks).
type WhatsAppWebhookHandler struct {
	store       WhatsAppDeliveryStore
	verifyToken string
}

// NewWhatsAppWebhookHandler wires the handler with its delivery store and the
// verification token Meta echoes during webhook setup (empty disables the GET
// verification handshake).
func NewWhatsAppWebhookHandler(store WhatsAppDeliveryStore, verifyToken string) *WhatsAppWebhookHandler {
	return &WhatsAppWebhookHandler{store: store, verifyToken: verifyToken}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/webhooks/whatsapp  — Meta subscription verification handshake
// ─────────────────────────────────────────────────────────────────────────────

// Verify answers Meta's one-time subscription challenge: when hub.mode is
// "subscribe" and hub.verify_token matches our configured token, we echo
// hub.challenge verbatim as plain text. Any mismatch is a 403.
func (h *WhatsAppWebhookHandler) Verify(c *gin.Context) {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")

	if mode == "subscribe" && h.verifyToken != "" && token == h.verifyToken {
		c.String(http.StatusOK, challenge)
		return
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error": "verification_failed", "message": "hub.verify_token did not match",
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/webhooks/whatsapp  — delivery-status callbacks
// ─────────────────────────────────────────────────────────────────────────────

// Receive parses a status callback and persists every status it carries. It
// always answers 200 once the body is structurally valid — Meta retries on any
// non-2xx, and a per-status persistence hiccup should not trigger a redelivery
// of the whole batch (those are logged for follow-up). A malformed body is a
// 400 so genuinely bad requests are visible.
func (h *WhatsAppWebhookHandler) Receive(c *gin.Context) {
	var payload waWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_payload", "message": err.Error(),
		})
		return
	}

	ctx := c.Request.Context()
	var processed, failedStatuses int

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, st := range change.Value.Statuses {
				update, ok := st.toUpdate()
				if !ok {
					continue // unrecognised status string — skip, do not fail the batch
				}
				if update.Status == notification.DeliveryFailed {
					failedStatuses++
				}
				if err := h.store.ApplyStatus(ctx, update); err != nil {
					// Persist failure: log and continue. We still 200 so Meta does
					// not redeliver the entire batch for one row's DB blip.
					c.Error(err)
					log.Printf("[WEBHOOK:WHATSAPP] persist status failed (msg=%s status=%s): %v",
						update.ProviderMessageID, update.Status, err)
					continue
				}
				processed++
			}
		}
	}

	// Structured signal for the alerting layer: a callback batch carrying
	// failed deliveries is worth surfacing even though we ack it.
	if failedStatuses > 0 {
		log.Printf("[ALERT] whatsapp webhook reported %d failed delivery status(es) in one callback", failedStatuses)
	}

	c.JSON(http.StatusOK, gin.H{"processed": processed})
}

// ── Inbound Cloud API webhook wire types (local to this handler) ─────────────

type waWebhookPayload struct {
	Object string             `json:"object"`
	Entry  []waWebhookEntry   `json:"entry"`
}

type waWebhookEntry struct {
	ID      string            `json:"id"`
	Changes []waWebhookChange `json:"changes"`
}

type waWebhookChange struct {
	Field string         `json:"field"`
	Value waWebhookValue `json:"value"`
}

type waWebhookValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Statuses         []waWebhookStatus `json:"statuses"`
}

type waWebhookStatus struct {
	ID          string `json:"id"`           // provider message id (wamid.*)
	Status      string `json:"status"`       // sent | delivered | read | failed
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"` // E.164 without '+'
	Errors      []struct {
		Code  int    `json:"code"`
		Title string `json:"title"`
	} `json:"errors"`
}

// toUpdate maps a parsed status object onto a DeliveryUpdate. It returns false
// for an empty id or an unrecognised status string so the caller can skip it.
func (s waWebhookStatus) toUpdate() (outbox.DeliveryUpdate, bool) {
	if s.ID == "" {
		return outbox.DeliveryUpdate{}, false
	}
	status, ok := mapWebhookStatus(s.Status)
	if !ok {
		return outbox.DeliveryUpdate{}, false
	}

	u := outbox.DeliveryUpdate{
		ProviderMessageID: s.ID,
		Status:            status,
		Recipient:         s.RecipientID,
	}
	if len(s.Errors) > 0 {
		u.ErrorCode = s.Errors[0].Code
		u.ErrorTitle = s.Errors[0].Title
	}
	return u, true
}

// mapWebhookStatus translates a Cloud API status string into our delivery
// vocabulary, rejecting anything unknown.
func mapWebhookStatus(s string) (notification.DeliveryStatus, bool) {
	switch s {
	case "sent":
		return notification.DeliverySent, true
	case "delivered":
		return notification.DeliveryDelivered, true
	case "read":
		return notification.DeliveryRead, true
	case "failed":
		return notification.DeliveryFailed, true
	default:
		return "", false
	}
}
