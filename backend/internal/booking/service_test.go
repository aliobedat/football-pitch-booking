package booking

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/repository"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test doubles
// ─────────────────────────────────────────────────────────────────────────────

// fakeStore is an in-memory Store. Each operation returns a canned booking (or
// a canned error) and records the params it was called with so tests can assert
// the audit actor/reason that would be persisted.
type fakeStore struct {
	booking *models.Booking
	contact *repository.BookingContact

	createErr  error
	cancelErr  error
	contactErr error

	// idempotentReplayed is what CreateBookingIdempotent reports as its replayed
	// flag (and idempotentErr its error), so tests can drive the replay-vs-fresh
	// notification behaviour.
	idempotentReplayed bool
	idempotentErr      error

	createCalls           int
	idempotentCreateCalls int
	cancelCalls           int
	contactCalls          int
	lastCreateReq         models.CreateBookingRequest
	lastIdem              models.IdempotencyParams
	lastCancelParams      repository.CancelBookingParams
}

func (f *fakeStore) CreateBooking(_ context.Context, req models.CreateBookingRequest) (*models.Booking, error) {
	f.createCalls++
	f.lastCreateReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.booking, nil
}

func (f *fakeStore) CreateBookingIdempotent(_ context.Context, req models.CreateBookingRequest, idem models.IdempotencyParams) (*models.Booking, bool, error) {
	f.idempotentCreateCalls++
	f.lastCreateReq = req
	f.lastIdem = idem
	if f.idempotentErr != nil {
		return nil, false, f.idempotentErr
	}
	return f.booking, f.idempotentReplayed, nil
}

func (f *fakeStore) CancelBooking(_ context.Context, p repository.CancelBookingParams) (*models.Booking, error) {
	f.cancelCalls++
	f.lastCancelParams = p
	if f.cancelErr != nil {
		return nil, f.cancelErr
	}
	return f.booking, nil
}

func (f *fakeStore) GetBookingContact(_ context.Context, _ int64) (*repository.BookingContact, error) {
	f.contactCalls++
	if f.contactErr != nil {
		return nil, f.contactErr
	}
	return f.contact, nil
}

// fakeNotifier records every dispatched message and can be made to fail.
type fakeNotifier struct {
	sent []notification.OutboundMessage
	err  error
}

func (f *fakeNotifier) Send(_ context.Context, msg notification.OutboundMessage) (notification.DeliveryResult, error) {
	f.sent = append(f.sent, msg)
	if f.err != nil {
		return notification.DeliveryResult{Status: notification.DeliveryFailed, Err: f.err}, f.err
	}
	return notification.DeliveryResult{Status: notification.DeliverySent, ProviderMessageID: "test_id"}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────────────

const (
	testPhone     = "+962790000000"
	testPitchName = "Pitch A"
)

func sampleBooking() *models.Booking {
	start := time.Date(2026, 6, 10, 18, 0, 0, 0, time.UTC)
	return &models.Booking{
		ID:         42,
		PitchID:    7,
		PlayerID:     3,
		StartTime:  start,
		EndTime:    start.Add(time.Hour),
		Status:     models.StatusConfirmed,
		TotalPrice: 30,
		CreatedAt:  start.Add(-24 * time.Hour),
	}
}

// newService wires a Service over the given store/notifier with a silenced
// logger so best-effort diagnostics don't pollute test output.
func newService(store Store, notifier Notifier) *Service {
	return NewService(store, notifier, WithLogger(log.New(io.Discard, "", 0)))
}

func int64Ptr(v int64) *int64 { return &v }

// ─────────────────────────────────────────────────────────────────────────────
// Create
// ─────────────────────────────────────────────────────────────────────────────

func TestCreate_DispatchesBookingConfirmed(t *testing.T) {
	b := sampleBooking()
	store := &fakeStore{
		booking: b,
		contact: &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got != b {
		t.Fatalf("Create returned %+v, want the stored booking %+v", got, b)
	}
	if store.createCalls != 1 {
		t.Errorf("CreateBooking called %d times, want 1", store.createCalls)
	}

	if len(notifier.sent) != 1 {
		t.Fatalf("notifier received %d messages, want exactly 1", len(notifier.sent))
	}
	msg := notifier.sent[0]
	if msg.Kind != notification.KindBookingConfirmed {
		t.Errorf("message kind = %q, want %q", msg.Kind, notification.KindBookingConfirmed)
	}
	if msg.Recipient != testPhone {
		t.Errorf("recipient = %q, want %q", msg.Recipient, testPhone)
	}
	payload, ok := msg.Payload.(notification.BookingConfirmedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want BookingConfirmedPayload", msg.Payload)
	}
	if payload.BookingID != b.ID || payload.PitchName != testPitchName ||
		!payload.StartTime.Equal(b.StartTime) || !payload.EndTime.Equal(b.EndTime) {
		t.Errorf("payload = %+v, does not match booking %+v / pitch %q", payload, b, testPitchName)
	}
}

// TestCreate_Idempotent_FreshDispatches: with an Idempotency-Key, a FRESH (not
// replayed) create routes through the idempotent store path and still notifies.
func TestCreate_Idempotent_FreshDispatches(t *testing.T) {
	b := sampleBooking()
	store := &fakeStore{
		booking:            b,
		contact:            &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
		idempotentReplayed: false,
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	req := models.CreateBookingRequest{
		PitchID: 7, PlayerID: 3,
		Idempotency: &models.IdempotencyParams{Key: "k-1", Endpoint: "POST /bookings", Fingerprint: "fp"},
	}
	if _, err := svc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.idempotentCreateCalls != 1 || store.createCalls != 0 {
		t.Errorf("routing wrong: idempotent=%d plain=%d, want 1/0", store.idempotentCreateCalls, store.createCalls)
	}
	if store.lastIdem.Key != "k-1" {
		t.Errorf("idem key not threaded: %+v", store.lastIdem)
	}
	if len(notifier.sent) != 1 {
		t.Errorf("fresh idempotent create sent %d notifications, want 1", len(notifier.sent))
	}
}

// TestCreate_Idempotent_ReplaySuppressesNotification: a REPLAYED booking returns
// the original but must NOT re-notify — otherwise a retry double-notifies.
func TestCreate_Idempotent_ReplaySuppressesNotification(t *testing.T) {
	b := sampleBooking()
	store := &fakeStore{
		booking:            b,
		contact:            &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
		idempotentReplayed: true,
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	req := models.CreateBookingRequest{
		PitchID: 7, PlayerID: 3,
		Idempotency: &models.IdempotencyParams{Key: "k-1", Endpoint: "POST /bookings", Fingerprint: "fp"},
	}
	got, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create (replay): %v", err)
	}
	if got != b {
		t.Errorf("replay returned %+v, want original %+v", got, b)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("replay sent %d notifications, want 0 (suppressed)", len(notifier.sent))
	}
}

func TestCreate_StoreErrorIsReturnedAndNothingDispatched(t *testing.T) {
	boom := errors.New("insert failed")
	store := &fakeStore{createErr: boom}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got != nil {
		t.Errorf("Create returned %+v, want nil on store error", got)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 when persistence fails", len(notifier.sent))
	}
}

// TestCreate_PitchNotBookableYieldsNoSideEffect is the service-layer half of the
// bookability guard: when the store rejects a create because the target pitch is
// deactivated or soft-deleted (ErrPitchNotBookable), the Service returns the
// error and dispatches NOTHING — no booking_confirmed message fires. The SQL half
// (that a deactivated/deleted pitch actually resolves to ErrPitchNotBookable under
// the row lock, with no booking row written and no slot held) is covered by the
// live-DB integration test in internal/repository/booking_bookable_test.go.
func TestCreate_PitchNotBookableYieldsNoSideEffect(t *testing.T) {
	store := &fakeStore{createErr: repository.ErrPitchNotBookable}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if !errors.Is(err, repository.ErrPitchNotBookable) {
		t.Fatalf("err = %v, want ErrPitchNotBookable", err)
	}
	if got != nil {
		t.Errorf("Create returned %+v, want nil for a non-bookable pitch", got)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 for a non-bookable pitch", len(notifier.sent))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cancel
// ─────────────────────────────────────────────────────────────────────────────

func TestCancel_DispatchesBookingCancelledWithReason(t *testing.T) {
	b := sampleBooking()
	b.Status = models.StatusCancelled
	store := &fakeStore{
		booking: b,
		contact: &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	const reason = "player can no longer attend"
	params := repository.CancelBookingParams{
		BookingID: b.ID,
		ActorID:   int64Ptr(3),
		ActorRole: repository.ActorPlayer,
		Reason:    reason,
	}
	got, err := svc.Cancel(context.Background(), params)
	if err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if got != b {
		t.Fatalf("Cancel returned %+v, want the stored booking %+v", got, b)
	}

	// The reason the caller passed must reach the audit/persistence layer intact.
	if store.lastCancelParams.Reason != reason {
		t.Errorf("store recorded reason %q, want %q", store.lastCancelParams.Reason, reason)
	}
	if store.lastCancelParams.ActorRole != repository.ActorPlayer {
		t.Errorf("store recorded actor role %q, want %q", store.lastCancelParams.ActorRole, repository.ActorPlayer)
	}

	if len(notifier.sent) != 1 {
		t.Fatalf("notifier received %d messages, want exactly 1", len(notifier.sent))
	}
	msg := notifier.sent[0]
	if msg.Kind != notification.KindBookingCancelled {
		t.Errorf("message kind = %q, want %q", msg.Kind, notification.KindBookingCancelled)
	}
	payload, ok := msg.Payload.(notification.BookingCancelledPayload)
	if !ok {
		t.Fatalf("payload type = %T, want BookingCancelledPayload", msg.Payload)
	}
	if payload.Reason != reason {
		t.Errorf("payload reason = %q, want %q", payload.Reason, reason)
	}
	if payload.BookingID != b.ID || payload.PitchName != testPitchName {
		t.Errorf("payload = %+v, does not match booking %+v / pitch %q", payload, b, testPitchName)
	}
}

func TestCancel_DefaultsReasonFromActorRole(t *testing.T) {
	cases := []struct {
		name       string
		actorRole  string
		wantReason string
	}{
		{"player default", repository.ActorPlayer, reasonCancelledByPlayer},
		{"owner default", repository.ActorOwner, reasonCancelledByStaff},
		{"admin default", repository.ActorAdmin, reasonCancelledByStaff},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeStore{
				booking: sampleBooking(),
				contact: &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
			}
			notifier := &fakeNotifier{}
			svc := newService(store, notifier)

			_, err := svc.Cancel(context.Background(), repository.CancelBookingParams{
				BookingID: 42,
				ActorRole: c.actorRole,
				// Reason intentionally empty → service fills the default.
			})
			if err != nil {
				t.Fatalf("Cancel returned error: %v", err)
			}
			if store.lastCancelParams.Reason != c.wantReason {
				t.Errorf("defaulted reason = %q, want %q", store.lastCancelParams.Reason, c.wantReason)
			}
			payload := notifier.sent[0].Payload.(notification.BookingCancelledPayload)
			if payload.Reason != c.wantReason {
				t.Errorf("payload reason = %q, want %q", payload.Reason, c.wantReason)
			}
		})
	}
}

func TestCancel_StoreErrorIsReturnedAndNothingDispatched(t *testing.T) {
	store := &fakeStore{cancelErr: repository.ErrInvalidStatusTransition}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Cancel(context.Background(), repository.CancelBookingParams{
		BookingID: 42, ActorRole: repository.ActorPlayer,
	})
	if !errors.Is(err, repository.ErrInvalidStatusTransition) {
		t.Fatalf("err = %v, want ErrInvalidStatusTransition", err)
	}
	if got != nil {
		t.Errorf("Cancel returned %+v, want nil on store error", got)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 when cancellation fails", len(notifier.sent))
	}
}

// TestCancel_NotOwnedYields404WithNoNotification is the service-layer half of the
// IDOR guard: when the ownership-scoped store rejects a cancel as not-found (the
// caller does not own the booking), the Service returns the error and dispatches
// NOTHING — no booking_cancelled message reaches another owner's player. The SQL
// half (that a foreign owner actually resolves to ErrBookingNotFound, and that no
// slot is released / no audit row is written) is covered by the live-DB
// integration test in internal/repository/booking_cancel_scoping_test.go.
func TestCancel_NotOwnedYields404WithNoNotification(t *testing.T) {
	store := &fakeStore{cancelErr: repository.ErrBookingNotFound}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Cancel(context.Background(), repository.CancelBookingParams{
		BookingID: 42, ActorID: int64Ptr(99), ActorRole: repository.ActorOwner,
	})
	if !errors.Is(err, repository.ErrBookingNotFound) {
		t.Fatalf("err = %v, want ErrBookingNotFound", err)
	}
	if got != nil {
		t.Errorf("Cancel returned %+v, want nil for a non-owned booking", got)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 for a non-owned (404) cancel", len(notifier.sent))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Best-effort notification semantics
// ─────────────────────────────────────────────────────────────────────────────

func TestCreate_NotifierFailureDoesNotFailBooking(t *testing.T) {
	b := sampleBooking()
	store := &fakeStore{
		booking: b,
		contact: &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
	}
	notifier := &fakeNotifier{err: errors.New("provider unreachable")}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if err != nil {
		t.Fatalf("Create returned error %v, want nil — the confirmed booking must stand despite a delivery failure", err)
	}
	if got != b {
		t.Fatalf("Create returned %+v, want the stored booking", got)
	}
	if len(notifier.sent) != 1 {
		t.Errorf("notifier saw %d send attempts, want 1", len(notifier.sent))
	}
}

func TestDispatch_MissingPhoneSkipsSend(t *testing.T) {
	store := &fakeStore{
		booking: sampleBooking(),
		contact: &repository.BookingContact{Phone: "", PitchName: testPitchName}, // no phone on file
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got == nil {
		t.Fatal("Create returned nil booking, want the stored booking")
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 when no phone is on file", len(notifier.sent))
	}
}

func TestDispatch_ContactLookupErrorSkipsSend(t *testing.T) {
	store := &fakeStore{
		booking:    sampleBooking(),
		contactErr: errors.New("contact lookup failed"),
	}
	notifier := &fakeNotifier{}
	svc := newService(store, notifier)

	got, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3})
	if err != nil {
		t.Fatalf("Create returned error %v, want nil — a contact lookup failure must not undo the booking", err)
	}
	if got == nil {
		t.Fatal("Create returned nil booking, want the stored booking")
	}
	if len(notifier.sent) != 0 {
		t.Errorf("notifier received %d messages, want 0 when contact lookup fails", len(notifier.sent))
	}
}

// TestService_DispatchesThroughRealNotificationService is a lightweight
// integration check: the Service wired to a real *notification.Service over a
// Fake channel delivers a booking_confirmed message end-to-end. Booking events
// are UTILITY-category and must NOT be blocked by the opt-in gate even when no
// OptInChecker is configured.
func TestService_DispatchesThroughRealNotificationService(t *testing.T) {
	b := sampleBooking()
	store := &fakeStore{
		booking: b,
		contact: &repository.BookingContact{Phone: testPhone, PitchName: testPitchName},
	}

	fake := notification.NewFakeChannel(notification.FakeSilent())
	notifier := notification.NewService(
		notification.ChannelFake,
		notification.WithChannel(notification.ChannelFake, fake),
	)
	svc := newService(store, notifier)

	if _, err := svc.Create(context.Background(), models.CreateBookingRequest{PitchID: 7, PlayerID: 3}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if fake.Count() != 1 {
		t.Fatalf("fake channel recorded %d messages, want 1", fake.Count())
	}
	last, _ := fake.Last()
	if last.Kind != notification.KindBookingConfirmed {
		t.Errorf("delivered kind = %q, want %q", last.Kind, notification.KindBookingConfirmed)
	}
	if last.Recipient != testPhone {
		t.Errorf("delivered recipient = %q, want %q", last.Recipient, testPhone)
	}
}
