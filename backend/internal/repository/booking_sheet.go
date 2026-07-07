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
