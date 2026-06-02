package handlers

// PART 6 webhook tests: the Cloud API delivery-status callback. They drive the
// handler over a gin router and assert the GET verification handshake plus the
// POST status parsing/persistence, using an in-memory delivery store.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/notification/outbox"
)

// fakeDeliveryStore records the updates the webhook applies. err, when set, makes
// ApplyStatus fail so the "ack despite persist error" behaviour can be checked.
type fakeDeliveryStore struct {
	mu      sync.Mutex
	updates []outbox.DeliveryUpdate
	err     error
}

func (f *fakeDeliveryStore) ApplyStatus(_ context.Context, u outbox.DeliveryUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.updates = append(f.updates, u)
	return nil
}

func (f *fakeDeliveryStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

func newWebhookRouter(store WhatsAppDeliveryStore, verifyToken string) *gin.Engine {
	h := NewWhatsAppWebhookHandler(store, verifyToken)
	r := gin.New()
	r.GET("/webhooks/whatsapp", h.Verify)
	r.POST("/webhooks/whatsapp", h.Receive)
	return r
}

func TestWebhook_Verify(t *testing.T) {
	cases := []struct {
		name        string
		verifyToken string
		query       string
		wantStatus  int
		wantBody    string
	}{
		{"valid handshake echoes challenge", "secret", "hub.mode=subscribe&hub.verify_token=secret&hub.challenge=CHAL123", http.StatusOK, "CHAL123"},
		{"wrong token is forbidden", "secret", "hub.mode=subscribe&hub.verify_token=nope&hub.challenge=CHAL123", http.StatusForbidden, ""},
		{"wrong mode is forbidden", "secret", "hub.mode=unsubscribe&hub.verify_token=secret&hub.challenge=CHAL123", http.StatusForbidden, ""},
		{"empty configured token rejects all", "", "hub.mode=subscribe&hub.verify_token=&hub.challenge=CHAL123", http.StatusForbidden, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newWebhookRouter(&fakeDeliveryStore{}, c.verifyToken)
			req := httptest.NewRequest(http.MethodGet, "/webhooks/whatsapp?"+c.query, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			if c.wantStatus == http.StatusOK && rec.Body.String() != c.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), c.wantBody)
			}
		})
	}
}

func TestWebhook_Receive_PersistsStatuses(t *testing.T) {
	store := &fakeDeliveryStore{}
	r := newWebhookRouter(store, "secret")

	// One callback batch carrying three statuses, one of them a failure with an
	// error detail. An empty-id status and an unknown status string are included
	// to prove they are skipped without failing the batch.
	body := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"id": "WABA_ID",
			"changes": []map[string]any{{
				"field": "messages",
				"value": map[string]any{
					"messaging_product": "whatsapp",
					"statuses": []map[string]any{
						{"id": "wamid.AAA", "status": "delivered", "recipient_id": "962790000000"},
						{"id": "wamid.BBB", "status": "failed", "recipient_id": "962790000001",
							"errors": []map[string]any{{"code": 131026, "title": "Message undeliverable"}}},
						{"id": "", "status": "read"},           // empty id → skipped
						{"id": "wamid.CCC", "status": "bogus"}, // unknown status → skipped
					},
				},
			}},
		}},
	}

	rec := postJSON(t, r, "/webhooks/whatsapp", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if store.count() != 2 {
		t.Fatalf("persisted %d statuses, want 2 (skipping empty-id and unknown)", store.count())
	}

	// Verify the parsed updates carry the right fields.
	byID := map[string]outbox.DeliveryUpdate{}
	for _, u := range store.updates {
		byID[u.ProviderMessageID] = u
	}
	if got := byID["wamid.AAA"]; got.Status != notification.DeliveryDelivered || got.Recipient != "962790000000" {
		t.Errorf("AAA update = %+v, want delivered to 962790000000", got)
	}
	failed := byID["wamid.BBB"]
	if failed.Status != notification.DeliveryFailed {
		t.Errorf("BBB status = %q, want failed", failed.Status)
	}
	if failed.ErrorCode != 131026 || failed.ErrorTitle != "Message undeliverable" {
		t.Errorf("BBB error = (%d, %q), want (131026, Message undeliverable)", failed.ErrorCode, failed.ErrorTitle)
	}
}

func TestWebhook_Receive_MalformedBodyIs400(t *testing.T) {
	r := newWebhookRouter(&fakeDeliveryStore{}, "secret")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", bytes.NewBufferString("}{ not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebhook_Receive_AcksDespitePersistError(t *testing.T) {
	// A per-row DB blip must NOT yield a non-2xx, or Meta redelivers the whole
	// batch. We still answer 200 and log the failure.
	store := &fakeDeliveryStore{err: errAlwaysFail}
	r := newWebhookRouter(store, "secret")

	body := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"changes": []map[string]any{{
				"value": map[string]any{
					"statuses": []map[string]any{
						{"id": "wamid.AAA", "status": "delivered", "recipient_id": "962790000000"},
					},
				},
			}},
		}},
	}
	rec := postJSON(t, r, "/webhooks/whatsapp", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when persistence fails", rec.Code)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

var errAlwaysFail = errPersist("boom")

type errPersist string

func (e errPersist) Error() string { return string(e) }

func postJSON(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
