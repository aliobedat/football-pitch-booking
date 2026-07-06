package repository

// ReportsRepository backs the two read-only owner report endpoints (R1):
// GET /owner/reports/financial and GET /owner/reports/bookings. Owner/admin
// only; staff never reach this layer.
//
// REVENUE PREDICATES ARE RATIFIED TO MATCH OwnerTimeSeries EXACTLY (dashboard
// parity is a hard requirement — a printed statement must equal the analytics
// tiles for the same window):
//   - gross    = SUM(total_price) WHERE status='confirmed' (source-agnostic;
//     blocks carry total_price 0)
//   - collected = same, FILTER (payment_status='paid_cash')
//   - booking_count excludes source='block'
//   - attribution strictly by the booking START: lower(booking_range) inside
//     the half-open UTC window [from, to) — never && (no double attribution)
//   - pitches JOIN WITHOUT a deleted_at filter, mirroring analytics: the
//     historical revenue of a soft-deleted pitch still counts
//     (docs/followups/reports-soft-deleted-pitch-revenue.md)
//
// Money follows the house pattern (docs/followups/money-decimal-migration.md):
// SUM() over NUMERIC in SQL, a single ::float8 cast on the final aggregate,
// round3 in the handler, JSON numbers. Never summed in Go.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// FinancialReportSummary is the statement's headline roll-up.
type FinancialReportSummary struct {
	BookingCount   int     `json:"booking_count"`
	GrossRevenue   float64 `json:"gross_revenue"`
	Collected      float64 `json:"collected"`
	Outstanding    float64 `json:"outstanding"`
	CancelledCount int     `json:"cancelled_count"`
}

// FinancialReportDay is one Amman calendar day's confirmed activity.
type FinancialReportDay struct {
	Date         string  `json:"date"` // YYYY-MM-DD (Amman)
	BookingCount int     `json:"booking_count"`
	GrossRevenue float64 `json:"gross_revenue"`
	Collected    float64 `json:"collected"`
}

// FinancialReportPitch is one pitch's confirmed activity (unfiltered reports only).
type FinancialReportPitch struct {
	PitchID      int64   `json:"pitch_id"`
	PitchName    string  `json:"pitch_name"`
	BookingCount int     `json:"booking_count"`
	GrossRevenue float64 `json:"gross_revenue"`
	Collected    float64 `json:"collected"`
}

// FinancialReport is the repository payload; the handler assembles the envelope
// (from/to/pitch_id/pitch_name) around it.
type FinancialReport struct {
	Summary FinancialReportSummary
	ByDay   []FinancialReportDay
	ByPitch []FinancialReportPitch // populated only when pitchID == 0
}

// BookingsReportSummary buckets the statement's rows. total excludes blocks and
// cancelled rows; attended/no_show read the attendance column over that same
// base set (so total = attended + no_show + still-pending); cancelled counts
// status='cancelled' non-block rows (which are excluded from the rows list).
type BookingsReportSummary struct {
	Total     int `json:"total"`
	Attended  int `json:"attended"`
	NoShow    int `json:"no_show"`
	Cancelled int `json:"cancelled"`
}

// ReportBookingRow is one statement line. Identity resolution is ratified:
// customer_name  = COALESCE(guest_name, contact_name, u.full_name, '')
// customer_phone = COALESCE(guest_phone, contact_phone, u.phone, '').
type ReportBookingRow struct {
	ID            int64     `json:"id"`
	PitchID       int64     `json:"pitch_id"`
	PitchName     string    `json:"pitch_name"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Source        string    `json:"source"`
	Status        string    `json:"status"`
	Attendance    string    `json:"attendance"`
	CustomerName  string    `json:"customer_name"`
	CustomerPhone string    `json:"customer_phone"`
	TotalPrice    float64   `json:"total_price"`
	PaymentStatus string    `json:"payment_status"`
}

// BookingsReport is the repository payload for the bookings statement.
type BookingsReport struct {
	Summary BookingsReportSummary
	Rows    []ReportBookingRow
}

// ErrReportTooLarge signals that the rows query exceeded the caller's limit —
// the handler maps it to 422 (narrow the window rather than truncate silently).
var ErrReportTooLarge = errors.New("reports: result exceeds the row limit")

type ReportsRepository interface {
	// ResolveReportPitch verifies pitchID exists, is not soft-deleted, and is in
	// the actor's scope, returning its name. ErrPitchNotFound otherwise — the
	// "not found OR not owned" 404 convention (existence never leaked). A report
	// explicitly filtered to a wrong pitch must 404, never render empty.
	ResolveReportPitch(ctx context.Context, actor auth.Actor, pitchID int64) (string, error)

	// OwnerFinancialReport aggregates the window [from, to) (UTC instants derived
	// from Amman civil dates by the handler). pitchID 0 = every in-scope pitch
	// (ByPitch populated); >0 = that pitch only (caller must resolve it first).
	OwnerFinancialReport(ctx context.Context, actor auth.Actor, pitchID int64, from, to time.Time) (FinancialReport, error)

	// OwnerBookingsReport returns the statement rows (blocks and cancelled rows
	// excluded, ordered by start ascending) plus the attendance summary. Returns
	// ErrReportTooLarge when the window holds more than maxRows rows.
	OwnerBookingsReport(ctx context.Context, actor auth.Actor, pitchID int64, from, to time.Time, maxRows int) (BookingsReport, error)
}

type reportsRepo struct {
	db *pgxpool.Pool
}

// NewReportsRepository constructs a Postgres-backed ReportsRepository.
func NewReportsRepository(db *pgxpool.Pool) ReportsRepository {
	return &reportsRepo{db: db}
}

func (r *reportsRepo) ResolveReportPitch(ctx context.Context, actor auth.Actor, pitchID int64) (string, error) {
	args := []any{pitchID}
	ownerClause, ownerArgs := actor.OwnerScopeFilter("owner_id", 2)
	args = append(args, ownerArgs...)

	var name string
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT name FROM pitches
		WHERE id = $1 AND deleted_at IS NULL AND %s
	`, ownerClause), args...).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrPitchNotFound
		}
		return "", fmt.Errorf("ResolveReportPitch: %w", err)
	}
	return name, nil
}

// reportScope builds the shared owner-scope + window + optional-pitch predicate
// tail. Placeholders: owner scope first (if any), then from, to, then pitch.
func reportScope(actor auth.Actor, pitchID int64, from, to time.Time) (ownerClause, pitchClause string, fromIdx, toIdx int, args []any) {
	ownerClause, args = actor.OwnerScopeFilter("p.owner_id", 1)
	args = append(args, from)
	fromIdx = len(args)
	args = append(args, to)
	toIdx = len(args)
	pitchClause = "TRUE"
	if pitchID > 0 {
		args = append(args, pitchID)
		pitchClause = fmt.Sprintf("p.id = $%d", len(args))
	}
	return
}

func (r *reportsRepo) OwnerFinancialReport(ctx context.Context, actor auth.Actor, pitchID int64, from, to time.Time) (FinancialReport, error) {
	ownerClause, pitchClause, fromIdx, toIdx, args := reportScope(actor, pitchID, from, to)

	// NOTE: no b.status filter in the WHERE — confirmed and cancelled legs are
	// split by FILTER so the whole summary comes back in one set-based query.
	window := fmt.Sprintf(
		"lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d", fromIdx, toIdx)

	var rep FinancialReport
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE b.status = 'confirmed' AND b.source <> 'block'),
			COALESCE(SUM(b.total_price) FILTER (WHERE b.status = 'confirmed'), 0)::float8,
			COALESCE(SUM(b.total_price) FILTER (
				WHERE b.status = 'confirmed' AND b.payment_status = 'paid_cash'), 0)::float8,
			COUNT(*) FILTER (WHERE b.status = 'cancelled' AND b.source <> 'block')
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE %s AND %s AND %s
	`, ownerClause, pitchClause, window), args...).Scan(
		&rep.Summary.BookingCount, &rep.Summary.GrossRevenue,
		&rep.Summary.Collected, &rep.Summary.CancelledCount)
	if err != nil {
		return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: summary: %w", err)
	}
	// Difference of two SQL aggregates (house pattern — financials.go does the
	// same for Net); the handler rounds all three legs with round3.
	rep.Summary.Outstanding = rep.Summary.GrossRevenue - rep.Summary.Collected

	// Daily breakdown — confirmed only, bucketed on the Amman wall clock exactly
	// like OwnerTimeSeries ('day' granularity).
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			to_char(date_trunc('day', lower(b.booking_range) AT TIME ZONE 'Asia/Amman'), 'YYYY-MM-DD') AS day,
			COUNT(*) FILTER (WHERE b.source <> 'block'),
			COALESCE(SUM(b.total_price), 0)::float8,
			COALESCE(SUM(b.total_price) FILTER (WHERE b.payment_status = 'paid_cash'), 0)::float8
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed' AND %s AND %s AND %s
		GROUP BY 1
		ORDER BY 1 ASC
	`, ownerClause, pitchClause, window), args...)
	if err != nil {
		return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_day query: %w", err)
	}
	defer rows.Close()

	rep.ByDay = make([]FinancialReportDay, 0)
	for rows.Next() {
		var d FinancialReportDay
		if err := rows.Scan(&d.Date, &d.BookingCount, &d.GrossRevenue, &d.Collected); err != nil {
			return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_day scan: %w", err)
		}
		rep.ByDay = append(rep.ByDay, d)
	}
	if err := rows.Err(); err != nil {
		return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_day rows: %w", err)
	}

	// Per-pitch breakdown — only for the unfiltered statement.
	if pitchID == 0 {
		prow, err := r.db.Query(ctx, fmt.Sprintf(`
			SELECT p.id, p.name,
				COUNT(*) FILTER (WHERE b.source <> 'block'),
				COALESCE(SUM(b.total_price), 0)::float8,
				COALESCE(SUM(b.total_price) FILTER (WHERE b.payment_status = 'paid_cash'), 0)::float8
			FROM bookings b
			JOIN pitches p ON p.id = b.pitch_id
			WHERE b.status = 'confirmed' AND %s AND %s
			GROUP BY p.id, p.name
			ORDER BY 4 DESC, p.id ASC
		`, ownerClause, window), args...)
		if err != nil {
			return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_pitch query: %w", err)
		}
		defer prow.Close()

		rep.ByPitch = make([]FinancialReportPitch, 0)
		for prow.Next() {
			var pb FinancialReportPitch
			if err := prow.Scan(&pb.PitchID, &pb.PitchName, &pb.BookingCount, &pb.GrossRevenue, &pb.Collected); err != nil {
				return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_pitch scan: %w", err)
			}
			rep.ByPitch = append(rep.ByPitch, pb)
		}
		if err := prow.Err(); err != nil {
			return FinancialReport{}, fmt.Errorf("OwnerFinancialReport: by_pitch rows: %w", err)
		}
	}

	return rep, nil
}

func (r *reportsRepo) OwnerBookingsReport(ctx context.Context, actor auth.Actor, pitchID int64, from, to time.Time, maxRows int) (BookingsReport, error) {
	ownerClause, pitchClause, fromIdx, toIdx, args := reportScope(actor, pitchID, from, to)
	window := fmt.Sprintf(
		"lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d", fromIdx, toIdx)

	var rep BookingsReport

	// Summary buckets over all non-block rows in the window. total (and its
	// attended/no_show split) excludes cancelled rows — matching the rows list —
	// while cancelled tallies them.
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE b.status <> 'cancelled'),
			COUNT(*) FILTER (WHERE b.status <> 'cancelled' AND b.attendance = 'checked_in'),
			COUNT(*) FILTER (WHERE b.status <> 'cancelled' AND b.attendance = 'no_show'),
			COUNT(*) FILTER (WHERE b.status = 'cancelled')
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.source <> 'block' AND %s AND %s AND %s
	`, ownerClause, pitchClause, window), args...).Scan(
		&rep.Summary.Total, &rep.Summary.Attended, &rep.Summary.NoShow, &rep.Summary.Cancelled)
	if err != nil {
		return BookingsReport{}, fmt.Errorf("OwnerBookingsReport: summary: %w", err)
	}

	// Rows — statement order (play time, not created_at). LIMIT maxRows+1 so an
	// oversized window is detected without ever streaming an unbounded set.
	limitArgs := append(args, maxRows+1)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			b.id, b.pitch_id, COALESCE(p.name, ''),
			lower(b.booking_range), upper(b.booking_range),
			b.source, b.status, b.attendance,
			COALESCE(b.guest_name,  b.contact_name,  u.full_name, ''),
			COALESCE(b.guest_phone, b.contact_phone, u.phone,     ''),
			b.total_price, b.payment_status
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.source <> 'block' AND b.status <> 'cancelled' AND %s AND %s AND %s
		ORDER BY lower(b.booking_range) ASC, b.id ASC
		LIMIT $%d
	`, ownerClause, pitchClause, window, len(limitArgs)), limitArgs...)
	if err != nil {
		return BookingsReport{}, fmt.Errorf("OwnerBookingsReport: rows query: %w", err)
	}
	defer rows.Close()

	rep.Rows = make([]ReportBookingRow, 0)
	for rows.Next() {
		var row ReportBookingRow
		if err := rows.Scan(
			&row.ID, &row.PitchID, &row.PitchName,
			&row.StartTime, &row.EndTime,
			&row.Source, &row.Status, &row.Attendance,
			&row.CustomerName, &row.CustomerPhone,
			&row.TotalPrice, &row.PaymentStatus,
		); err != nil {
			return BookingsReport{}, fmt.Errorf("OwnerBookingsReport: rows scan: %w", err)
		}
		rep.Rows = append(rep.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return BookingsReport{}, fmt.Errorf("OwnerBookingsReport: rows: %w", err)
	}
	if len(rep.Rows) > maxRows {
		return BookingsReport{}, ErrReportTooLarge
	}

	return rep, nil
}
