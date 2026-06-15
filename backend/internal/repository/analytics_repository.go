package repository

// AnalyticsRepository backs the finance/analytics endpoints (Dashboard PR 2).
// These are owner/admin-only surfaces; staff are hard-rejected at the route +
// handler before ever reaching this layer.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
	// OwnerRevenueSummary sums confirmed-booking revenue. ownerScope follows the
	// data-layer convention: 0 = admin (all pitches), else filter to that owner's
	// pitches. pitchID 0 = every in-scope pitch, else a single pitch.
	OwnerRevenueSummary(ctx context.Context, ownerScope, pitchID int) (RevenueSummary, error)
}

type analyticsRepo struct {
	db *pgxpool.Pool
}

// NewAnalyticsRepository constructs a Postgres-backed AnalyticsRepository.
func NewAnalyticsRepository(db *pgxpool.Pool) AnalyticsRepository {
	return &analyticsRepo{db: db}
}

func (r *analyticsRepo) OwnerRevenueSummary(ctx context.Context, ownerScope, pitchID int) (RevenueSummary, error) {
	s := RevenueSummary{PitchID: pitchID}
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(b.total_price), 0)::float8, COUNT(*)
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.status = 'confirmed'
		  AND ($1 = 0 OR p.owner_id = $1)
		  AND ($2 = 0 OR p.id = $2)
	`, ownerScope, pitchID).Scan(&s.TotalRevenue, &s.BookingCount)
	if err != nil {
		return RevenueSummary{}, fmt.Errorf("OwnerRevenueSummary: %w", err)
	}
	return s, nil
}
