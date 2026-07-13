// Package booking holds the booking domain orchestration (PART 5). The Service
// ties three concerns together for each state transition under the INSTANT
// BOOKING model:
//
//  1. Persistence + audit — delegated to the Store, which writes the booking
//     state change and its status_transitions audit row in one transaction
//     (Architecture Principle 4).
//  2. Notification — every transition dispatches a channel-agnostic
//     OutboundMessage through the NotificationService (Notifier), per the
//     notification-abstraction principle. There are NO direct provider calls
//     here; the Service only speaks the notification contract.
//
// There is deliberately no payment, deposit, or refund logic and no admin
// approval step: a created booking is immediately confirmed, and a confirmed
// booking can be cancelled by the player or an owner/admin.
package booking

import (
	"context"
	"log"

	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/repository"
)

// Store is the persistence seam the Service depends on. The concrete
// repository.BookingRepository satisfies it; tests provide an in-memory fake.
// Implementations MUST record the state transition and its audit row
// atomically.
type Store interface {
	CreateBooking(ctx context.Context, req models.CreateBookingRequest) (*models.Booking, error)
	CreateBookingIdempotent(ctx context.Context, req models.CreateBookingRequest, idem models.IdempotencyParams) (*models.Booking, bool, error)
	CancelBooking(ctx context.Context, params repository.CancelBookingParams) (*models.Booking, error)
	GetBookingContact(ctx context.Context, bookingID int64) (*repository.BookingContact, error)
}

// Notifier is the outbound-message seam. *notification.Service satisfies it,
// so the Service routes through the opt-in gate and active channel without
// knowing any provider details. Tests provide a recording fake.
type Notifier interface {
	Send(ctx context.Context, msg notification.OutboundMessage) (notification.DeliveryResult, error)
}

// Compile-time guarantee that the production repository satisfies Store. If the
// interface ever drifts from the repository, the build fails here.
var _ Store = (repository.BookingRepository)(nil)

// Default audit reasons for cancellations when the caller supplies none.
const (
	reasonCancelledByPlayer = "cancelled by player"
	reasonCancelledByStaff  = "cancelled by pitch owner/admin"
)

// Service orchestrates booking state transitions: persistence + audit via the
// Store, and player notification via the Notifier.
type Service struct {
	store    Store
	notifier Notifier
	logger   *log.Logger
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithLogger sets the logger used for best-effort notification diagnostics.
// Defaults to log.Default(); pass a discarding logger to silence tests.
func WithLogger(l *log.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewService builds a booking Service over the given Store and Notifier.
func NewService(store Store, notifier Notifier, opts ...Option) *Service {
	s := &Service{
		store:    store,
		notifier: notifier,
		logger:   log.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create performs an instant booking: the Store persists it as confirmed and
// records the creation transition, then the player is notified with a
// booking_confirmed message. A persistence failure aborts and returns the
// error (no notification is sent). Notification is best-effort — a delivery
// failure is logged but does not undo the confirmed booking.
func (s *Service) Create(ctx context.Context, req models.CreateBookingRequest) (*models.Booking, error) {
	// Idempotent path: when the handler attached an Idempotency-Key, route through
	// the store's idempotent create so a double-tap / retry replays the original
	// booking. On a replay no new booking was created, so the confirmation
	// notification MUST be suppressed — otherwise a retry would re-notify.
	if req.Idempotency != nil {
		b, replayed, err := s.store.CreateBookingIdempotent(ctx, req, *req.Idempotency)
		if err != nil {
			return nil, err
		}
		if !replayed {
			s.dispatch(ctx, b, notification.KindBookingConfirmed, "")
		}
		return b, nil
	}

	b, err := s.store.CreateBooking(ctx, req)
	if err != nil {
		return nil, err
	}

	s.dispatch(ctx, b, notification.KindBookingConfirmed, "")
	return b, nil
}

// Cancel transitions a confirmed booking to cancelled (releasing the slot),
// records the audited transition, and notifies the player with a
// booking_cancelled message. The actor (player or owner/admin) and reason are
// captured in the audit trail. A missing reason is defaulted from the actor
// role. As with Create, notification is best-effort.
func (s *Service) Cancel(ctx context.Context, params repository.CancelBookingParams) (*models.Booking, error) {
	if params.Reason == "" {
		params.Reason = defaultCancelReason(params.ActorRole)
	}

	b, err := s.store.CancelBooking(ctx, params)
	if err != nil {
		return nil, err
	}

	s.dispatch(ctx, b, notification.KindBookingCancelled, params.Reason)
	return b, nil
}

// dispatch resolves the player's contact details and sends the booking event.
// It is best-effort: any failure (contact lookup, missing phone, delivery) is
// logged and swallowed so the persisted, audited transition stands on its own.
func (s *Service) dispatch(ctx context.Context, b *models.Booking, kind notification.MessageKind, reason string) {
	// Notify guard (PR 2): only PLAYER bookings have a recipient. A block (and, in
	// PR 3, an academy session) has no player_id, so it has no one to notify — and
	// GetBookingContact's INNER JOIN on player_id would not resolve. The audited
	// state transition (written by the Store, in-tx) stands regardless; we only
	// skip the side-effect here, keeping one create/cancel path for every source.
	if b.Source != "" && b.Source != models.SourcePlayer {
		return
	}

	contact, err := s.store.GetBookingContact(ctx, b.ID)
	if err != nil {
		s.logger.Printf("[booking] notify: contact lookup failed for booking %d: %v", b.ID, err)
		return
	}
	if contact.Phone == "" {
		s.logger.Printf("[booking] notify: booking %d has no phone on file, skipping %s", b.ID, kind)
		return
	}

	var payload notification.Payload
	switch kind {
	case notification.KindBookingConfirmed:
		// The Arabic confirmation greets the player by name ({{1}}). A row with no
		// resolvable name (no contact_name snapshot and no users.full_name) must NOT
		// send a blank-name greeting — skip the confirmation entirely (owner ruling).
		if contact.PlayerName == "" {
			s.logger.Printf("[booking] notify: booking %d has no player name on file, skipping %s", b.ID, kind)
			return
		}
		payload = notification.BookingConfirmedPayload{
			BookingID:  b.ID,
			PlayerName: contact.PlayerName,
			PitchName:  contact.PitchName,
			Location:   contact.Location,
			StartTime:  b.StartTime,
			EndTime:    b.EndTime,
			Amount:     b.TotalPrice,
		}
	case notification.KindBookingCancelled:
		payload = notification.BookingCancelledPayload{
			BookingID: b.ID,
			PitchName: contact.PitchName,
			StartTime: b.StartTime,
			EndTime:   b.EndTime,
			Reason:    reason,
		}
	default:
		s.logger.Printf("[booking] notify: unsupported message kind %q for booking %d", kind, b.ID)
		return
	}

	msg := notification.OutboundMessage{
		Recipient: contact.Phone,
		Kind:      kind,
		Payload:   payload,
	}
	if _, err := s.notifier.Send(ctx, msg); err != nil {
		s.logger.Printf("[booking] notify: sending %s for booking %d failed: %v", kind, b.ID, err)
	}
}

// defaultCancelReason picks a human-readable audit reason from the actor role
// when the caller did not provide one.
func defaultCancelReason(actorRole string) string {
	switch actorRole {
	case repository.ActorOwner, repository.ActorAdmin:
		return reasonCancelledByStaff
	default:
		return reasonCancelledByPlayer
	}
}
