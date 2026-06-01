package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pitchHues is cycled for new pitches that don't specify a colour.
var pitchHues = []string{
	"rgba(16,71,50,0.65)",
	"rgba(14,62,94,0.65)",
	"rgba(94,45,14,0.65)",
	"rgba(75,14,60,0.65)",
	"rgba(14,80,45,0.60)",
}

// pitchSelectCols is the canonical column list for SELECT queries.
// Requires the FROM clause to alias pitches as "p" and LEFT JOIN reviews as "r".
// Column order must match scanPitch exactly.
const pitchSelectCols = `
	p.id,
	COALESCE(p.owner_id, 0),
	p.name,
	p.neighborhood,
	p.surface,
	p.format,
	p.price_per_hour,
	ROUND(AVG(r.rating), 1)::double precision,
	COUNT(r.id)::int,
	p.is_featured,
	p.amenities,
	p.pitch_hue,
	p.latitude,
	p.longitude
`

// pitchReturnCols is used in INSERT/UPDATE RETURNING clauses where no
// FROM-level join is available.  Correlated subqueries compute rating/count
// from the reviews table for the affected row.
const pitchReturnCols = `
	id,
	COALESCE(owner_id, 0),
	name,
	neighborhood,
	surface,
	format,
	price_per_hour,
	(SELECT ROUND(AVG(rating), 1)::double precision FROM reviews WHERE pitch_id = pitches.id),
	(SELECT COUNT(id)::int FROM reviews WHERE pitch_id = pitches.id),
	is_featured,
	amenities,
	pitch_hue,
	latitude,
	longitude
`

// Pitch is the canonical Go representation of a pitch row.
// JSON tags match what the frontend expects.
type Pitch struct {
	ID           int      `json:"id"`
	OwnerID      int      `json:"owner_id,omitempty"`
	Name         string   `json:"name"`
	Neighborhood string   `json:"neighborhood"`
	Surface      string   `json:"surface"`
	Format       string   `json:"format"`
	PricePerHour int      `json:"pricePerHour"`
	Rating       *float64 `json:"rating"`       // nil = no reviews yet
	ReviewsCount int      `json:"reviewsCount"` // renamed from reviewCount
	IsFeatured   bool     `json:"isFeatured"`
	Amenities    []string `json:"amenities"`
	PitchHue     string   `json:"pitchHue"`
	Latitude     float64  `json:"lat"`
	Longitude    float64  `json:"lng"`
}

type CreatePitchRequest struct {
	Name         string `json:"name"           binding:"required"`
	Neighborhood string `json:"neighborhood"   binding:"required"`
	Surface      string `json:"surface"        binding:"required"`
	Format       string `json:"format"         binding:"required"`
	PricePerHour int    `json:"price_per_hour" binding:"required,gt=0"`
	OwnerID      int    // injected from JWT — never from JSON body
}

type PitchFilters struct {
	Neighborhood string
	Format       string
	FeaturedOnly bool
}

type PitchModel struct {
	DB *pgxpool.Pool
}

// scanPitch reads one row into a Pitch value.
// Column order must match pitchSelectCols / pitchReturnCols exactly.
func scanPitch(row interface {
	Scan(...any) error
}) (Pitch, error) {
	var p Pitch
	err := row.Scan(
		&p.ID, &p.OwnerID,
		&p.Name, &p.Neighborhood, &p.Surface, &p.Format,
		&p.PricePerHour,
		&p.Rating, &p.ReviewsCount, // *float64 scans NULL → nil
		&p.IsFeatured, &p.Amenities, &p.PitchHue,
		&p.Latitude, &p.Longitude,
	)
	return p, err
}

func (m *PitchModel) GetAll(ctx context.Context, filters PitchFilters) ([]Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	args := []interface{}{}
	wheres := []string{}

	if filters.Neighborhood != "" {
		args = append(args, filters.Neighborhood)
		wheres = append(wheres, fmt.Sprintf("p.neighborhood = $%d", len(args)))
	}
	if filters.Format != "" {
		args = append(args, filters.Format)
		wheres = append(wheres, fmt.Sprintf("p.format = $%d", len(args)))
	}
	if filters.FeaturedOnly {
		args = append(args, true)
		wheres = append(wheres, fmt.Sprintf("p.is_featured = $%d", len(args)))
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM   pitches p
		LEFT JOIN reviews r ON r.pitch_id = p.id
		%s
		GROUP BY p.id
		ORDER BY p.is_featured DESC, p.price_per_hour ASC, p.id ASC
	`, pitchSelectCols, whereClause)

	rows, err := m.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pitches := []Pitch{}
	for rows.Next() {
		p, err := scanPitch(rows)
		if err != nil {
			return nil, err
		}
		pitches = append(pitches, p)
	}
	return pitches, rows.Err()
}

func (m *PitchModel) GetByID(ctx context.Context, id int) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := m.DB.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s
		FROM   pitches p
		LEFT JOIN reviews r ON r.pitch_id = p.id
		WHERE  p.id = $1
		GROUP BY p.id
	`, pitchSelectCols), id)

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *PitchModel) GetByOwnerID(ctx context.Context, ownerID int) ([]Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := m.DB.Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM   pitches p
		LEFT JOIN reviews r ON r.pitch_id = p.id
		WHERE  p.owner_id = $1
		GROUP BY p.id
		ORDER BY p.id DESC
	`, pitchSelectCols), ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pitches := []Pitch{}
	for rows.Next() {
		p, err := scanPitch(rows)
		if err != nil {
			return nil, err
		}
		pitches = append(pitches, p)
	}
	return pitches, rows.Err()
}

// UpdatePitchRequest holds the fields an owner may change on their pitch.
// Zero-value fields are ignored (existing DB value is kept).
type UpdatePitchRequest struct {
	Name         string `json:"name"`
	Neighborhood string `json:"neighborhood"`
	Surface      string `json:"surface"`
	Format       string `json:"format"`
	PricePerHour int    `json:"price_per_hour"`
}

// UpdatePitch applies a partial update to pitch `id`.
// ownerID == 0 means the caller is an admin and the ownership check is skipped.
func (m *PitchModel) UpdatePitch(ctx context.Context, id, ownerID int, req UpdatePitchRequest) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	const setCols = `
		name           = CASE WHEN $%d <> '' THEN $%d ELSE name END,
		neighborhood   = CASE WHEN $%d <> '' THEN $%d ELSE neighborhood END,
		surface        = CASE WHEN $%d <> '' THEN $%d ELSE surface END,
		format         = CASE WHEN $%d <> '' THEN $%d ELSE format END,
		price_per_hour = CASE WHEN $%d > 0   THEN $%d ELSE price_per_hour END`

	var row pgx.Row
	if ownerID == 0 {
		row = m.DB.QueryRow(ctx, fmt.Sprintf(
			`UPDATE pitches SET `+setCols+` WHERE id = $1 RETURNING %s`,
			2, 2, 3, 3, 4, 4, 5, 5, 6, 6, pitchReturnCols,
		), id, req.Name, req.Neighborhood, req.Surface, req.Format, req.PricePerHour)
	} else {
		row = m.DB.QueryRow(ctx, fmt.Sprintf(
			`UPDATE pitches SET `+setCols+` WHERE id = $1 AND owner_id = $2 RETURNING %s`,
			3, 3, 4, 4, 5, 5, 6, 6, 7, 7, pitchReturnCols,
		), id, ownerID, req.Name, req.Neighborhood, req.Surface, req.Format, req.PricePerHour)
	}

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// DeletePitch removes pitch `id`.
// ownerID == 0 means the caller is an admin and the ownership check is skipped.
func (m *PitchModel) DeletePitch(ctx context.Context, id, ownerID int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `DELETE FROM pitches WHERE id = $1 AND owner_id = $2`
	args := []any{id, ownerID}
	if ownerID == 0 {
		query = `DELETE FROM pitches WHERE id = $1`
		args = []any{id}
	}

	tag, err := m.DB.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (m *PitchModel) CreatePitch(ctx context.Context, req CreatePitchRequest) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	hue := pitchHues[req.OwnerID%len(pitchHues)]

	row := m.DB.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO pitches
			(owner_id, name, neighborhood, surface, format, price_per_hour,
			 rating, review_count, is_featured, pitch_hue, amenities,
			 latitude, longitude)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, false, $7, '{}', 0, 0)
		RETURNING %s
	`, pitchReturnCols),
		req.OwnerID, req.Name, req.Neighborhood, req.Surface, req.Format,
		req.PricePerHour, hue,
	)

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
