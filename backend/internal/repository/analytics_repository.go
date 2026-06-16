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
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// RevenueSummary is the headline financial roll-up for an owner (or a single
// pitch). Revenue counts CONFIRMED bookings only (the revenue-correct status).
type RevenueSummary struct {
	TotalRevenue float64 `json:"total_revenue"`
	BookingCount int     `json:"booking_count"`
	PitchID      int     `json:"pitch_id,omitempty"`
}

// KPISummary is the owner dashboard's headline tile set (WO2). Every figure is
// owner-scoped and anchored in Asia/Amman per the approved revenue/bucket
// contract: revenue = SUM(total_price) over CONFIRMED bookings (blocks are
// unpriced → contribute 0); counts EXCLUDE source='block' (a maintenance hold is
// not a business booking). Time windows are bucketed by the booking START instant
// (lower(booking_range)), the occupancy date.
type KPISummary struct {
	TodayRevenue        float64 `json:"today_revenue"`         // confirmed revenue for the current Amman day
	TodayConfirmedCount int     `json:"today_confirmed_count"` // confirmed, non-block bookings playing today (Amman)
	WeekToDateRevenue   float64 `json:"week_to_date_revenue"`  // confirmed revenue since the start of the current Amman week
	UpcomingBookings    int     `json:"upcoming_bookings"`     // confirmed, non-block bookings whose slot is still in the future
}

// TimeBucket is one point on the owner analytics time series: a calendar bucket
// (day/week/month start, Amman) with its confirmed revenue and booking volume.
// Volume EXCLUDES blocks; revenue is source-agnostic (blocks contribute 0).
type TimeBucket struct {
	Bucket  string  `json:"bucket"` // YYYY-MM-DD (Amman) — start of the day/week/month
	Revenue float64 `json:"revenue"`
	Volume  int     `json:"volume"`
}

// TimeSeriesParams bounds an owner time-series query. Granularity is one of
// day|week|month (validated by the handler — never interpolated raw). From/To are
// absolute UTC instants bounding the booking START as [From, To), derived by the
// handler from Amman calendar dates. PitchID 0 = every in-scope pitch.
type TimeSeriesParams struct {
	Granularity string
	From        time.Time
	To          time.Time
	PitchID     int
}

// AnalyticsRepository reads aggregate financial data.
type AnalyticsRepository interface {
	// OwnerRevenueSummary sums confirmed-booking revenue scoped to the actor via
	// the canonical auth.Actor.OwnerScopeFilter primitive (admin → all pitches;
	// owner → only their own). pitchID 0 = every in-scope pitch, else a single one.
	OwnerRevenueSummary(ctx context.Context, actor auth.Actor, pitchID int) (RevenueSummary, error)

	// OwnerKPIs returns the dashboard headline tiles (owner-scoped, Amman-anchored).
	OwnerKPIs(ctx context.Context, actor auth.Actor) (KPISummary, error)

	// OwnerTimeSeries returns confirmed revenue + non-block volume grouped by the
	// requested Amman calendar bucket, owner-scoped, ordered by bucket ascending.
	OwnerTimeSeries(ctx context.Context, actor auth.Actor, params TimeSeriesParams) ([]TimeBucket, error)
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

// ammanWeekStartUTC returns the UTC instant at 00:00 Amman of the most recent
// Saturday on/before `instant` — the start of the current Amman business week.
// Jordan's weekend is Friday–Saturday, so Saturday opens the operational week.
func ammanWeekStartUTC(instant time.Time) time.Time {
	local := instant.In(timeutil.Amman())
	// Go weekday: Sunday=0 … Saturday=6. Days since the most recent Saturday.
	offset := (int(local.Weekday()) + 1) % 7
	start, _ := timeutil.AmmanDayBoundsUTC(local.AddDate(0, 0, -offset))
	return start
}

// OwnerKPIs computes the four headline tiles in ONE owner-scoped, set-based query
// using conditional aggregation (FILTER) — no N+1, no per-tile round-trip. Time
// windows are computed in Go against Asia/Amman and passed as UTC bounds, so the
// SQL stays timezone-agnostic and the boundaries honour the tz database.
func (r *analyticsRepo) OwnerKPIs(ctx context.Context, actor auth.Actor) (KPISummary, error) {
	now := time.Now().UTC()
	todayStart, todayEnd := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now))
	weekStart := ammanWeekStartUTC(now)

	ownerClause, args := actor.OwnerScopeFilter("p.owner_id", 1)
	// Append the four window bounds after the (optional) owner-scope arg.
	ts := len(args) + 1
	args = append(args, todayStart, todayEnd, weekStart, now)

	var k KPISummary
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			COALESCE(SUM(b.total_price) FILTER (
				WHERE lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d), 0)::float8 AS today_revenue,
			COUNT(*) FILTER (
				WHERE b.source <> 'block'
				  AND lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d) AS today_count,
			COALESCE(SUM(b.total_price) FILTER (
				WHERE lower(b.booking_range) >= $%d AND lower(b.booking_range) < $%d), 0)::float8 AS wtd_revenue,
			COUNT(*) FILTER (
				WHERE b.source <> 'block' AND lower(b.booking_range) >= $%d) AS upcoming
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed' AND %s
	`, ts, ts+1, ts, ts+1, ts+2, ts+3, ts+3, ownerClause),
		args...).Scan(&k.TodayRevenue, &k.TodayConfirmedCount, &k.WeekToDateRevenue, &k.UpcomingBookings)
	if err != nil {
		return KPISummary{}, fmt.Errorf("OwnerKPIs: %w", err)
	}
	return k, nil
}

// OwnerTimeSeries groups confirmed revenue + non-block volume by the requested
// Amman calendar bucket. Bucketing converts the stored UTC instant to Amman wall
// time (AT TIME ZONE) before truncating, so a booking is attributed to the Amman
// day/week/month it is PLAYED. Owner scoping and the [From,To) range are applied
// in SQL; granularity is validated by the caller and safe to interpolate.
func (r *analyticsRepo) OwnerTimeSeries(ctx context.Context, actor auth.Actor, params TimeSeriesParams) ([]TimeBucket, error) {
	ownerClause, args := actor.OwnerScopeFilter("p.owner_id", 1)

	args = append(args, params.From)
	fromIdx := len(args)
	args = append(args, params.To)
	toIdx := len(args)

	pitchClause := "TRUE"
	if params.PitchID > 0 {
		args = append(args, params.PitchID)
		pitchClause = fmt.Sprintf("p.id = $%d", len(args))
	}

	// date_trunc on the Amman-local wall time. The result is a naive timestamp at
	// the bucket start in Amman; to_char renders the calendar label the client keys on.
	bucketExpr := fmt.Sprintf(
		"date_trunc('%s', lower(b.booking_range) AT TIME ZONE 'Asia/Amman')", params.Granularity)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			to_char(%s, 'YYYY-MM-DD') AS bucket,
			COALESCE(SUM(b.total_price), 0)::float8 AS revenue,
			COUNT(*) FILTER (WHERE b.source <> 'block') AS volume
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed'
		  AND %s
		  AND %s
		  AND lower(b.booking_range) >= $%d
		  AND lower(b.booking_range) <  $%d
		GROUP BY 1
		ORDER BY 1 ASC
	`, bucketExpr, ownerClause, pitchClause, fromIdx, toIdx), args...)
	if err != nil {
		return nil, fmt.Errorf("OwnerTimeSeries: query: %w", err)
	}
	defer rows.Close()

	series := make([]TimeBucket, 0)
	for rows.Next() {
		var b TimeBucket
		if err := rows.Scan(&b.Bucket, &b.Revenue, &b.Volume); err != nil {
			return nil, fmt.Errorf("OwnerTimeSeries: scan: %w", err)
		}
		series = append(series, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("OwnerTimeSeries: rows: %w", err)
	}
	return series, nil
}
