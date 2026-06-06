package handlers

// No-DB handler tests for the Idempotency-Key plumbing on POST /bookings: the
// header is threaded into the request's Idempotency params with a fingerprint
// derived from the booking's semantic content, and its absence leaves the legacy
// (non-idempotent) path untouched.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/models"
)

func postBooking(t *testing.T, h *BookingHandler, body any, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	r := newBookingRouter(h, 3, "player")
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/bookings", &buf)
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func futureBookingBody() map[string]any {
	start := time.Now().UTC().Add(48 * time.Hour)
	return map[string]any{
		"pitch_id":    7,
		"start_time":  start.Format(time.RFC3339),
		"end_time":    start.Add(time.Hour).Format(time.RFC3339),
		"total_price": 30,
	}
}

func TestCreateBooking_ThreadsIdempotencyKey(t *testing.T) {
	fake := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: fake}

	rec := postBooking(t, h, futureBookingBody(), "idem-abc-123")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	idem := fake.lastCreateReq.Idempotency
	if idem == nil {
		t.Fatal("Idempotency params not attached when header present")
	}
	if idem.Key != "idem-abc-123" {
		t.Errorf("key = %q, want %q", idem.Key, "idem-abc-123")
	}
	if idem.Fingerprint == "" {
		t.Error("fingerprint is empty")
	}
}

func TestCreateBooking_NoHeader_NoIdempotency(t *testing.T) {
	fake := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: fake}

	rec := postBooking(t, h, futureBookingBody(), "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreateReq.Idempotency != nil {
		t.Error("Idempotency params attached even though no header was sent")
	}
}

// Fingerprint reflects the request's semantic content: a different time range
// yields a different fingerprint (so a reused key with a changed booking is
// caught downstream), while total_price is excluded (server recomputes it).
func TestBookingFingerprint_SemanticIdentity(t *testing.T) {
	start := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	base := models.CreateBookingRequest{PitchID: 7, StartTime: start, EndTime: start.Add(time.Hour), TotalPrice: 30}

	samePriceDiff := base
	samePriceDiff.TotalPrice = 999 // price must NOT change the fingerprint
	if bookingFingerprint(base) != bookingFingerprint(samePriceDiff) {
		t.Error("fingerprint changed with total_price; it should be excluded")
	}

	diffTime := base
	diffTime.EndTime = start.Add(2 * time.Hour)
	if bookingFingerprint(base) == bookingFingerprint(diffTime) {
		t.Error("fingerprint unchanged for a different time range; it should differ")
	}
}
