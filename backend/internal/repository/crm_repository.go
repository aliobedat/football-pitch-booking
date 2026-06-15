package repository

// CRMRepository backs the owner CRM (Dashboard PR 5): players who booked at the
// owner's pitch(es), with derived visit/no-show counts. Scoped to the owner's
// pitches via OwnerScopeFilter — never global for an owner.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// CRMRow is one player's relationship summary for the in-scope pitches.
type CRMRow struct {
	PlayerID int    `json:"player_id"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Visits   int    `json:"visits"`   // attendance='checked_in'
	NoShows  int    `json:"no_shows"` // attendance='no_show'
}

// CRMRepository reads the per-owner player CRM.
type CRMRepository interface {
	OwnerCRM(ctx context.Context, actor auth.Actor) ([]CRMRow, error)
}

type crmRepo struct {
	db *pgxpool.Pool
}

// NewCRMRepository constructs a Postgres-backed CRMRepository.
func NewCRMRepository(db *pgxpool.Pool) CRMRepository {
	return &crmRepo{db: db}
}

func (r *crmRepo) OwnerCRM(ctx context.Context, actor auth.Actor) ([]CRMRow, error) {
	// Only real player bookings (source='player' ⇒ player_id NOT NULL). Visits and
	// no-shows are DERIVED from attendance, scoped to the owner's pitches.
	ownerClause, args := actor.OwnerScopeFilter("p.owner_id", 1)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT u.id, COALESCE(u.full_name,''), COALESCE(u.phone,''),
		       COUNT(*) FILTER (WHERE b.attendance='checked_in')::int AS visits,
		       COUNT(*) FILTER (WHERE b.attendance='no_show')::int   AS no_shows
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		JOIN users u ON u.id = b.player_id
		WHERE b.source='player' AND b.status <> 'cancelled' AND %s
		GROUP BY u.id, u.full_name, u.phone
		ORDER BY visits DESC, no_shows DESC`, ownerClause), args...)
	if err != nil {
		return nil, fmt.Errorf("OwnerCRM: %w", err)
	}
	defer rows.Close()
	out := []CRMRow{}
	for rows.Next() {
		var c CRMRow
		if err := rows.Scan(&c.PlayerID, &c.Name, &c.Phone, &c.Visits, &c.NoShows); err != nil {
			return nil, fmt.Errorf("OwnerCRM: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
