package repository

// CalendarRepository backs the Visual Calendar Command Center (Cockpit WO2): a
// per-day resource timeline — the owner's pitches as ROWS, each with that Amman
// day's resolved operating windows (for windowing/dimming) and its non-cancelled
// occupancy as absolutely-positioned events. Owner-scoped via OwnerScopeFilter
// (owner → own pitches; admin → all). Read-only; reuses the SAME operating-hours
// resolver the booking write-path uses, so the calendar can never show a window
// the engine wouldn't honour.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// CalendarEvent is one occupancy block on a pitch row. Times are absolute UTC; the
// client converts to Amman for positioning. Title resolves player/guest/block.
type CalendarEvent struct {
	ID            int64     `json:"id"`
	PitchID       int64     `json:"pitch_id"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Source        string    `json:"source"`         // player | manual | block | academy
	Status        string    `json:"status"`         // confirmed | pending
	Attendance    string    `json:"attendance"`     // pending | checked_in | no_show
	PaymentStatus string    `json:"payment_status"` // unpaid | paid_cash
	Title         string    `json:"title"`          // player name → guest name → block label
	CustomerID    *int64    `json:"customer_id"`
}

// CalendarPitchRow is one resource row: the pitch, its resolved open windows for
// the day, and its events. HasSchedule=false means the pitch has NO configured
// hours (open 24/7 per the fail-open rule) — the client must not read empty
// OpenWindows as "closed all day".
type CalendarPitchRow struct {
	PitchID     int64                   `json:"pitch_id"`
	PitchName   string                  `json:"pitch_name"`
	IsActive    bool                    `json:"is_active"`
	OpenWindows []data.ConcreteInterval `json:"open_windows"`
	HasSchedule bool                    `json:"has_schedule"`
	Events      []CalendarEvent         `json:"events"`
}

// CalendarDay is the full day payload: the Amman calendar date + one row per
// in-scope pitch (including pitches with zero bookings that day).
type CalendarDay struct {
	Date    string             `json:"date"`
	Pitches []CalendarPitchRow `json:"pitches"`
}

type CalendarRepository interface {
	// OwnerDayCalendar returns the resource-timeline payload for the Amman calendar
	// day of `ammanDate` (only its Y/M/D are read), scoped to the actor.
	OwnerDayCalendar(ctx context.Context, actor auth.Actor, ammanDate time.Time) (*CalendarDay, error)
}

type calendarRepo struct {
	db *pgxpool.Pool
}

func NewCalendarRepository(db *pgxpool.Pool) CalendarRepository {
	return &calendarRepo{db: db}
}

func (r *calendarRepo) OwnerDayCalendar(ctx context.Context, actor auth.Actor, ammanDate time.Time) (*CalendarDay, error) {
	y, m, d := ammanDate.Date()
	dateStr := fmt.Sprintf("%04d-%02d-%02d", y, m, int(d))

	// 1. In-scope, non-deleted pitches (the rows) — including empty ones.
	ownerClause, args := actor.OwnerScopeFilter("owner_id", 1)
	pitchRows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT id, name, is_active
		FROM pitches
		WHERE deleted_at IS NULL AND %s
		ORDER BY name
	`, ownerClause), args...)
	if err != nil {
		return nil, fmt.Errorf("OwnerDayCalendar: pitches: %w", err)
	}
	defer pitchRows.Close()

	rowsByPitch := map[int64]*CalendarPitchRow{}
	order := make([]int64, 0)
	for pitchRows.Next() {
		var p CalendarPitchRow
		if err := pitchRows.Scan(&p.PitchID, &p.PitchName, &p.IsActive); err != nil {
			return nil, fmt.Errorf("OwnerDayCalendar: pitch scan: %w", err)
		}
		p.OpenWindows = []data.ConcreteInterval{}
		p.Events = []CalendarEvent{}
		rowsByPitch[p.PitchID] = &p
		order = append(order, p.PitchID)
	}
	if err := pitchRows.Err(); err != nil {
		return nil, fmt.Errorf("OwnerDayCalendar: pitch rows: %w", err)
	}
	pitchRows.Close()

	// 2. Per-pitch operating windows for the date (reuse the write-path resolver).
	for _, pid := range order {
		windows, err := loadOperatingWindowsTx(ctx, r.db, pid)
		if err != nil {
			return nil, fmt.Errorf("OwnerDayCalendar: load hours pitch %d: %w", pid, err)
		}
		row := rowsByPitch[pid]
		row.HasSchedule = len(windows) > 0
		if row.HasSchedule {
			resolved, err := data.ResolveWindowsForDate(windows, ammanDate)
			if err != nil {
				return nil, fmt.Errorf("OwnerDayCalendar: resolve hours pitch %d: %w", pid, err)
			}
			if resolved != nil {
				row.OpenWindows = resolved
			}
		}
	}

	// 3. The day's non-cancelled occupancy, bucketed into its pitch row.
	fromUTC, toUTC := timeutil.AmmanDayBoundsUTC(ammanDate)
	evClause, evArgs := actor.OwnerScopeFilter("p.owner_id", 3) // $1,$2 = time bounds
	allArgs := append([]any{fromUTC, toUTC}, evArgs...)
	evRows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT b.id, b.pitch_id,
		       lower(b.booking_range), upper(b.booking_range),
		       b.source, b.status, b.attendance, b.payment_status, b.customer_id,
		       %s
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.status <> 'cancelled'
		  AND lower(b.booking_range) >= $1 AND lower(b.booking_range) < $2
		  AND %s
		ORDER BY lower(b.booking_range)
	`, attendeeNameExpr, evClause), allArgs...)
	if err != nil {
		return nil, fmt.Errorf("OwnerDayCalendar: events: %w", err)
	}
	defer evRows.Close()
	for evRows.Next() {
		var e CalendarEvent
		if err := evRows.Scan(&e.ID, &e.PitchID, &e.StartTime, &e.EndTime,
			&e.Source, &e.Status, &e.Attendance, &e.PaymentStatus, &e.CustomerID, &e.Title); err != nil {
			return nil, fmt.Errorf("OwnerDayCalendar: event scan: %w", err)
		}
		if row, ok := rowsByPitch[e.PitchID]; ok {
			row.Events = append(row.Events, e)
		}
	}
	if err := evRows.Err(); err != nil {
		return nil, fmt.Errorf("OwnerDayCalendar: event rows: %w", err)
	}

	day := &CalendarDay{Date: dateStr, Pitches: make([]CalendarPitchRow, 0, len(order))}
	for _, pid := range order {
		day.Pitches = append(day.Pitches, *rowsByPitch[pid])
	}
	return day, nil
}
