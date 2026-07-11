package repository

// DayViewRepository backs the single-pitch Day View timeline (PR-1). Unlike the
// multi-pitch Visual Calendar (calendar_repository.go), this endpoint answers "for
// ONE pitch on ONE Amman day, what does every 30-minute cell look like?" — a
// discrete slot grid (available | booked | blocked | closed) plus a day summary.
//
// The 30-minute cell is a RENDERING convention only: the schedule model stores no
// slot length (operating_hours are open/close windows; bookings are arbitrary
// tstzrange). Slot classification is derived here from the SAME two sources the
// booking write-path trusts — the operating-hours resolver and the non-cancelled
// occupancy rows — so the grid can never advertise a slot the engine wouldn't sell.
//
// Owner-scoped via the canonical OwnerScopeFilter primitive (owner → own pitch;
// admin → any). A cross-owner / unknown / soft-deleted pitch collapses to
// ErrPitchNotFound → 404, matching the project's established "not found OR not
// owned" convention (never 403, to avoid leaking pitch existence). Read-only.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// SlotMinutes is the fixed Day View cell width. A rendering convention, NOT a
// per-pitch attribute — do not turn this into configurable state (PR-1 guardrail).
const SlotMinutes = 30

// DayViewBookingRef is the occupancy attached to a booked slot. Times are absolute
// UTC; the client converts to Amman. Present ONLY on booked slots.
type DayViewBookingRef struct {
	ID            int64     `json:"id"`
	Source        string    `json:"source"`         // player | manual | academy | block
	Status        string    `json:"status"`         // confirmed | pending
	Attendance    string    `json:"attendance"`     // pending | checked_in | no_show
	PaymentStatus string    `json:"payment_status"` // unpaid | paid_cash (legacy, unchanged)
	Title         string    `json:"title"`          // player name → guest name → block label
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`

	// Booking-sheet money fields (WO-BOOKING-SHEET / PR-A, additive). amount_paid is
	// nullable (null = untracked); payment_display/remaining are DERIVED.
	TotalPrice     float64  `json:"total_price"`
	AmountPaid     *float64 `json:"amount_paid"`
	PaymentDisplay string   `json:"payment_display"` // untracked | unpaid | partial | paid
	Remaining      *float64 `json:"remaining"`       // null when untracked

	// WO-SERIES-CANCEL: recurrence grouping handle so the sheet can detect a
	// series (repeat glyph + cancel-all option). Null for a one-off. Additive.
	RecurrenceGroupID *string `json:"recurrence_group_id"`
}

// DayViewSlot is one 30-minute cell. `partial` is true when a booked cell is only
// partially covered by occupancy (any overlap marks the whole cell booked, per the
// PR-1 ruling; partial records that the coverage is incomplete). Booking is nil
// unless Status == "booked".
type DayViewSlot struct {
	Start   time.Time          `json:"start"`
	End     time.Time          `json:"end"`
	Status  string             `json:"status"` // available | booked | blocked | closed
	Partial bool               `json:"partial"`
	Booking *DayViewBookingRef `json:"booking,omitempty"`
}

// DayViewSummary rolls up the day. BookedHours/AvailableHours are the slot counts
// × 0.5h. ConfirmedRevenue mirrors analytics semantics: SUM(total_price) over
// CONFIRMED bookings whose START falls in the Amman day (blocks contribute 0).
type DayViewSummary struct {
	TotalBookings    int     `json:"total_bookings"`
	BookedSlots      int     `json:"booked_slots"`
	BookedHours      float64 `json:"booked_hours"`
	AvailableSlots   int     `json:"available_slots"`
	AvailableHours   float64 `json:"available_hours"`
	ConfirmedRevenue float64 `json:"confirmed_revenue"`
}

// DayView is the full single-pitch, single-day payload.
type DayView struct {
	PitchID     int64                   `json:"pitch_id"`
	PitchName   string                  `json:"pitch_name"`
	IsActive    bool                    `json:"is_active"`
	Date        string                  `json:"date"`         // YYYY-MM-DD (Amman)
	Timezone    string                  `json:"timezone"`     // "Asia/Amman"
	SlotMinutes int                     `json:"slot_minutes"` // 30
	HasSchedule bool                    `json:"has_schedule"` // false ⇒ open 24/7
	// PricePerHour is the pitch's whole-JOD hourly rate, exposed so the client can
	// project extension prices without a round-trip (WO-BOOKING-SHEET, additive).
	PricePerHour int                     `json:"price_per_hour"`
	OpenWindows  []data.ConcreteInterval `json:"open_windows"`
	Slots        []DayViewSlot           `json:"slots"`
	Summary      DayViewSummary          `json:"summary"`
}

type DayViewRepository interface {
	// OwnerDayView returns the 30-minute grid + summary for `pitchID` on the Amman
	// calendar day of `ammanDate` (only its Y/M/D are read), scoped to `actor`.
	// Returns ErrPitchNotFound when the pitch is unknown, soft-deleted, or not
	// owned by a non-admin actor.
	OwnerDayView(ctx context.Context, actor auth.Actor, pitchID int64, ammanDate time.Time) (*DayView, error)
}

type dayViewRepo struct {
	db *pgxpool.Pool
}

// NewDayViewRepository constructs a Postgres-backed DayViewRepository.
func NewDayViewRepository(db *pgxpool.Pool) DayViewRepository {
	return &dayViewRepo{db: db}
}

// dayBooking is the internal occupancy row (superset of the public ref: carries
// total_price for the revenue roll-up, never serialised).
type dayBooking struct {
	ref        DayViewBookingRef
	totalPrice float64
}

func (r *dayViewRepo) OwnerDayView(ctx context.Context, actor auth.Actor, pitchID int64, ammanDate time.Time) (*DayView, error) {
	// 1. Resolve the pitch under owner scope. pitch_id is $1; the owner predicate
	//    (if any) starts at $2. Admin → "TRUE" (unscoped). Missing/not-owned/
	//    soft-deleted all collapse to ErrPitchNotFound → 404 (existence not leaked).
	args := []any{pitchID}
	ownerClause, ownerArgs := actor.OwnerScopeFilter("owner_id", 2)
	args = append(args, ownerArgs...)

	var (
		name         string
		isActive     bool
		pricePerHour int
	)
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s, is_active, price_per_hour
		FROM pitches p
		WHERE id = $1 AND deleted_at IS NULL AND %s
	`, pitchDisplayNameExpr, ownerClause), args...).Scan(&name, &isActive, &pricePerHour)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPitchNotFound
		}
		return nil, fmt.Errorf("OwnerDayView: resolve pitch: %w", err)
	}

	y, m, d := ammanDate.Date()
	dateStr := fmt.Sprintf("%04d-%02d-%02d", y, m, int(d))
	fromUTC, toUTC := timeutil.AmmanDayBoundsUTC(ammanDate)

	// 2. Resolve the day's open windows (reuse the write-path resolver). No rows ⇒
	//    unconfigured ⇒ open 24/7 (fail-open) — the client must not read [] as closed.
	windows, err := loadOperatingWindowsTx(ctx, r.db, pitchID)
	if err != nil {
		return nil, fmt.Errorf("OwnerDayView: load hours: %w", err)
	}
	hasSchedule := len(windows) > 0
	openWindows := []data.ConcreteInterval{}
	if hasSchedule {
		resolved, err := data.ResolveWindowsForDate(windows, ammanDate)
		if err != nil {
			return nil, fmt.Errorf("OwnerDayView: resolve hours: %w", err)
		}
		if resolved != nil {
			openWindows = resolved
		}
	}

	// 3. The day's non-cancelled occupancy for this pitch — any booking OVERLAPPING
	//    the Amman day (a cross-midnight row that spills into the early hours is
	//    included), ordered by start so slot coverage sweeps are deterministic.
	occ, err := r.loadOccupancy(ctx, pitchID, fromUTC, toUTC)
	if err != nil {
		return nil, err
	}

	// 4. Build the discrete 30-minute grid.
	slots, summary := buildGrid(fromUTC, toUTC, openWindows, hasSchedule, isActive, occ)

	// 5. Summary counters that come from the occupancy set (not the grid):
	//    total_bookings excludes blocks (a maintenance hold is not a booking);
	//    confirmed_revenue = SUM(total_price) over confirmed rows STARTING in the
	//    Amman day (matches analytics bucketing), blocks contribute 0.
	for _, b := range occ {
		if b.ref.Source != "block" {
			summary.TotalBookings++
		}
		startsToday := !b.ref.StartTime.Before(fromUTC) && b.ref.StartTime.Before(toUTC)
		if b.ref.Status == "confirmed" && startsToday {
			summary.ConfirmedRevenue += b.totalPrice
		}
	}

	return &DayView{
		PitchID:     pitchID,
		PitchName:   name,
		IsActive:    isActive,
		Date:        dateStr,
		Timezone:    "Asia/Amman",
		SlotMinutes:  SlotMinutes,
		HasSchedule:  hasSchedule,
		PricePerHour: pricePerHour,
		OpenWindows:  openWindows,
		Slots:        slots,
		Summary:      summary,
	}, nil
}

// loadOccupancy reads this pitch's non-cancelled rows overlapping [fromUTC, toUTC).
func (r *dayViewRepo) loadOccupancy(ctx context.Context, pitchID int64, fromUTC, toUTC time.Time) ([]dayBooking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT b.id, b.source, b.status, b.attendance, b.payment_status,
		       lower(b.booking_range), upper(b.booking_range),
		       b.total_price::float8, b.amount_paid::float8,
		       %s,
		       b.recurrence_group_id
		FROM bookings b
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.pitch_id = $1
		  AND b.status <> 'cancelled'
		  AND b.booking_range && tstzrange($2::timestamptz, $3::timestamptz, '[)')
		ORDER BY lower(b.booking_range)
	`, attendeeNameExpr), pitchID, fromUTC, toUTC)
	if err != nil {
		return nil, fmt.Errorf("OwnerDayView: occupancy query: %w", err)
	}
	defer rows.Close()

	occ := make([]dayBooking, 0)
	for rows.Next() {
		var b dayBooking
		if err := rows.Scan(&b.ref.ID, &b.ref.Source, &b.ref.Status, &b.ref.Attendance,
			&b.ref.PaymentStatus, &b.ref.StartTime, &b.ref.EndTime,
			&b.totalPrice, &b.ref.AmountPaid,
			&b.ref.Title, &b.ref.RecurrenceGroupID); err != nil {
			return nil, fmt.Errorf("OwnerDayView: occupancy scan: %w", err)
		}
		// Additive money fields (WO-BOOKING-SHEET): expose total + derived display.
		b.ref.TotalPrice = round3(b.totalPrice)
		if b.ref.AmountPaid != nil {
			v := round3(*b.ref.AmountPaid)
			b.ref.AmountPaid = &v
		}
		b.ref.PaymentDisplay, b.ref.Remaining = derivePayment(b.ref.TotalPrice, b.ref.AmountPaid)
		occ = append(occ, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("OwnerDayView: occupancy rows: %w", err)
	}
	return occ, nil
}

// buildGrid walks the Amman day in 30-minute cells and classifies each. The GIST
// EXCLUDE constraint guarantees at most one occupancy row per instant, so the
// earliest overlapping row is the cell's representative:
//
//   - booked  — a normal booking (player/manual/academy) overlaps the cell; the
//     row is attached, `partial` set when its coverage of the cell is incomplete.
//   - blocked — an actual owner-created block row (source='block') overlaps the
//     cell; the block row is attached (blocked slots DO carry a booking object).
//   - closed  — an unoccupied cell that cannot be sold or acted on: outside the
//     open windows, or ANY unoccupied cell on an inactive pitch. No booking object.
//   - available — an unoccupied cell inside an open window on an active pitch
//     (has_schedule=false ⇒ open 24/7, so every unoccupied active cell is open).
//
// An inactive pitch therefore returns its occupancy (history/blocks) but zero
// available slots — every unoccupied cell is `closed`, so the surface never
// advertises availability it cannot sell. Only `booked` cells count toward
// booked_slots; block occupancy is surfaced per-slot but is not a "booking".
func buildGrid(
	fromUTC, toUTC time.Time,
	openWindows []data.ConcreteInterval,
	hasSchedule, isActive bool,
	occ []dayBooking,
) ([]DayViewSlot, DayViewSummary) {
	step := time.Duration(SlotMinutes) * time.Minute
	var slots []DayViewSlot
	var summary DayViewSummary

	for s := fromUTC; s.Before(toUTC); s = s.Add(step) {
		e := s.Add(step)
		slot := DayViewSlot{Start: s, End: e}

		// Occupancy overlap: earliest overlapping row is the representative; union
		// coverage decides `partial`.
		var rep *DayViewBookingRef
		coveredUntil := s
		overlapped := false
		for i := range occ {
			b := &occ[i]
			if b.ref.StartTime.Before(e) && s.Before(b.ref.EndTime) { // overlaps cell
				overlapped = true
				if rep == nil {
					rep = &occ[i].ref
				}
				// Extend contiguous coverage from the cell start (rows are start-ordered).
				if !b.ref.StartTime.After(coveredUntil) && b.ref.EndTime.After(coveredUntil) {
					coveredUntil = b.ref.EndTime
				}
			}
		}

		switch {
		case overlapped:
			// A real block row → blocked; any other occupancy → booked. Both keep the
			// booking object; only booked cells count toward booked_slots.
			if rep.Source == "block" {
				slot.Status = "blocked"
			} else {
				slot.Status = "booked"
				summary.BookedSlots++
			}
			slot.Partial = coveredUntil.Before(e)
			slot.Booking = rep
		case !isActive:
			// Inactive pitch: every unoccupied cell is unsellable, no booking object.
			slot.Status = "closed"
		case !hasSchedule || data.SlotContained(s, e, openWindows):
			slot.Status = "available"
			summary.AvailableSlots++
		default:
			// Active pitch, unoccupied, outside operating hours → not sellable.
			slot.Status = "closed"
		}

		slots = append(slots, slot)
	}

	summary.BookedHours = float64(summary.BookedSlots) * float64(SlotMinutes) / 60
	summary.AvailableHours = float64(summary.AvailableSlots) * float64(SlotMinutes) / 60
	return slots, summary
}
