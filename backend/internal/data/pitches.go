package data

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// pitchColumns is the canonical SELECT list for all Pitch queries.
// Column order must match the Scan call in scanPitch.
// Columns that do not exist in the DB (description, image_url) are omitted.
const pitchColumns = `
	id,
	COALESCE(owner_id, 0),
	name,
	neighborhood,
	surface,
	format,
	price_per_hour,
	rating,
	review_count,
	is_featured,
	amenities,
	pitch_hue
`

type Pitch struct {
	ID           int      `json:"id"`
	OwnerID      int      `json:"owner_id,omitempty"`
	Name         string   `json:"name"`
	Neighborhood string   `json:"neighborhood"`
	Surface      string   `json:"surface"`
	Format       string   `json:"format"`
	PricePerHour int      `json:"pricePerHour"`
	Rating       float64  `json:"rating"`
	ReviewCount  int      `json:"reviewCount"`
	IsFeatured   bool     `json:"isFeatured"`
	Amenities    []string `json:"amenities"`
	PitchHue     string   `json:"pitchHue"`
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

// scanPitch reads one row into a Pitch value. Column order must match pitchColumns.
func scanPitch(row interface {
	Scan(...any) error
}) (Pitch, error) {
	var p Pitch
	err := row.Scan(
		&p.ID, &p.OwnerID,
		&p.Name, &p.Neighborhood, &p.Surface, &p.Format,
		&p.PricePerHour,
		&p.Rating, &p.ReviewCount,
		&p.IsFeatured, &p.Amenities, &p.PitchHue,
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
		wheres = append(wheres, fmt.Sprintf("neighborhood = $%d", len(args)))
	}
	if filters.Format != "" {
		args = append(args, filters.Format)
		wheres = append(wheres, fmt.Sprintf("format = $%d", len(args)))
	}
	if filters.FeaturedOnly {
		args = append(args, true)
		wheres = append(wheres, fmt.Sprintf("is_featured = $%d", len(args)))
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT %s FROM pitches %s
		ORDER BY is_featured DESC, price_per_hour ASC, id ASC
	`, pitchColumns, whereClause)

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
		SELECT %s FROM pitches WHERE id = $1
	`, pitchColumns), id)

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
		SELECT %s FROM pitches WHERE owner_id = $1 ORDER BY id DESC
	`, pitchColumns), ownerID)
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

func (m *PitchModel) CreatePitch(ctx context.Context, req CreatePitchRequest) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	hue := pitchHues[req.OwnerID%len(pitchHues)]

	row := m.DB.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO pitches
			(owner_id, name, neighborhood, surface, format, price_per_hour,
			 rating, review_count, is_featured, pitch_hue, amenities)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, false, $7, '{}')
		RETURNING %s
	`, pitchColumns),
		req.OwnerID, req.Name, req.Neighborhood, req.Surface, req.Format,
		req.PricePerHour, hue,
	)

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
