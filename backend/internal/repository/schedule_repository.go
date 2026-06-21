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
	// before or after play). Idempotent.
	SetPayment(ctx context.Context, actor auth.Actor, boundPitchIDs []int, bookingID int, payment string) (*ScheduleRow, error)
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
		       %s
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
			&s.Source, &s.Status, &s.Attendance, &s.PaymentStatus, &s.AttendeeName); err != nil {
			return nil, fmt.Errorf("DailySchedule: scan: %w", err)
		}
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
