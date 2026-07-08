package repository

// ScheduleRepository backs the Dashboard PR 4 staff view: the daily occupancy
// schedule for an in-scope pitch and the check-in / no-show attendance toggle.
// Scope is enforced in SQL (staff → bound pitch, owner → owned, admin → any).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ErrBookingNotInScope — the booking does not exist or its pitch is outside the
// caller's scope. Mapped to 403 (existence is not leaked vs 404).
var ErrBookingNotInScope = errors.New("schedule: booking not in caller scope")

// Valid attendance values (mirrors the migration 022 CHECK).
var validAttendance = map[string]bool{"pending": true, "checked_in": true, "no_show": true}

// IsValidAttendance reports whether s is an accepted attendance value.
func IsValidAttendance(s string) bool { return validAttendance[s] }

// Valid payment-settlement values (mirrors the payment_status enum after
// migration 024). Cash-native: a booking is either unpaid or settled in cash.
var validPayment = map[string]bool{"unpaid": true, "paid_cash": true}

// IsValidPayment reports whether s is an accepted payment_status value.
func IsValidPayment(s string) bool { return validPayment[s] }

// ScheduleRow is one occupancy line in the daily schedule.
type ScheduleRow struct {
	ID            int64     `json:"id"`
	PitchID       int64     `json:"pitch_id"`
	PitchName     string    `json:"pitch_name"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Source        string    `json:"source"` // player | manual | block
	Status        string    `json:"status"`
	Attendance    string    `json:"attendance"`     // pending | checked_in | no_show
	PaymentStatus string    `json:"payment_status"` // unpaid | paid_cash
	AttendeeName  string    `json:"attendee_name"`

	// WO-BOOKING-SHEET / PR-B.2a — additive money state so جدول اليوم can open the
	// booking sheet without a second fetch. Derived via the booking_sheet helpers;
	// PricePerHour is per-row (the schedule spans pitches with different rates).
	TotalPrice     float64  `json:"total_price"`
	AmountPaid     *float64 `json:"amount_paid"`     // null = untracked
	PaymentDisplay string   `json:"payment_display"` // derived: untracked|unpaid|partial|paid
	Remaining      *float64 `json:"remaining"`       // derived; null when untracked
	PricePerHour   int      `json:"price_per_hour"`  // whole-JOD hourly rate of the row's pitch
}

// ScheduleRepository reads the daily schedule and writes attendance.
type ScheduleRepository interface {
	// DailySchedule returns non-cancelled occupancy whose start falls in
	// [fromUTC, toUTC), for the caller's scope, ordered by start time. pitchFilter
	// > 0 narrows to one pitch (must still be in scope).
	DailySchedule(ctx context.Context, actor auth.Actor, boundPitchIDs []int, pitchFilter int, fromUTC, toUTC time.Time) ([]ScheduleRow, error)

	// SetAttendance sets bookings.attendance for bookingID iff its pitch is in the
	// caller's scope; otherwise ErrBookingNotInScope. Idempotent.
	SetAttendance(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, attendance string) (*ScheduleRow, error)

	// SetPayment sets bookings.payment_status (Cash-Settlement Marker, WO-F1) for a
	// NON-CANCELLED booking iff its pitch is in the caller's scope; otherwise
	// ErrBookingNotInScope. Settable on any non-cancelled booking (cash may arrive
	// before or after play). Idempotent. Retained for the dbadmin reconciliation
	// probe; the HTTP path uses ApplyPayment.
	SetPayment(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, payment string) (*ScheduleRow, error)

	// ApplyPayment is the WO-BOOKING-SHEET unified payment write: legacy
	// payment_status toggle OR the new amount_paid form, keeping payment_status in
	// sync (the bridge) — all atomic. Staff-aware scope (BoundPitchIDs); out-of-
	// scope/unknown → ErrSheetNotInScope (404), block → ErrSheetBlock (409),
	// cancelled → ErrSheetCancelled (409), amount_paid > total → ErrSheetPaidExceedsTotal
	// (422). Returns the full BookingSheet with derived display state.
	ApplyPayment(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, intent PaymentIntent) (*BookingSheet, error)
}

// PaymentIntent is the resolved payment write the handler hands the repository,
// after it has parsed the request body form and enforced the staff carve-out.
// Exactly one Mode is set.
type PaymentIntent struct {
	Mode string // "legacy_paid" | "legacy_unpaid" | "new"

	// New-form fields (Mode == "new"). Each *Provided flag distinguishes an absent
	// key from an explicit value (incl. explicit null for AmountPaid).
	AmountPaidProvided bool
	AmountPaid         *float64 // nil with Provided=true → explicit untracked (NULL)
	TotalPriceProvided bool
	TotalPrice         float64 // already round3-normalised by the handler
}

type scheduleRepo struct {
	db *pgxpool.Pool
}

// NewScheduleRepository constructs a Postgres-backed ScheduleRepository.
func NewScheduleRepository(db *pgxpool.Pool) ScheduleRepository {
	return &scheduleRepo{db: db}
}

// scopePredicate returns a SQL fragment + args restricting bookings to the
// caller's scope: staff → ANY of their bound pitches (1:N); owner → owned pitches;
// admin → all.
func scopePredicate(actor auth.Actor, boundPitchIDs []int, startIdx int) (string, []any) {
	switch actor.Role {
	case auth.RoleStaff:
		// = ANY($n) over the staff member's full pitch set. An empty set never
		// reaches here (ResolveScope 403s unbound staff), but ANY(empty) also
		// matches nothing — fail-closed either way.
		return fmt.Sprintf("b.pitch_id = ANY($%d)", startIdx), []any{boundPitchIDs}
	default:
		// owner → "p.owner_id = $n"; admin → "TRUE".
		clause, args := actor.OwnerScopeFilter("p.owner_id", startIdx)
		return clause, args
	}
}

// attendeeNameExpr: player full name → guest name → a block label.
const attendeeNameExpr = `COALESCE(NULLIF(u.full_name, ''), b.guest_name, CASE WHEN b.source = 'block' THEN 'فترة محجوبة' ELSE '' END)`

func (r *scheduleRepo) DailySchedule(ctx context.Context, actor auth.Actor, boundPitchIDs []int, pitchFilter int, fromUTC, toUTC time.Time) ([]ScheduleRow, error) {
	scopeSQL, args := scopePredicate(actor, boundPitchIDs, 3) // $1,$2 are the time bounds
	q := fmt.Sprintf(`
		SELECT b.id, b.pitch_id, p.name,
		       lower(b.booking_range), upper(b.booking_range),
		       b.source, b.status, b.attendance, b.payment_status,
		       %s,
		       b.total_price::float8, b.amount_paid::float8, p.price_per_hour
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.status <> 'cancelled'
		  AND lower(b.booking_range) >= $1 AND lower(b.booking_range) < $2
		  AND %s`, attendeeNameExpr, scopeSQL)
	allArgs := append([]any{fromUTC, toUTC}, args...)
	if pitchFilter > 0 {
		allArgs = append(allArgs, pitchFilter)
		q += fmt.Sprintf(" AND b.pitch_id = $%d", len(allArgs))
	}
	q += " ORDER BY lower(b.booking_range)"

	rows, err := r.db.Query(ctx, q, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("DailySchedule: %w", err)
	}
	defer rows.Close()

	out := []ScheduleRow{}
	for rows.Next() {
		var s ScheduleRow
		if err := rows.Scan(&s.ID, &s.PitchID, &s.PitchName, &s.StartTime, &s.EndTime,
			&s.Source, &s.Status, &s.Attendance, &s.PaymentStatus, &s.AttendeeName,
			&s.TotalPrice, &s.AmountPaid, &s.PricePerHour); err != nil {
			return nil, fmt.Errorf("DailySchedule: scan: %w", err)
		}
		// Same 3-dp normalisation + derivation as the booking-sheet endpoints, so
		// the row state and a subsequent PATCH response can never disagree.
		s.TotalPrice = round3(s.TotalPrice)
		if s.AmountPaid != nil {
			v := round3(*s.AmountPaid)
			s.AmountPaid = &v
		}
		s.PaymentDisplay, s.Remaining = derivePayment(s.TotalPrice, s.AmountPaid)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *scheduleRepo) SetAttendance(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, attendance string) (*ScheduleRow, error) {
	scopeSQL, args := scopePredicate(actor, boundPitchIDs, 3) // $1=bookingID, $2=attendance
	// Scope is evaluated against the pitches join; an out-of-scope (or missing)
	// booking matches no row → ErrBookingNotInScope. No notification/block/penalty
	// side effects — this UPDATE sets attendance and nothing else (data-only).
	q := fmt.Sprintf(`
		UPDATE bookings b
		SET attendance = $2
		FROM pitches p
		WHERE p.id = b.pitch_id
		  AND b.id = $1
		  AND b.status <> 'cancelled'
		  AND %s
		RETURNING b.id, b.pitch_id, p.name,
		          lower(b.booking_range), upper(b.booking_range),
		          b.source, b.status, b.attendance, b.payment_status,
		          (SELECT %s FROM bookings b2 LEFT JOIN users u ON u.id = b2.player_id WHERE b2.id = b.id)`,
		scopeSQL, attendeeNameExpr)
	allArgs := append([]any{bookingID, attendance}, args...)

	var s ScheduleRow
	err := r.db.QueryRow(ctx, q, allArgs...).Scan(&s.ID, &s.PitchID, &s.PitchName,
		&s.StartTime, &s.EndTime, &s.Source, &s.Status, &s.Attendance, &s.PaymentStatus, &s.AttendeeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBookingNotInScope
	}
	if err != nil {
		return nil, fmt.Errorf("SetAttendance: %w", err)
	}
	return &s, nil
}

// SetPayment sets payment_status for a non-cancelled, in-scope booking. Mirrors
// SetAttendance's scope-as-predicate pattern: an out-of-scope or missing booking
// matches no row → ErrBookingNotInScope. Data-only (no notification/side effects).
func (r *scheduleRepo) SetPayment(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, payment string) (*ScheduleRow, error) {
	scopeSQL, args := scopePredicate(actor, boundPitchIDs, 3) // $1=bookingID, $2=payment
	q := fmt.Sprintf(`
		UPDATE bookings b
		SET payment_status = $2
		FROM pitches p
		WHERE p.id = b.pitch_id
		  AND b.id = $1
		  AND b.status <> 'cancelled'
		  AND %s
		RETURNING b.id, b.pitch_id, p.name,
		          lower(b.booking_range), upper(b.booking_range),
		          b.source, b.status, b.attendance, b.payment_status,
		          (SELECT %s FROM bookings b2 LEFT JOIN users u ON u.id = b2.player_id WHERE b2.id = b.id)`,
		scopeSQL, attendeeNameExpr)
	allArgs := append([]any{bookingID, payment}, args...)

	var s ScheduleRow
	err := r.db.QueryRow(ctx, q, allArgs...).Scan(&s.ID, &s.PitchID, &s.PitchName,
		&s.StartTime, &s.EndTime, &s.Source, &s.Status, &s.Attendance, &s.PaymentStatus, &s.AttendeeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBookingNotInScope
	}
	if err != nil {
		return nil, fmt.Errorf("SetPayment: %w", err)
	}
	return &s, nil
}

// ApplyPayment writes amount_paid + the synced payment_status legacy field in one
// atomic statement, after a locked fetch-within-scope that distinguishes 404 /
// block / cancelled. Staff-aware scope (BoundPitchIDs) preserves the existing
// staff cash toggle; the staff carve-out (no total_price) is enforced in the
// handler before this is called.
func (r *scheduleRepo) ApplyPayment(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, intent PaymentIntent) (*BookingSheet, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("ApplyPayment: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Locked fetch within scope. scopePredicate references b/p, so we JOIN pitches.
	scopeSQL, sargs := scopePredicate(actor, boundPitchIDs, 2) // $1 = bookingID
	var (
		src, status   string
		storedPayStat string
		storedTotal   float64
		storedPaid    *float64
	)
	q := fmt.Sprintf(`
		SELECT b.source, b.status, b.payment_status, b.total_price::float8, b.amount_paid::float8
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.id = $1 AND %s
		FOR UPDATE OF b`, scopeSQL)
	err = tx.QueryRow(ctx, q, append([]any{bookingID}, sargs...)...).
		Scan(&src, &status, &storedPayStat, &storedTotal, &storedPaid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSheetNotInScope
	}
	if err != nil {
		return nil, fmt.Errorf("ApplyPayment: fetch: %w", err)
	}
	if src == "block" {
		return nil, ErrSheetBlock
	}
	if status == "cancelled" {
		return nil, ErrSheetCancelled
	}

	// Resolve the new (amount_paid, total_price, payment_status) triple.
	newTotal := round3(storedTotal)
	var newPaid *float64
	var newStatus string

	switch intent.Mode {
	case "legacy_paid":
		p := newTotal
		newPaid, newStatus = &p, "paid_cash"
	case "legacy_unpaid":
		newPaid, newStatus = nil, "unpaid"
	default: // "new"
		if intent.TotalPriceProvided {
			newTotal = round3(intent.TotalPrice)
		}
		if intent.AmountPaidProvided {
			if intent.AmountPaid == nil {
				newPaid = nil
			} else {
				v := round3(*intent.AmountPaid)
				newPaid = &v
			}
		} else {
			newPaid = storedPaid // unchanged
		}
		if newPaid != nil && (*newPaid > newTotal+0.0005) {
			return nil, ErrSheetPaidExceedsTotal
		}
		switch {
		case newPaid == nil:
			newStatus = storedPayStat // legacy payment_status untouched
		case moneyEqual(*newPaid, newTotal):
			newStatus = "paid_cash"
		default:
			newStatus = "unpaid"
		}
	}

	var out BookingSheet
	err = tx.QueryRow(ctx, `
		UPDATE bookings b
		SET amount_paid    = $2,
		    total_price    = $3,
		    payment_status = $4,
		    updated_at     = now()
		FROM pitches p
		WHERE b.id = $1 AND p.id = b.pitch_id
		RETURNING b.id, b.pitch_id, p.name,
		          lower(b.booking_range), upper(b.booking_range),
		          b.source, b.status,
		          b.total_price::float8, b.amount_paid::float8, b.payment_status`,
		bookingID, newPaid, newTotal, newStatus).Scan(
		&out.ID, &out.PitchID, &out.PitchName, &out.StartTime, &out.EndTime,
		&out.Source, &out.Status, &out.TotalPrice, &out.AmountPaid, &out.PaymentStatus)
	if err != nil {
		return nil, fmt.Errorf("ApplyPayment: update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("ApplyPayment: commit: %w", err)
	}

	out.TotalPrice = round3(out.TotalPrice)
	if out.AmountPaid != nil {
		v := round3(*out.AmountPaid)
		out.AmountPaid = &v
	}
	return out.withDerived(), nil
}
