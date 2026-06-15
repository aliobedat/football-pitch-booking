package repository

// AnalyticsRepository backs the finance/analytics endpoints (Dashboard PR 2).
// These are owner/admin-only surfaces; staff are hard-rejected at the route +
// handler before ever reaching this layer.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// realizedRevenuePredicate — rows that count as realized revenue (Dashboard
// PR 5): confirmed, attended (not a no-show), and an actual paid booking
// (player or manual walk-in; blocks carry no revenue).
const realizedRevenuePredicate = `b.status = 'confirmed' AND b.attendance <> 'no_show' AND b.source IN ('player','manual')`

// ammanTS renders a booking's start instant in Asia/Amman for civil-day/hour
// grouping (revenue-by-day, heatmap weekday/hour).
const ammanTS = `(lower(b.booking_range) AT TIME ZONE 'Asia/Amman')`

// DayPoint / MonthPoint are revenue time-series buckets (date/month as ISO).
type DayPoint struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
}
type MonthPoint struct {
	Month   string  `json:"month"`
	Revenue float64 `json:"revenue"`
}

// HeatCell is one bookings-by hour×weekday bucket. Weekday: 0=Sun..6=Sat (PG DOW).
type HeatCell struct {
	Weekday int `json:"weekday"`
	Hour    int `json:"hour"`
	Count   int `json:"count"`
}

// PeriodTotals are the headline figures for one window (for current-vs-previous
// and the no-show rate). NoShowRate = NoShows / (NoShows + relevant attended/
// pending player+manual bookings).
type PeriodTotals struct {
	Revenue    float64 `json:"revenue"`
	Bookings   int     `json:"bookings"` // realized (revenue-bearing) bookings
	NoShows    int     `json:"no_shows"`
	NoShowRate float64 `json:"no_show_rate"`
}

// RevenueSummary is the headline financial roll-up for an owner (or a single
// pitch). Revenue counts CONFIRMED bookings only (the revenue-correct status).
type RevenueSummary struct {
	TotalRevenue float64 `json:"total_revenue"`
	BookingCount int     `json:"booking_count"`
	PitchID      int     `json:"pitch_id,omitempty"`
}

// AnalyticsRepository reads aggregate financial data.
type AnalyticsRepository interface {
	// OwnerRevenueSummary sums confirmed-booking revenue scoped to the actor via
	// the canonical auth.Actor.OwnerScopeFilter primitive (admin → all pitches;
	// owner → only their own). pitchID 0 = every in-scope pitch, else a single one.
	OwnerRevenueSummary(ctx context.Context, actor auth.Actor, pitchID int) (RevenueSummary, error)

	// RevenueByDay / RevenueByMonth return realized-revenue time series over
	// [fromUTC,toUTC), bucketed by Asia/Amman civil date/month, scoped to actor.
	RevenueByDay(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]DayPoint, error)
	RevenueByMonth(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]MonthPoint, error)

	// BookingHeatmap returns realized booking counts by Amman weekday × hour.
	BookingHeatmap(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]HeatCell, error)

	// Totals returns realized revenue/bookings + no-show count & rate for a window.
	Totals(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) (PeriodTotals, error)
}

// scopedWhere builds "<ownerScope> AND lower(b.booking_range) >= $n AND < $n+1"
// and the args, starting placeholders at 1.
func scopedWindow(actor auth.Actor, fromUTC, toUTC time.Time) (clause string, args []any) {
	ownerClause, args := actor.OwnerScopeFilter("p.owner_id", 1)
	args = append(args, fromUTC, toUTC)
	clause = fmt.Sprintf("%s AND lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d",
		ownerClause, len(args)-1, len(args))
	return clause, args
}

func (r *analyticsRepo) RevenueByDay(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]DayPoint, error) {
	w, args := scopedWindow(actor, fromUTC, toUTC)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT to_char(%s::date, 'YYYY-MM-DD') AS d, COALESCE(SUM(b.total_price),0)::float8
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE %s AND %s
		GROUP BY d ORDER BY d`, ammanTS, realizedRevenuePredicate, w), args...)
	if err != nil {
		return nil, fmt.Errorf("RevenueByDay: %w", err)
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Date, &p.Revenue); err != nil {
			return nil, fmt.Errorf("RevenueByDay: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *analyticsRepo) RevenueByMonth(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]MonthPoint, error) {
	w, args := scopedWindow(actor, fromUTC, toUTC)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT to_char(date_trunc('month', %s), 'YYYY-MM') AS m, COALESCE(SUM(b.total_price),0)::float8
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE %s AND %s
		GROUP BY m ORDER BY m`, ammanTS, realizedRevenuePredicate, w), args...)
	if err != nil {
		return nil, fmt.Errorf("RevenueByMonth: %w", err)
	}
	defer rows.Close()
	out := []MonthPoint{}
	for rows.Next() {
		var p MonthPoint
		if err := rows.Scan(&p.Month, &p.Revenue); err != nil {
			return nil, fmt.Errorf("RevenueByMonth: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *analyticsRepo) BookingHeatmap(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]HeatCell, error) {
	w, args := scopedWindow(actor, fromUTC, toUTC)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT EXTRACT(DOW FROM %s)::int AS wd, EXTRACT(HOUR FROM %s)::int AS hr, COUNT(*)::int
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE %s AND %s
		GROUP BY wd, hr ORDER BY wd, hr`, ammanTS, ammanTS, realizedRevenuePredicate, w), args...)
	if err != nil {
		return nil, fmt.Errorf("BookingHeatmap: %w", err)
	}
	defer rows.Close()
	out := []HeatCell{}
	for rows.Next() {
		var c HeatCell
		if err := rows.Scan(&c.Weekday, &c.Hour, &c.Count); err != nil {
			return nil, fmt.Errorf("BookingHeatmap: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *analyticsRepo) Totals(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) (PeriodTotals, error) {
	w, args := scopedWindow(actor, fromUTC, toUTC)
	// Realized revenue + bookings; no-shows counted over player+manual confirmed
	// rows in-window. Rate = no_show / (realized + no_show).
	var t PeriodTotals
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT
		  COALESCE(SUM(b.total_price) FILTER (WHERE %s),0)::float8,
		  COUNT(*) FILTER (WHERE %s)::int AS realized,
		  COUNT(*) FILTER (WHERE b.status='confirmed' AND b.source IN ('player','manual') AND b.attendance='no_show')::int AS noshow
		FROM bookings b JOIN pitches p ON p.id = b.pitch_id
		WHERE %s`, realizedRevenuePredicate, realizedRevenuePredicate, w),
		args...).Scan(&t.Revenue, &t.Bookings, &t.NoShows)
	if err != nil {
		return PeriodTotals{}, fmt.Errorf("Totals: %w", err)
	}
	if denom := t.Bookings + t.NoShows; denom > 0 {
		t.NoShowRate = float64(t.NoShows) / float64(denom)
	}
	return t, nil
}

type analyticsRepo struct {
	db *pgxpool.Pool
}

// NewAnalyticsRepository constructs a Postgres-backed AnalyticsRepository.
func NewAnalyticsRepository(db *pgxpool.Pool) AnalyticsRepository {
	return &analyticsRepo{db: db}
}

func (r *analyticsRepo) OwnerRevenueSummary(ctx context.Context, actor auth.Actor, pitchID int) (RevenueSummary, error) {
	s := RevenueSummary{PitchID: pitchID}

	// Canonical owner scoping — admin → "TRUE"; owner → p.owner_id = $1.
	ownerClause, args := actor.OwnerScopeFilter("p.owner_id", 1)
	pitchClause := "TRUE"
	if pitchID > 0 {
		args = append(args, pitchID)
		pitchClause = fmt.Sprintf("p.id = $%d", len(args))
	}

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(b.total_price), 0)::float8, COUNT(*)
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed' AND %s AND %s
	`, ownerClause, pitchClause), args...).Scan(&s.TotalRevenue, &s.BookingCount)
	if err != nil {
		return RevenueSummary{}, fmt.Errorf("OwnerRevenueSummary: %w", err)
	}
	return s, nil
}
