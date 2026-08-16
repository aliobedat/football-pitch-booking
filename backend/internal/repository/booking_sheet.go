package repository

// Booking Sheet backend (WO-BOOKING-SHEET / PR-A). Two owner/staff operations on
// an existing booking: extend its end time (owner/admin) and track partial cash
// payment (owner/admin/staff). Money is NUMERIC in SQL, cast to float8 only at
// the SELECT boundary and round3'd here — never accumulated as Go float.
//
// amount_paid (migration 032) is the source of truth for cash collected;
// payment_status stays a synced legacy field for the frozen collected-cash
// consumers (analytics/net-profit/reports), written atomically alongside it.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ── Sentinels (mapped to HTTP by the handlers) ───────────────────────────────
var (
	// ErrSheetNotFound / ErrSheetNotInScope — the booking is unknown or outside the
	// caller's scope. Both surface as 404 (existence not leaked), matching Day View.
	ErrSheetNotFound   = errors.New("sheet: booking not found or not in scope")
	ErrSheetNotInScope = errors.New("sheet: booking not in caller scope")
	// ErrSheetBlock — source='block'; blocks are not revenue bookings. → 409 not_a_booking.
	ErrSheetBlock = errors.New("sheet: block rows are not bookings")
	// ErrSheetCancelled — the booking is cancelled. → 409 booking_cancelled.
	ErrSheetCancelled = errors.New("sheet: booking is cancelled")
	// ErrSheetEnded — the booking already ended (upper < now). → 400 booking_ended.
	ErrSheetEnded = errors.New("sheet: booking already ended")
	// ErrSheetConflict — the extended range overlaps another non-cancelled booking
	// (EXCLUDE 23P01). → 409 slot_conflict.
	ErrSheetConflict = errors.New("sheet: extended slot conflicts with an existing reservation")
	// ErrSheetPaidExceedsTotal — amount_paid > effective total. → 422 paid_exceeds_total.
	ErrSheetPaidExceedsTotal = errors.New("sheet: amount_paid exceeds total_price")
)

// round3 (fils rounding) is shared from expense_repository.go — the ONLY place
// booking-sheet money is rounded in Go; SQL does the arithmetic.

// moneyEqual reports whether two JOD amounts are equal at 3-dp resolution.
func moneyEqual(a, b float64) bool { return math.Abs(round3(a)-round3(b)) < 0.0005 }

// BookingSheet is the response shape shared by both endpoints: the booking's
// core fields plus the money state. amount_paid / remaining are nullable
// (null = untracked). payment_display is DERIVED, never stored.
type BookingSheet struct {
	ID             int64     `json:"id"`
	PitchID        int64     `json:"pitch_id"`
	PitchName      string    `json:"pitch_name"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	TotalPrice     float64   `json:"total_price"`
	AmountPaid     *float64  `json:"amount_paid"`     // null = untracked
	PaymentStatus  string    `json:"payment_status"`  // legacy synced field
	PaymentDisplay string    `json:"payment_display"` // derived: untracked|unpaid|partial|paid
	Remaining      *float64  `json:"remaining"`       // derived; null when untracked
}

// derivePayment computes the display state + remaining from the total and the
// (nullable) amount_paid. NULL → untracked; 0 → unpaid; 0<x<total → partial;
// x>=total → paid.
func derivePayment(total float64, amountPaid *float64) (display string, remaining *float64) {
	if amountPaid == nil {
		return "untracked", nil
	}
	paid := round3(*amountPaid)
	rem := round3(total - paid)
	switch {
	case paid <= 0:
		return "unpaid", &rem
	case moneyEqual(paid, total) || paid > total:
		zero := 0.0
		return "paid", &zero
	default:
		return "partial", &rem
	}
}

// withDerived fills PaymentDisplay/Remaining on a sheet whose TotalPrice and
// AmountPaid are already set.
func (s *BookingSheet) withDerived() *BookingSheet {
	s.PaymentDisplay, s.Remaining = derivePayment(s.TotalPrice, s.AmountPaid)
	return s
}

// ── Extend repository ────────────────────────────────────────────────────────

// extendTarget is the pre-write snapshot the handler validates (block / cancelled
// / ended / operating-hours) before the atomic UPDATE.
type extendTarget struct {
	PitchID int64
	Source  string
	Status  string
	Start   time.Time // lower(booking_range)
	End     time.Time // upper(booking_range)
}

// BookingSheetRepository owns the extend read + write. Payment lives on the
// existing ScheduleRepository (staff-aware scope) per the Gate-2 amendment.
type BookingSheetRepository interface {
	// LoadExtendTarget resolves the booking under OWNER scope (admin unscoped) for
	// the extend pre-checks. Unknown / not-owned / soft-deleted pitch → ErrSheetNotFound.
	LoadExtendTarget(ctx context.Context, actor auth.Actor, bookingID int64) (*extendTarget, error)

	// ApplyExtend grows booking_range by `minutes` and adds the SQL-computed
	// additive price delta, in one atomic owner-scoped UPDATE. The guards
	// (source<>block, status<>cancelled, upper>=now, owner scope) live in the WHERE
	// so a raced state change yields no row → ErrSheetNotFound. A GIST EXCLUDE
	// violation (23P01) → ErrSheetConflict. amount_paid / payment_status untouched.
	ApplyExtend(ctx context.Context, actor auth.Actor, bookingID int64, minutes int) (*BookingSheet, error)

	// LoadContact reads the editable-contact fields (WO-BOOKING-EDIT-CONTACT). The
	// WHERE clause is byte-identical in shape to ApplyContact's so read and write
	// scope can never drift apart. source not in (manual, academy), cancelled, or
	// out of owner scope → ErrSheetNotFound (existence not leaked).
	LoadContact(ctx context.Context, actor auth.Actor, bookingID int64) (*BookingContactFields, error)

	// ApplyContact corrects guest_name and/or guest_phone on an existing
	// manual/academy booking, in one atomic owner-scoped UPDATE. Either field may
	// be nil (COALESCE keeps the existing value). No range, price, status, or
	// attendance is touched. pgx.ErrNoRows → ErrSheetNotFound.
	ApplyContact(ctx context.Context, actor auth.Actor, bookingID int64, guestName, guestPhone *string) (*BookingContactFields, error)

	// ApplySeriesContact corrects guest_name and/or guest_phone across every
	// active manual/academy member of bookingID's recurrence_group_id, in one
	// atomic owner-scoped UPDATE (WO-CONTACT-SERIES). bookingID's own group id
	// is resolved in a subquery — bookingID itself is NOT re-validated for
	// scope/source/status here beyond that subquery (the handler's pre-read via
	// LoadContact already did that, and remains the source of the 422
	// not_recurring distinction: this method's WHERE clause cannot tell "no
	// such booking" apart from "booking has no group," both yield zero rows).
	// Cancelled/foreign-owner/non-manual-academy members are silently excluded
	// from the update, not erred. Returns the ids actually touched.
	ApplySeriesContact(ctx context.Context, actor auth.Actor, bookingID int64, guestName, guestPhone *string) ([]int64, error)

	// LoadRescheduleTarget resolves the booking under OWNER scope for the
	// reschedule pre-checks (recurrence; the candidate range for the hours gate).
	// Unknown / not-owned / soft-deleted pitch → ErrSheetNotFound.
	LoadRescheduleTarget(ctx context.Context, actor auth.Actor, bookingID int64) (*rescheduleTarget, error)

	// ResolveOwnedPitch confirms pitchID is an active, non-deleted pitch in the
	// actor's scope — used to validate the RESCHEDULE TARGET pitch before it is
	// handed to the operating-hours resolver (never let an unscoped pitch id
	// become an hours-configuration oracle). Not found / not owned → ErrSheetNotFound.
	ResolveOwnedPitch(ctx context.Context, actor auth.Actor, pitchID int64) error

	// ApplyReschedule moves booking_range to start at newStart (duration
	// preserved) and/or moves pitch_id to newPitchID, in one atomic owner-scoped
	// UPDATE. The guards (source<>block, status<>cancelled, recurrence_group_id
	// IS NULL, not-yet-started, new start not in the past, target pitch shares
	// the current owner and is active, owner scope) all live in the WHERE — a
	// raced state change yields no row → ErrSheetNotFound. The recurrence guard
	// is query-layer here (not just the handler's pre-read 422): a caller that
	// somehow reaches this method directly on a recurring row still gets a
	// hard ErrSheetNotFound, never a silent move. A GIST EXCLUDE violation
	// (23P01) → ErrSheetConflict. total_price / amount_paid untouched
	// (WO-BOOKING-RESCHEDULE: same duration only — a price differs-by-pitch
	// delta is explicitly out of scope, unlike ApplyExtend's additive delta).
	ApplyReschedule(ctx context.Context, actor auth.Actor, bookingID int64, newStart time.Time, newPitchID int64) (*BookingSheet, error)
}

// rescheduleTarget is the pre-write snapshot the handler validates (recurrence;
// the candidate range for the operating-hours gate) before the atomic UPDATE.
type rescheduleTarget struct {
	PitchID           int64
	Start             time.Time // lower(booking_range)
	End               time.Time // upper(booking_range)
	RecurrenceGroupID *string
}

// BookingContactFields is the read shape for GET /bookings/:id/contact.
type BookingContactFields struct {
	GuestName          string  `json:"guest_name"`
	GuestPhone         string  `json:"guest_phone"`
	Source             string  `json:"source"`
	RecurrenceGroupID  *string `json:"recurrence_group_id"`
}

type bookingSheetRepo struct {
	db *pgxpool.Pool
}

// NewBookingSheetRepository constructs a Postgres-backed BookingSheetRepository.
func NewBookingSheetRepository(db *pgxpool.Pool) BookingSheetRepository {
	return &bookingSheetRepo{db: db}
}

func (r *bookingSheetRepo) LoadExtendTarget(ctx context.Context, actor auth.Actor, bookingID int64) (*extendTarget, error) {
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 2) // $1 = bookingID
	args := append([]any{bookingID}, ownerArgs...)

	var t extendTarget
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT b.pitch_id, b.source, b.status,
		       lower(b.booking_range), upper(b.booking_range)
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.id = $1
		  AND p.deleted_at IS NULL
		  AND %s
	`, ownerClause), args...).Scan(&t.PitchID, &t.Source, &t.Status, &t.Start, &t.End)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("LoadExtendTarget: %w", err)
	}
	return &t, nil
}

func (r *bookingSheetRepo) ApplyExtend(ctx context.Context, actor auth.Actor, bookingID int64, minutes int) (*BookingSheet, error) {
	// $1 = bookingID, $2 = minutes; owner predicate (if any) starts at $3.
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 3)
	args := append([]any{bookingID, minutes}, ownerArgs...)

	var s BookingSheet
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE bookings b
		SET booking_range = tstzrange(lower(b.booking_range),
		                              upper(b.booking_range) + make_interval(mins => $2),
		                              '[)'),
		    total_price   = b.total_price
		                    + round((p.price_per_hour::numeric * $2) / 60.0, 3),
		    updated_at    = now()
		FROM pitches p
		WHERE b.id = $1
		  AND p.id = b.pitch_id
		  AND b.source <> 'block'
		  AND b.status <> 'cancelled'
		  AND upper(b.booking_range) >= now()
		  AND %s
		RETURNING b.id, b.pitch_id, p.name,
		          lower(b.booking_range), upper(b.booking_range),
		          b.source, b.status,
		          b.total_price::float8, b.amount_paid::float8, b.payment_status
	`, ownerClause), args...).Scan(
		&s.ID, &s.PitchID, &s.PitchName, &s.StartTime, &s.EndTime,
		&s.Source, &s.Status, &s.TotalPrice, &s.AmountPaid, &s.PaymentStatus,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgExclusionViolation {
			return nil, ErrSheetConflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// Guards in the WHERE matched no row — a state raced between load and
			// apply (cancelled / ended / scope). Fail closed as not-found.
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("ApplyExtend: %w", err)
	}
	s.TotalPrice = round3(s.TotalPrice)
	if s.AmountPaid != nil {
		v := round3(*s.AmountPaid)
		s.AmountPaid = &v
	}
	return s.withDerived(), nil
}

// ── Contact repository (WO-BOOKING-EDIT-CONTACT) ─────────────────────────────

func (r *bookingSheetRepo) LoadContact(ctx context.Context, actor auth.Actor, bookingID int64) (*BookingContactFields, error) {
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 2) // $1 = bookingID
	args := append([]any{bookingID}, ownerArgs...)

	var ct BookingContactFields
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(b.guest_name,''), COALESCE(b.guest_phone,''), b.source, b.recurrence_group_id
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.id = $1
		  AND b.source IN ('manual','academy')
		  AND b.status <> 'cancelled'
		  AND %s
	`, ownerClause), args...).Scan(&ct.GuestName, &ct.GuestPhone, &ct.Source, &ct.RecurrenceGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("LoadContact: %w", err)
	}
	return &ct, nil
}

func (r *bookingSheetRepo) ApplyContact(ctx context.Context, actor auth.Actor, bookingID int64, guestName, guestPhone *string) (*BookingContactFields, error) {
	// $1 = bookingID, $2 = guestName, $3 = guestPhone; owner predicate starts at $4.
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 4)
	args := append([]any{bookingID, guestName, guestPhone}, ownerArgs...)

	var ct BookingContactFields
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE bookings b
		SET guest_name  = COALESCE($2, b.guest_name),
		    guest_phone = COALESCE($3, b.guest_phone),
		    updated_at  = now()
		FROM pitches p
		WHERE b.id = $1
		  AND p.id = b.pitch_id
		  AND b.source IN ('manual','academy')
		  AND b.status <> 'cancelled'
		  AND %s
		RETURNING COALESCE(b.guest_name,''), COALESCE(b.guest_phone,''), b.source, b.recurrence_group_id
	`, ownerClause), args...).Scan(&ct.GuestName, &ct.GuestPhone, &ct.Source, &ct.RecurrenceGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("ApplyContact: %w", err)
	}
	return &ct, nil
}

func (r *bookingSheetRepo) ApplySeriesContact(ctx context.Context, actor auth.Actor, bookingID int64, guestName, guestPhone *string) ([]int64, error) {
	// $1 = bookingID, $2 = guestName, $3 = guestPhone; owner predicate starts at $4.
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 4)
	args := append([]any{bookingID, guestName, guestPhone}, ownerArgs...)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		UPDATE bookings b
		SET guest_name  = COALESCE($2, b.guest_name),
		    guest_phone = COALESCE($3, b.guest_phone),
		    updated_at  = now()
		FROM pitches p
		WHERE b.recurrence_group_id = (
		        SELECT recurrence_group_id FROM bookings WHERE id = $1
		      )
		  AND b.recurrence_group_id IS NOT NULL
		  AND p.id = b.pitch_id
		  AND b.source IN ('manual','academy')
		  AND b.status <> 'cancelled'
		  AND %s
		RETURNING b.id
	`, ownerClause), args...)
	if err != nil {
		return nil, fmt.Errorf("ApplySeriesContact: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ApplySeriesContact: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ApplySeriesContact: %w", err)
	}
	return ids, nil
}

// ── Reschedule repository (WO-BOOKING-RESCHEDULE) ────────────────────────────

func (r *bookingSheetRepo) LoadRescheduleTarget(ctx context.Context, actor auth.Actor, bookingID int64) (*rescheduleTarget, error) {
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 2) // $1 = bookingID
	args := append([]any{bookingID}, ownerArgs...)

	var t rescheduleTarget
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT b.pitch_id, lower(b.booking_range), upper(b.booking_range), b.recurrence_group_id
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.id = $1
		  AND p.deleted_at IS NULL
		  AND %s
	`, ownerClause), args...).Scan(&t.PitchID, &t.Start, &t.End, &t.RecurrenceGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("LoadRescheduleTarget: %w", err)
	}
	return &t, nil
}

func (r *bookingSheetRepo) ResolveOwnedPitch(ctx context.Context, actor auth.Actor, pitchID int64) error {
	ownerClause, ownerArgs := actor.OwnerScopeFilter("owner_id", 2) // $1 = pitchID
	args := append([]any{pitchID}, ownerArgs...)

	var id int64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM pitches
		WHERE id = $1 AND is_active AND deleted_at IS NULL AND %s
	`, ownerClause), args...).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSheetNotFound
		}
		return fmt.Errorf("ResolveOwnedPitch: %w", err)
	}
	return nil
}

func (r *bookingSheetRepo) ApplyReschedule(ctx context.Context, actor auth.Actor, bookingID int64, newStart time.Time, newPitchID int64) (*BookingSheet, error) {
	// $1 = bookingID, $2 = newStart, $3 = newPitchID; owner predicate starts at $4.
	ownerClause, ownerArgs := actor.OwnerScopeFilter("p.owner_id", 4)
	args := append([]any{bookingID, newStart, newPitchID}, ownerArgs...)

	var s BookingSheet
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE bookings b
		SET booking_range = tstzrange($2, $2 + (upper(b.booking_range) - lower(b.booking_range)), '[)'),
		    pitch_id      = $3,
		    updated_at    = now()
		FROM pitches p, pitches np
		WHERE b.id = $1
		  AND p.id = b.pitch_id
		  AND np.id = $3
		  AND np.owner_id = p.owner_id
		  AND np.is_active AND np.deleted_at IS NULL
		  AND b.source <> 'block'
		  AND b.status <> 'cancelled'
		  AND b.recurrence_group_id IS NULL
		  AND lower(b.booking_range) >= now()
		  AND $2 >= now()
		  AND %s
		RETURNING b.id, b.pitch_id, np.name,
		          lower(b.booking_range), upper(b.booking_range),
		          b.source, b.status,
		          b.total_price::float8, b.amount_paid::float8, b.payment_status
	`, ownerClause), args...).Scan(
		&s.ID, &s.PitchID, &s.PitchName, &s.StartTime, &s.EndTime,
		&s.Source, &s.Status, &s.TotalPrice, &s.AmountPaid, &s.PaymentStatus,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgExclusionViolation {
			return nil, ErrSheetConflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// Guards in the WHERE matched no row — a state raced between load and
			// apply (cancelled / started / target pitch scope). Fail closed as
			// not-found, same as ApplyExtend.
			return nil, ErrSheetNotFound
		}
		return nil, fmt.Errorf("ApplyReschedule: %w", err)
	}
	s.TotalPrice = round3(s.TotalPrice)
	if s.AmountPaid != nil {
		v := round3(*s.AmountPaid)
		s.AmountPaid = &v
	}
	return s.withDerived(), nil
}
