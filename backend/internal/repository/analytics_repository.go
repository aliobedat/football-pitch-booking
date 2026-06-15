package repository

// AnalyticsRepository backs the finance/analytics endpoints (Dashboard PR 2).
// These are owner/admin-only surfaces; staff are hard-rejected at the route +
// handler before ever reaching this layer.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

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
