package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrDoubleBooking           = errors.New("booking: time slot conflicts with an existing reservation")
	ErrPitchNotFound           = errors.New("booking: pitch not found or not available")
	ErrBookingNotFound         = errors.New("booking: booking does not exist")
	ErrInvalidStatusTransition = errors.New("booking: status transition is not permitted")
)

const pgExclusionViolation = "23P01"

// Actor roles recorded in the status_transitions audit trail (Architecture
// Principle 4). They map onto status_transitions.actor_role (VARCHAR(20)).
const (
	ActorPlayer = "player"
	ActorOwner  = "owner"
	ActorAdmin  = "admin"
	ActorSystem = "system"
)

// reasonBookingCreated is the audit reason stored for the implicit
// creation → confirmed transition under instant booking.
const reasonBookingCreated = "player created booking (instant confirmation)"

// CancelBookingParams carries everything needed to cancel a confirmed booking
// and audit the transition. ActorID is nil when the system (not a user) acts.
type CancelBookingParams struct {
	BookingID int64
	ActorID   *int64
	ActorRole string
	Reason    string
}

// BookingContact holds the data required to notify the player about a booking
// event: their E.164 phone (the message recipient) and the pitch name.
type BookingContact struct {
	Phone     string
	PitchName string
}

// ─────────────────────────────────────────────────────────────────────────────
// Interface
// ─────────────────────────────────────────────────────────────────────────────

type BookingRepository interface {
	CreateBooking(ctx context.Context, req models.CreateBookingRequest) (*models.Booking, error)
	GetBookedSlots(ctx context.Context, pitchID int, date time.Time) ([]models.AvailabilitySlot, error)
	GetUserBookings(ctx context.Context, userID int64) ([]models.Booking, error)
	GetAllBookings(ctx context.Context, ownerID int64) ([]models.AdminBooking, error)
	UpdateBookingStatus(
		ctx context.Context,
		bookingID int,
		newStatus models.BookingStatus,
		allowedFrom []models.BookingStatus,
	) (*models.Booking, error)

	// CancelBooking transitions a confirmed booking to cancelled and records the
	// transition in the audit trail, atomically. Cancelling releases the slot for
	// re-booking (the anti-overlap EXCLUDE constraint ignores cancelled rows).
	CancelBooking(ctx context.Context, params CancelBookingParams) (*models.Booking, error)

	// GetBookingContact returns the player's phone and the pitch name for a
	// booking — the inputs a notification channel needs to reach the player.
	GetBookingContact(ctx context.Context, bookingID int64) (*BookingContact, error)
}

type bookingRepo struct {
	db *pgxpool.Pool
}

func NewBookingRepository(db *pgxpool.Pool) BookingRepository {
	return &bookingRepo{db: db}
}

// ─────────────────────────────────────────────────────────────────────────────
// CreateBooking
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) CreateBooking(
	ctx context.Context,
	req models.CreateBookingRequest,
) (*models.Booking, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CreateBooking: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var pricePerHour float64
	var pitchName string

	// جلب سعر الملعب لحساب التكلفة الإجمالية
	err = tx.QueryRow(ctx, `
		SELECT name, price_per_hour
		FROM pitches
		WHERE id = $1
		FOR SHARE
	`, req.PitchID).Scan(&pitchName, &pricePerHour)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPitchNotFound
		}
		return nil, fmt.Errorf("CreateBooking: fetch pitch: %w", err)
	}

	durationHours := req.EndTime.Sub(req.StartTime).Hours()
	totalPrice := math.Round(durationHours*pricePerHour*1000) / 1000

	var b models.Booking

	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status)
		VALUES ($1, $2, tsrange($3::timestamp, $4::timestamp, '[)'), $5, 'confirmed')
		RETURNING
			id,
			pitch_id,
			player_id,
			lower(booking_range) AS start_time,
			upper(booking_range) AS end_time,
			status,
			total_price,
			created_at
	`,
		req.PitchID, req.PlayerID,
		req.StartTime.UTC(), req.EndTime.UTC(),
		totalPrice,
	).Scan(
		&b.ID, &b.PitchID, &b.PlayerID,
		&b.StartTime, &b.EndTime,
		&b.Status, &b.TotalPrice,
		&b.CreatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgExclusionViolation {
			return nil, ErrDoubleBooking
		}
		return nil, fmt.Errorf("CreateBooking: insert: %w", err)
	}

	// Audit the implicit creation → confirmed transition (Architecture
	// Principle 4). from_status is NULL for the initial event; the actor is the
	// player who created the booking. Recorded in the same transaction so a
	// booking can never exist without its creation audit row.
	if _, err = tx.Exec(ctx, `
		INSERT INTO status_transitions
			(booking_id, from_status, to_status, actor_id, actor_role, reason)
		VALUES ($1, NULL, 'confirmed', $2, $3, $4)
	`, b.ID, req.PlayerID, ActorPlayer, reasonBookingCreated); err != nil {
		return nil, fmt.Errorf("CreateBooking: record transition: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CreateBooking: commit: %w", err)
	}

	return &b, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetBookedSlots
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) GetBookedSlots(
	ctx context.Context,
	pitchID int,
	date time.Time,
) ([]models.AvailabilitySlot, error) {

	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := r.db.Query(ctx, `
		SELECT id, lower(booking_range) AS start_time, upper(booking_range) AS end_time, status
		FROM bookings
		WHERE pitch_id = $1
		  AND status <> 'cancelled'
		  AND booking_range && tsrange($2::timestamp, $3::timestamp, '[)')
		ORDER BY lower(booking_range)
	`, pitchID, dayStart, dayEnd)

	if err != nil {
		return nil, fmt.Errorf("GetBookedSlots: query: %w", err)
	}
	defer rows.Close()

	slots := make([]models.AvailabilitySlot, 0)
	for rows.Next() {
		var s models.AvailabilitySlot
		if err := rows.Scan(&s.BookingID, &s.StartTime, &s.EndTime, &s.Status); err != nil {
			return nil, fmt.Errorf("GetBookedSlots: scan: %w", err)
		}
		slots = append(slots, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetBookedSlots: rows error: %w", err)
	}

	return slots, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetUserBookings
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) GetUserBookings(ctx context.Context, userID int64) ([]models.Booking, error) {
	rows, err := r.db.Query(ctx, `
		SELECT b.id, b.pitch_id, COALESCE(p.name, '') AS pitch_name,
		       b.player_id,
		       lower(b.booking_range) AS start_time, upper(b.booking_range) AS end_time,
		       b.status, b.total_price, b.created_at
		FROM bookings b
		LEFT JOIN pitches p ON p.id = b.pitch_id
		WHERE b.player_id = $1
		ORDER BY b.created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("GetUserBookings: query: %w", err)
	}
	defer rows.Close()

	bookings := make([]models.Booking, 0)
	for rows.Next() {
		var b models.Booking
		if err := rows.Scan(
			&b.ID, &b.PitchID, &b.PitchName,
			&b.PlayerID, &b.StartTime, &b.EndTime,
			&b.Status, &b.TotalPrice, &b.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetUserBookings: scan: %w", err)
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetUserBookings: rows error: %w", err)
	}
	return bookings, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetAllBookings
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) GetAllBookings(ctx context.Context, ownerID int64) ([]models.AdminBooking, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			b.id,
			b.pitch_id,    COALESCE(p.name,      '') AS pitch_name,
			b.player_id,   COALESCE(u.full_name,  '') AS user_name,
			               COALESCE(u.email,      '') AS user_email,
			lower(b.booking_range) AS start_time, upper(b.booking_range) AS end_time,
			b.status, b.total_price, b.created_at
		FROM bookings b
		INNER JOIN pitches p ON p.id = b.pitch_id AND p.owner_id = $1
		LEFT JOIN  users   u ON u.id = b.player_id
		ORDER BY b.created_at DESC
	`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("GetAllBookings: query: %w", err)
	}
	defer rows.Close()

	bookings := make([]models.AdminBooking, 0)
	for rows.Next() {
		var b models.AdminBooking
		if err := rows.Scan(
			&b.ID, &b.PitchID, &b.PitchName,
			&b.PlayerID, &b.UserName, &b.UserEmail,
			&b.StartTime, &b.EndTime,
			&b.Status, &b.TotalPrice, &b.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetAllBookings: scan: %w", err)
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetAllBookings: rows error: %w", err)
	}
	return bookings, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UpdateBookingStatus
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) UpdateBookingStatus(
	ctx context.Context,
	bookingID int,
	newStatus models.BookingStatus,
	allowedFrom []models.BookingStatus,
) (*models.Booking, error) {

	allowed := make([]string, len(allowedFrom))
	for i, s := range allowedFrom {
		allowed[i] = string(s)
	}

	var b models.Booking

	err := r.db.QueryRow(ctx, `
		UPDATE bookings
		SET status = $2
		WHERE id = $1 AND status = ANY($3::booking_status[])
		RETURNING
			id,
			pitch_id,
			player_id,
			lower(booking_range) AS start_time,
			upper(booking_range) AS end_time,
			status,
			total_price,
			created_at
	`, bookingID, string(newStatus), allowed).Scan(
		&b.ID,
		&b.PitchID,
		&b.PlayerID,
		&b.StartTime,
		&b.EndTime,
		&b.Status,
		&b.TotalPrice,
		&b.CreatedAt,
	)

	if err == nil {
		return &b, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("UpdateBookingStatus: update: %w", err)
	}

	var exists bool
	if scanErr := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM bookings WHERE id = $1)`,
		bookingID,
	).Scan(&exists); scanErr != nil {
		return nil, fmt.Errorf("UpdateBookingStatus: exists check: %w", scanErr)
	}

	if !exists {
		return nil, ErrBookingNotFound
	}

	return nil, ErrInvalidStatusTransition
}

// ─────────────────────────────────────────────────────────────────────────────
// CancelBooking
// ─────────────────────────────────────────────────────────────────────────────

// CancelBooking transitions a confirmed booking to cancelled and records the
// transition in status_transitions, both in a single transaction so the audit
// trail can never drift from the booking state. Only confirmed → cancelled is
// permitted (instant booking has no pending state). Setting status to cancelled
// releases the slot: the anti-double-booking EXCLUDE constraint is defined
// WHERE (status <> 'cancelled'), so the slot is immediately bookable again.
func (r *bookingRepo) CancelBooking(
	ctx context.Context,
	p CancelBookingParams,
) (*models.Booking, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CancelBooking: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var b models.Booking

	err = tx.QueryRow(ctx, `
		UPDATE bookings
		SET status = 'cancelled'
		WHERE id = $1 AND status = 'confirmed'
		RETURNING
			id,
			pitch_id,
			player_id,
			lower(booking_range) AS start_time,
			upper(booking_range) AS end_time,
			status,
			total_price,
			created_at
	`, p.BookingID).Scan(
		&b.ID, &b.PitchID, &b.PlayerID,
		&b.StartTime, &b.EndTime,
		&b.Status, &b.TotalPrice,
		&b.CreatedAt,
	)

	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("CancelBooking: update: %w", err)
		}

		// No row updated: either the booking does not exist, or it was not in a
		// cancellable (confirmed) state. Disambiguate for a precise error.
		var exists bool
		if scanErr := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM bookings WHERE id = $1)`,
			p.BookingID,
		).Scan(&exists); scanErr != nil {
			return nil, fmt.Errorf("CancelBooking: exists check: %w", scanErr)
		}
		if !exists {
			return nil, ErrBookingNotFound
		}
		return nil, ErrInvalidStatusTransition
	}

	// Audit the confirmed → cancelled transition. actor_id is NULL when ActorID
	// is nil (system action); actor_role and reason capture who acted and why.
	if _, err = tx.Exec(ctx, `
		INSERT INTO status_transitions
			(booking_id, from_status, to_status, actor_id, actor_role, reason)
		VALUES ($1, 'confirmed', 'cancelled', $2, $3, $4)
	`, b.ID, p.ActorID, p.ActorRole, p.Reason); err != nil {
		return nil, fmt.Errorf("CancelBooking: record transition: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CancelBooking: commit: %w", err)
	}

	return &b, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetBookingContact
// ─────────────────────────────────────────────────────────────────────────────

// GetBookingContact returns the player's phone (E.164) and the pitch name for a
// booking, joining users and pitches. A missing phone is surfaced as an empty
// string (the caller decides whether a notification can be dispatched).
func (r *bookingRepo) GetBookingContact(
	ctx context.Context,
	bookingID int64,
) (*BookingContact, error) {

	var c BookingContact
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(u.phone, ''), COALESCE(p.name, '')
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		JOIN users   u ON u.id = b.player_id
		WHERE b.id = $1
	`, bookingID).Scan(&c.Phone, &c.PitchName)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, fmt.Errorf("GetBookingContact: %w", err)
	}

	return &c, nil
}