package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrDoubleBooking           = errors.New("booking: time slot conflicts with an existing reservation")
	ErrPitchNotFound           = errors.New("booking: pitch not found or not available")
	ErrPitchNotBookable        = errors.New("booking: pitch is deactivated or deleted and cannot be booked")
	ErrBookingNotFound         = errors.New("booking: booking does not exist")
	ErrInvalidStatusTransition = errors.New("booking: status transition is not permitted")

	// ErrSlotOutsideOperatingHours means the requested slot is not fully contained
	// within any of the pitch's configured open windows (the operating-hours gate).
	// Only raised for gated (player) writes on a pitch that HAS a schedule; an
	// unconfigured pitch is open 24/7 and never trips this. Mapped to 422.
	ErrSlotOutsideOperatingHours = errors.New("booking: requested slot is outside the pitch's operating hours")

	// ErrIdempotencyInProgress means a booking attempt with this idempotency key is
	// still in flight (a committed 'pending' claim that has not completed) — the
	// caller should retry shortly. Mapped to 409.
	ErrIdempotencyInProgress = errors.New("booking: a request with this idempotency key is already in progress")
	// ErrIdempotencyKeyConflict means the idempotency key was reused with a
	// DIFFERENT request body (fingerprint mismatch) — a client bug. Mapped to 422.
	ErrIdempotencyKeyConflict = errors.New("booking: idempotency key reused with a different request")
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

	// CreateBookingIdempotent creates a booking under an idempotency key (see
	// models.IdempotencyParams), in ONE transaction with the key's claim+completion
	// so the booking and its idempotency record commit together. replayed is true
	// when the original booking is returned from a prior completed attempt (no new
	// booking created, so the caller must skip side effects like notifications). It
	// returns ErrIdempotencyKeyConflict (key reused with a different body) or
	// ErrIdempotencyInProgress (a committed pending claim) for those cases.
	CreateBookingIdempotent(ctx context.Context, req models.CreateBookingRequest, idem models.IdempotencyParams) (booking *models.Booking, replayed bool, err error)

	// DeleteExpiredIdempotencyKeys prunes idempotency rows past their TTL and
	// returns how many were removed. Safe to call periodically.
	DeleteExpiredIdempotencyKeys(ctx context.Context, now time.Time) (int64, error)

	GetBookedSlots(ctx context.Context, pitchID int, date time.Time) ([]models.AvailabilitySlot, error)

	// GetOpenWindows resolves the pitch's weekly operating hours to the concrete
	// UTC open intervals touching the given Amman calendar date. hasSchedule is
	// false when the pitch has NO configured windows (open 24/7 per the fail-open
	// decision) — callers must NOT read an empty slice as "closed" without it.
	GetOpenWindows(ctx context.Context, pitchID int, date time.Time) (intervals []data.ConcreteInterval, hasSchedule bool, err error)
	GetUserBookings(ctx context.Context, userID int64) ([]models.Booking, error)

	// GetAllBookings lists bookings scoped to the actor: an admin sees every
	// booking on the platform, an owner sees only bookings whose pitch they own
	// (the ownership predicate is applied in SQL via the pitches join).
	GetAllBookings(ctx context.Context, actor auth.Actor) ([]models.AdminBooking, error)
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

	b, err := insertConfirmedBookingTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CreateBooking: commit: %w", err)
	}
	return b, nil
}

// insertConfirmedBookingTx performs the pitch-lock + bookability guard + booking
// insert + creation-audit INSIDE the caller's transaction, returning the created
// confirmed booking. It neither begins nor commits — the caller owns the tx — so
// the same body backs both the plain create path and the idempotent one, keeping
// a single source of truth for the slot-conflict (EXCLUDE) and audit invariants.
func insertConfirmedBookingTx(ctx context.Context, tx pgx.Tx, req models.CreateBookingRequest) (*models.Booking, error) {
	var pricePerHour float64
	var pitchName string
	var isActive bool
	var deletedAt *time.Time

	// جلب سعر الملعب لحساب التكلفة الإجمالية، مع قفل صف الملعب للتحقق من توفّره.
	//
	// Resolve and LOCK the target pitch row (FOR UPDATE) inside the booking
	// transaction. The lock serialises against the is_active toggle and the
	// soft-delete UPDATE, so a pitch cannot be deactivated or deleted in the
	// window between this bookability check and the INSERT below (no TOCTOU race).
	// The bookability flags are fetched here — not via a separate pre-flight
	// SELECT — so the predicate is evaluated under the same lock that protects the
	// insert. The WHERE clause matches the pitch by id only (no active/deleted
	// predicate) so a non-bookable pitch is still resolved and can be told apart
	// from a genuinely missing id.
	err := tx.QueryRow(ctx, `
		SELECT name, price_per_hour, is_active, deleted_at
		FROM pitches
		WHERE id = $1
		FOR UPDATE
	`, req.PitchID).Scan(&pitchName, &pricePerHour, &isActive, &deletedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPitchNotFound
		}
		return nil, fmt.Errorf("CreateBooking: fetch pitch: %w", err)
	}

	// Bookability guard: a deactivated or soft-deleted pitch must not accept
	// bookings even via a handcrafted request that bypasses the read-layer filter.
	// Aborting here — before any booking row, slot hold, audit row, or
	// notification side effect — leaves the transaction with zero effect on
	// rollback.
	if !isActive || deletedAt != nil {
		return nil, ErrPitchNotBookable
	}

	// Operating-hours gate (locked decision #2). A player booking must fall FULLY
	// within one configured open window (containment, not overlap). The schedule is
	// read UNDER THE SAME pitch lock taken above, so a concurrent PUT
	// /operating-hours cannot open a TOCTOU gap between this check and the INSERT —
	// the booking is evaluated against the strictly latest committed schedule.
	// Fail-open: a pitch with NO configured windows is open 24/7 (hasSchedule
	// false). Owner/admin-initiated writes set BypassHoursGate and skip the gate.
	if !req.BypassHoursGate {
		windows, err := loadOperatingWindowsTx(ctx, tx, req.PitchID)
		if err != nil {
			return nil, err
		}
		if len(windows) > 0 { // configured → fail closed unless contained
			resolved, err := data.ResolveWindowsForDate(windows, timeutil.InAmman(req.StartTime))
			if err != nil {
				return nil, fmt.Errorf("CreateBooking: resolve operating hours: %w", err)
			}
			if !data.SlotContained(req.StartTime, req.EndTime, resolved) {
				return nil, ErrSlotOutsideOperatingHours
			}
		}
	}

	durationHours := req.EndTime.Sub(req.StartTime).Hours()
	totalPrice := math.Round(durationHours*pricePerHour*1000) / 1000

	var b models.Booking

	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), $5, 'confirmed')
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

	return &b, nil
}

// idempotencyTTL is how long an idempotency key remains in force. A key is a
// random UUID per booking ATTEMPT, so 24h comfortably covers any client retry
// window while bounding storage; expired rows are pruned by
// DeleteExpiredIdempotencyKeys.
const idempotencyTTL = 24 * time.Hour

// CreateBookingIdempotent runs the booking insert under an idempotency key, in
// ONE transaction with the key's claim and completion. See the interface doc for
// the contract. Concurrency model: the claim is `INSERT ... ON CONFLICT DO
// NOTHING`, which WAITS on a concurrent uncommitted claim for the same key and
// then observes its committed outcome — so a genuine double-tap serialises and
// the loser REPLAYS the winner's booking (no duplicate, no 409 in the common
// case). ErrIdempotencyInProgress guards a committed 'pending' claim (a prior
// attempt that crashed between claim and completion).
func (r *bookingRepo) CreateBookingIdempotent(
	ctx context.Context,
	req models.CreateBookingRequest,
	idem models.IdempotencyParams,
) (*models.Booking, bool, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("CreateBookingIdempotent: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()

	// Claim the key with a pending row. On conflict (key already exists for this
	// user) nothing is inserted and QueryRow returns ErrNoRows.
	var claimedID int64
	claimErr := tx.QueryRow(ctx, `
		INSERT INTO booking_idempotency_keys
			(idem_key, user_id, endpoint, fingerprint, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6)
		ON CONFLICT (user_id, idem_key) DO NOTHING
		RETURNING id
	`, idem.Key, req.PlayerID, idem.Endpoint, idem.Fingerprint, now, now.Add(idempotencyTTL)).Scan(&claimedID)

	switch {
	case claimErr == nil:
		// We own the claim — create the booking and complete the record. A booking
		// error (e.g. ErrDoubleBooking) rolls the whole tx back, including the claim,
		// so the key is not burned and the client may retry.
		b, err := insertConfirmedBookingTx(ctx, tx, req)
		if err != nil {
			return nil, false, err
		}
		snapshot, err := json.Marshal(b)
		if err != nil {
			return nil, false, fmt.Errorf("CreateBookingIdempotent: snapshot response: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking_idempotency_keys
			SET status = 'completed', booking_id = $2, response = $3
			WHERE id = $1
		`, claimedID, b.ID, snapshot); err != nil {
			return nil, false, fmt.Errorf("CreateBookingIdempotent: complete: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("CreateBookingIdempotent: commit: %w", err)
		}
		return b, false, nil

	case errors.Is(claimErr, pgx.ErrNoRows):
		// Key already exists (committed by a prior/concurrent attempt). Inspect it.
		var status, fingerprint string
		var response []byte
		if err := tx.QueryRow(ctx, `
			SELECT status, fingerprint, response
			FROM booking_idempotency_keys
			WHERE user_id = $1 AND idem_key = $2
		`, req.PlayerID, idem.Key).Scan(&status, &fingerprint, &response); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The conflicting row disappeared (the other attempt rolled back) between
				// our claim and this read — treat as in-progress; the client retries.
				return nil, false, ErrIdempotencyInProgress
			}
			return nil, false, fmt.Errorf("CreateBookingIdempotent: load existing: %w", err)
		}

		// A reused key with a different request body is a client bug, regardless of
		// the prior attempt's state.
		if fingerprint != idem.Fingerprint {
			return nil, false, ErrIdempotencyKeyConflict
		}

		switch status {
		case "completed":
			var b models.Booking
			if err := json.Unmarshal(response, &b); err != nil {
				return nil, false, fmt.Errorf("CreateBookingIdempotent: decode replay: %w", err)
			}
			return &b, true, nil // replayed — caller MUST skip notification
		case "pending":
			return nil, false, ErrIdempotencyInProgress
		default:
			return nil, false, fmt.Errorf("CreateBookingIdempotent: unexpected status %q", status)
		}

	default:
		return nil, false, fmt.Errorf("CreateBookingIdempotent: claim: %w", claimErr)
	}
}

// DeleteExpiredIdempotencyKeys prunes idempotency rows past their TTL.
func (r *bookingRepo) DeleteExpiredIdempotencyKeys(ctx context.Context, now time.Time) (int64, error) {
	ct, err := r.db.Exec(ctx,
		`DELETE FROM booking_idempotency_keys WHERE expires_at <= $1`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("DeleteExpiredIdempotencyKeys: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetBookedSlots
// ─────────────────────────────────────────────────────────────────────────────

func (r *bookingRepo) GetBookedSlots(
	ctx context.Context,
	pitchID int,
	date time.Time,
) ([]models.AvailabilitySlot, error) {

	// Player-facing availability is gated on the pitch being active and not
	// soft-deleted: a deactivated pitch exposes no availability (ErrPitchNotFound
	// → 404), so it cannot be booked through this surface. Owner/admin listings
	// use a different path and are unaffected.
	var active bool
	switch err := r.db.QueryRow(ctx,
		`SELECT is_active FROM pitches WHERE id = $1 AND deleted_at IS NULL`,
		pitchID,
	).Scan(&active); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrPitchNotFound
	case err != nil:
		return nil, fmt.Errorf("GetBookedSlots: pitch lookup: %w", err)
	case !active:
		return nil, ErrPitchNotFound
	}

	// "A day" is the Asia/Amman CIVIL day, not a UTC day: a slot at 00:30 Amman is
	// stored as 21:30-the-previous-day UTC, so UTC day bounds would wrongly exclude
	// the first three hours of the local day (and include late-evening slots). Take
	// the day's bounds as absolute UTC instants of the Amman calendar day; because
	// booking_range is a tstzrange (true points in time), the UTC-zoned bounds are
	// compared as instants via ::timestamptz — timezone-independent.
	dayStart, dayEnd := timeutil.AmmanDayBoundsUTC(date)

	rows, err := r.db.Query(ctx, `
		SELECT id, lower(booking_range) AS start_time, upper(booking_range) AS end_time, status
		FROM bookings
		WHERE pitch_id = $1
		  AND status <> 'cancelled'
		  AND booking_range && tstzrange($2::timestamptz, $3::timestamptz, '[)')
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
// Operating hours
// ─────────────────────────────────────────────────────────────────────────────

// rowQuerier is the read surface common to *pgxpool.Pool and pgx.Tx, so the
// operating-hours loader works both on the connection pool (availability read)
// and inside the booking transaction (write-path gate, under the pitch lock).
type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadOperatingWindowsTx reads a pitch's raw weekly windows ("HH:MM" wall-clock).
// It is the single read used by BOTH the availability resolution and the
// write-path gate, so the two paths can never diverge on what the schedule is.
func loadOperatingWindowsTx(ctx context.Context, q rowQuerier, pitchID int64) ([]data.OperatingWindow, error) {
	rows, err := q.Query(ctx, `
		SELECT weekday, to_char(open_time, 'HH24:MI'), to_char(close_time, 'HH24:MI')
		FROM operating_hours
		WHERE pitch_id = $1
	`, pitchID)
	if err != nil {
		return nil, fmt.Errorf("loadOperatingWindows: query: %w", err)
	}
	defer rows.Close()

	var windows []data.OperatingWindow
	for rows.Next() {
		var w data.OperatingWindow
		if err := rows.Scan(&w.Weekday, &w.OpenTime, &w.CloseTime); err != nil {
			return nil, fmt.Errorf("loadOperatingWindows: scan: %w", err)
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

// GetOpenWindows resolves the pitch's schedule to concrete UTC open intervals for
// the Amman calendar date `date` (its Y/M/D are read in Amman civil terms). See
// the interface doc for the hasSchedule contract (fail-open on unconfigured).
func (r *bookingRepo) GetOpenWindows(ctx context.Context, pitchID int, date time.Time) ([]data.ConcreteInterval, bool, error) {
	windows, err := loadOperatingWindowsTx(ctx, r.db, int64(pitchID))
	if err != nil {
		return nil, false, err
	}
	if len(windows) == 0 {
		return nil, false, nil // unconfigured → open 24/7
	}
	resolved, err := data.ResolveWindowsForDate(windows, date)
	if err != nil {
		return nil, true, fmt.Errorf("GetOpenWindows: resolve: %w", err)
	}
	return resolved, true, nil
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

func (r *bookingRepo) GetAllBookings(ctx context.Context, actor auth.Actor) ([]models.AdminBooking, error) {
	// Owner scoping is the pitches join: owners get an extra ownership predicate,
	// admins get none (every booking). Booking history is preserved across pitch
	// soft-deletion, so no deleted_at filter is applied here.
	pitchJoin := "INNER JOIN pitches p ON p.id = b.pitch_id"
	args := []any{}
	if !actor.IsAdmin() {
		args = append(args, actor.UserID)
		pitchJoin = fmt.Sprintf("INNER JOIN pitches p ON p.id = b.pitch_id AND p.owner_id = $%d", len(args))
	}

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			b.id,
			b.pitch_id,    COALESCE(p.name,      '') AS pitch_name,
			b.player_id,   COALESCE(u.full_name,  '') AS user_name,
			               COALESCE(u.email,      '') AS user_email,
			               COALESCE(u.phone,      '') AS user_phone,
			lower(b.booking_range) AS start_time, upper(b.booking_range) AS end_time,
			b.status, b.total_price, b.created_at
		FROM bookings b
		%s
		LEFT JOIN  users   u ON u.id = b.player_id
		ORDER BY b.created_at DESC
	`, pitchJoin), args...)
	if err != nil {
		return nil, fmt.Errorf("GetAllBookings: query: %w", err)
	}
	defer rows.Close()

	bookings := make([]models.AdminBooking, 0)
	for rows.Next() {
		var b models.AdminBooking
		if err := rows.Scan(
			&b.ID, &b.PitchID, &b.PitchName,
			&b.PlayerID, &b.UserName, &b.UserEmail, &b.UserPhone,
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

	// ── Ownership-scoped resolve + lock ──────────────────────────────────────
	// The target booking is resolved (and row-locked) WITH the actor's ownership
	// predicate so a caller can never act on a booking outside their scope:
	//   - admin  → any booking (no predicate)
	//   - owner  → only bookings whose pitch they own (pitches.owner_id)
	//   - player → only their own bookings (bookings.player_id)
	// A non-matching booking yields pgx.ErrNoRows → ErrBookingNotFound → 404, so
	// existence of another owner's/player's booking is never leaked (no 403). The
	// lock (FOR UPDATE OF b) serialises concurrent cancels, keeping the operation
	// idempotent-safe. Because resolution fails BEFORE any mutation, a rejected
	// cancel performs zero side effects (no slot release, no audit row; the
	// notification is dispatched by the service only after this returns success).
	pitchJoin := "JOIN pitches pt ON pt.id = b.pitch_id"
	where := "b.id = $1"
	args := []any{p.BookingID}

	switch p.ActorRole {
	case ActorAdmin, ActorSystem:
		// Unscoped: privileged actors may cancel any booking.
	case ActorOwner:
		if p.ActorID == nil {
			return nil, ErrBookingNotFound
		}
		args = append(args, *p.ActorID)
		where += fmt.Sprintf(" AND pt.owner_id = $%d", len(args))
	case ActorPlayer:
		if p.ActorID == nil {
			return nil, ErrBookingNotFound
		}
		args = append(args, *p.ActorID)
		where += fmt.Sprintf(" AND b.player_id = $%d", len(args))
	default:
		// Unknown/empty role is categorically unscopable — deny without leaking.
		return nil, ErrBookingNotFound
	}

	var currentStatus string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT b.status
		FROM bookings b
		%s
		WHERE %s
		FOR UPDATE OF b
	`, pitchJoin, where), args...).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound // not found OR not in the actor's scope
		}
		return nil, fmt.Errorf("CancelBooking: resolve: %w", err)
	}

	// State-machine guard: only confirmed → cancelled is permitted (instant
	// booking has no pending state). An already-cancelled or otherwise
	// non-cancellable booking returns 409 with no side effects (idempotent-safe).
	if currentStatus != string(models.StatusConfirmed) {
		return nil, ErrInvalidStatusTransition
	}

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
		// The row is locked and was confirmed a statement ago, so a miss here is a
		// genuine error rather than a concurrency race.
		return nil, fmt.Errorf("CancelBooking: update: %w", err)
	}

	// Audit the confirmed → cancelled transition with the ACTUAL canceller. actor_id
	// is NULL only for a system action (ActorID nil); actor_role and reason capture
	// who acted and why.
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