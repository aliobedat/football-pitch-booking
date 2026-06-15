package handlers

// PART 5.1 wiring tests: the booking HTTP handlers must route create/cancel
// through the BookingService (so transitions are audited and the player is
// notified) rather than calling the repository directly. These tests drive the
// handlers over a real gin router with a recording fake service — no Postgres
// required — and assert both the HTTP contract and the params handed to the
// service (actor id/role for the audit trail).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// fakeBookingService records calls and returns canned results, standing in for
// *booking.Service so the handler can be exercised without persistence or
// notifications.
type fakeBookingService struct {
	booking *models.Booking

	createErr error
	cancelErr error

	createCalls   int
	cancelCalls   int
	lastCreateReq models.CreateBookingRequest
	lastCancel    repository.CancelBookingParams
}

func (f *fakeBookingService) Create(_ context.Context, req models.CreateBookingRequest) (*models.Booking, error) {
	f.createCalls++
	f.lastCreateReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.booking, nil
}

func (f *fakeBookingService) Cancel(_ context.Context, params repository.CancelBookingParams) (*models.Booking, error) {
	f.cancelCalls++
	f.lastCancel = params
	if f.cancelErr != nil {
		return nil, f.cancelErr
	}
	return f.booking, nil
}

// newBookingRouter mounts the create/cancel routes behind a middleware that
// injects the given authenticated identity, mimicking what RequireAuth would
// set on the context for a real request.
func newBookingRouter(h *BookingHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/bookings", inject, h.CreateBooking)
	r.PATCH("/bookings/:id/cancel", inject, h.CancelBooking)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func sampleHandlerBooking() *models.Booking {
	start := time.Now().UTC().Add(48 * time.Hour)
	pid := int64(3)
	return &models.Booking{
		ID:         42,
		PitchID:    7,
		PlayerID:   &pid,
		StartTime:  start,
		EndTime:    start.Add(time.Hour),
		Status:     models.StatusConfirmed,
		Source:     models.SourcePlayer,
		TotalPrice: 30,
		CreatedAt:  time.Now().UTC(),
	}
}

// validCreateBody returns a request body that passes the handler's time/duration
// validation so the call reaches the service.
func validCreateBody() map[string]any {
	start := time.Now().UTC().Add(48 * time.Hour)
	return map[string]any{
		"pitch_id":    7,
		"start_time":  start.Format(time.RFC3339),
		"end_time":    start.Add(time.Hour).Format(time.RFC3339),
		"total_price": 30,
	}
}

// newBlockRouter mounts the block routes behind the SAME RequireRole guard used
// in production, with an identity injector, so the authz contract (player → 403)
// is exercised end-to-end. RequireRole aborts before the handler, so the nil repo
// is never touched.
func newBlockRouter(h *BookingHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches/:id/blocks", inject, middleware.RequireRole("owner", "admin"), h.CreateBlock)
	r.DELETE("/pitches/:id/blocks/:bookingId", inject, middleware.RequireRole("owner", "admin"), h.CancelBlock)
	return r
}

// A player must not be able to create or remove blocks (owner/admin only).
func TestCreateBlock_PlayerForbidden(t *testing.T) {
	r := newBlockRouter(&BookingHandler{}, 3, "player")
	rec := doJSON(t, r, http.MethodPost, "/pitches/7/blocks", map[string]any{
		"start_time": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
		"end_time":   time.Now().UTC().Add(49 * time.Hour).Format(time.RFC3339),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a player creating a block (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCancelBlock_PlayerForbidden(t *testing.T) {
	r := newBlockRouter(&BookingHandler{}, 3, "player")
	rec := doJSON(t, r, http.MethodDelete, "/pitches/7/blocks/55", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a player removing a block (body: %s)", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CreateBooking
// ─────────────────────────────────────────────────────────────────────────────

func TestCreateBooking_RoutesThroughServiceAndStampsPlayerID(t *testing.T) {
	svc := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	rec := doJSON(t, r, http.MethodPost, "/bookings", validCreateBody())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if svc.createCalls != 1 {
		t.Fatalf("service.Create called %d times, want 1", svc.createCalls)
	}
	// The authenticated user id from the token must be stamped onto the request.
	if svc.lastCreateReq.PlayerID != 3 {
		t.Errorf("create req PlayerID = %d, want 3", svc.lastCreateReq.PlayerID)
	}
	if svc.lastCreateReq.PitchID != 7 {
		t.Errorf("create req PitchID = %d, want 7", svc.lastCreateReq.PitchID)
	}
}

func TestCreateBooking_ValidationFailsBeforeService(t *testing.T) {
	svc := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	// start_time in the past must be rejected without touching the service.
	body := validCreateBody()
	body["start_time"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body["end_time"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

	rec := doJSON(t, r, http.MethodPost, "/bookings", body)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if svc.createCalls != 0 {
		t.Errorf("service.Create called %d times, want 0 on validation failure", svc.createCalls)
	}
}

func TestCreateBooking_ServiceErrorMapsToConflict(t *testing.T) {
	svc := &fakeBookingService{createErr: repository.ErrDoubleBooking}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	rec := doJSON(t, r, http.MethodPost, "/bookings", validCreateBody())

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CancelBooking
// ─────────────────────────────────────────────────────────────────────────────

func TestCancelBooking_RoutesThroughServiceWithActorAndReason(t *testing.T) {
	cancelled := sampleHandlerBooking()
	cancelled.Status = models.StatusCancelled
	svc := &fakeBookingService{booking: cancelled}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 9, "owner")

	rec := doJSON(t, r, http.MethodPatch, "/bookings/42/cancel",
		map[string]any{"reason": "double-booked by phone"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if svc.cancelCalls != 1 {
		t.Fatalf("service.Cancel called %d times, want 1", svc.cancelCalls)
	}
	p := svc.lastCancel
	if p.BookingID != 42 {
		t.Errorf("cancel BookingID = %d, want 42", p.BookingID)
	}
	if p.ActorID == nil || *p.ActorID != 9 {
		t.Errorf("cancel ActorID = %v, want 9", p.ActorID)
	}
	if p.ActorRole != "owner" {
		t.Errorf("cancel ActorRole = %q, want %q", p.ActorRole, "owner")
	}
	if p.Reason != "double-booked by phone" {
		t.Errorf("cancel Reason = %q, want the body reason", p.Reason)
	}
}

func TestCancelBooking_NoBodyLeavesReasonForServiceDefault(t *testing.T) {
	svc := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	// A bare PATCH with no JSON body must still reach the service; the empty
	// reason is intentionally left for the service to default from the role.
	rec := doJSON(t, r, http.MethodPatch, "/bookings/42/cancel", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if svc.cancelCalls != 1 {
		t.Fatalf("service.Cancel called %d times, want 1", svc.cancelCalls)
	}
	if svc.lastCancel.Reason != "" {
		t.Errorf("cancel Reason = %q, want empty (service defaults it)", svc.lastCancel.Reason)
	}
	if svc.lastCancel.ActorRole != "player" {
		t.Errorf("cancel ActorRole = %q, want %q", svc.lastCancel.ActorRole, "player")
	}
}

func TestCancelBooking_InvalidIDRejectedBeforeService(t *testing.T) {
	svc := &fakeBookingService{booking: sampleHandlerBooking()}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	rec := doJSON(t, r, http.MethodPatch, "/bookings/abc/cancel", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if svc.cancelCalls != 0 {
		t.Errorf("service.Cancel called %d times, want 0 for an invalid id", svc.cancelCalls)
	}
}

func TestCancelBooking_InvalidTransitionMapsToConflict(t *testing.T) {
	svc := &fakeBookingService{cancelErr: repository.ErrInvalidStatusTransition}
	h := &BookingHandler{service: svc}
	r := newBookingRouter(h, 3, "player")

	rec := doJSON(t, r, http.MethodPatch, "/bookings/42/cancel", nil)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}
