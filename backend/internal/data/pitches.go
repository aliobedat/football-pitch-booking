package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ErrPitchHasFutureBookings is returned by SoftDeletePitch when the pitch still
// has confirmed bookings that have not yet ended. Deletion is blocked (mapped to
// 409 by the handler) rather than auto-cancelling those bookings.
var ErrPitchHasFutureBookings = errors.New("pitch has future confirmed bookings")

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
	p.longitude,
	p.is_active,
	p.image_url,
	p.image_public_id,
	p.description,
	COALESCE(p.maps_url, '')
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
	longitude,
	is_active,
	image_url,
	image_public_id,
	description,
	COALESCE(maps_url, '')
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
	IsActive     bool     `json:"isActive"`
	ImageURL     string   `json:"image_url"`
	ImagePublicID string  `json:"image_public_id"`
	Description   string  `json:"description"`
	MapsURL       string  `json:"maps_url" db:"maps_url"`
}

type CreatePitchRequest struct {
	Name         string `json:"name"           binding:"required"`
	Neighborhood string `json:"neighborhood"   binding:"required"`
	Surface      string `json:"surface"        binding:"required"`
	Format       string `json:"format"         binding:"required"`
	PricePerHour int    `json:"price_per_hour" binding:"required,gt=0"`
	OwnerID      int    // injected from JWT — never from JSON body

	// Optional free-text description. Plain text only — stored raw and escaped at
	// render time (the frontend renders it as text, never via innerHTML). The
	// handler trims it and caps its length before this struct reaches the INSERT.
	Description string `json:"description"`

	// Optional image set at creation time. These come from a completed direct
	// upload to Cloudinary (the handler validates ImageURL belongs to our cloud
	// before persisting). Both empty means "no image yet".
	ImageURL      string `json:"image_url"`
	ImagePublicID string `json:"image_public_id"`
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
		&p.IsActive,
		&p.ImageURL, &p.ImagePublicID,
		&p.Description,
		&p.MapsURL,
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

	// Public listing: soft-deleted AND deactivated pitches are never shown to
	// players. These anchor the WHERE clause; any caller filters are AND-appended.
	// (Owner/admin listings use ListForActor, which is NOT filtered by is_active.)
	wheres = append([]string{"p.deleted_at IS NULL", "p.is_active = true"}, wheres...)
	whereClause := "WHERE " + strings.Join(wheres, " AND ")

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
		WHERE  p.id = $1 AND p.deleted_at IS NULL AND p.is_active = true
		GROUP BY p.id
	`, pitchSelectCols), id)

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListForActor returns the pitches the actor may manage: every (non-deleted)
// pitch for an admin, and only the actor's own pitches for an owner. The
// ownership predicate is applied in SQL — an owner can never see another owner's
// rows. Backs GET /owner/pitches.
func (m *PitchModel) ListForActor(ctx context.Context, actor auth.Actor) ([]Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	wheres := []string{"p.deleted_at IS NULL"}
	args := []interface{}{}
	if !actor.IsAdmin() {
		args = append(args, actor.UserID)
		wheres = append(wheres, fmt.Sprintf("p.owner_id = $%d", len(args)))
	}

	rows, err := m.DB.Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM   pitches p
		LEFT JOIN reviews r ON r.pitch_id = p.id
		WHERE  %s
		GROUP BY p.id
		ORDER BY p.id DESC
	`, pitchSelectCols, strings.Join(wheres, " AND ")), args...)
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
	// Description is written unconditionally (unlike the other fields, which keep
	// their value when sent empty): the reused create/edit form always submits the
	// full desired description, so an empty string is a legitimate "clear it". The
	// handler trims and length-caps it first.
	Description string `json:"description"`
	// MapsURL is a POINTER so we can distinguish three cases the other string
	// fields cannot: nil = field absent from the body → leave the DB value
	// unchanged; "" = present-but-empty → clear it; non-empty → set it. The SQL
	// uses COALESCE($n, maps_url), so a NULL param (nil pointer) keeps the column.
	MapsURL *string `json:"maps_url"`
}

// UpdatePitch applies a partial update to pitch `id`, scoped to the actor: an
// owner may only update their own pitch (ownership predicate in SQL), while an
// admin may update any. A soft-deleted pitch is never updatable. When the
// predicate matches no row the UPDATE returns pgx.ErrNoRows, which the handler
// maps to 404 (without leaking whether the row exists under another owner).
func (m *PitchModel) UpdatePitch(ctx context.Context, id int, actor auth.Actor, req UpdatePitchRequest) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// surface/format are enum types (pitch_surface / pitch_format), so the THEN
	// branch must cast the text param to the enum — otherwise CASE cannot unify
	// the THEN (text) and ELSE (enum) branch types. The WHEN guard keeps the cast
	// off the empty-string "leave unchanged" path.
	const setCols = `
		name           = CASE WHEN $%d <> '' THEN $%d ELSE name END,
		neighborhood   = CASE WHEN $%d <> '' THEN $%d ELSE neighborhood END,
		surface        = CASE WHEN $%d <> '' THEN $%d::pitch_surface ELSE surface END,
		format         = CASE WHEN $%d <> '' THEN $%d::pitch_format ELSE format END,
		price_per_hour = CASE WHEN $%d > 0   THEN $%d ELSE price_per_hour END`

	// description rides the same scoped UPDATE but is set unconditionally (so an
	// empty value clears it), as its own trailing placeholder after the CASE cols.
	// maps_url rides the same scoped UPDATE via COALESCE($n, maps_url): a nil
	// pointer (field absent) binds NULL → keeps the existing value; a non-nil
	// pointer (incl. "") overwrites, so an empty string clears the link.
	var row pgx.Row
	if actor.IsAdmin() {
		row = m.DB.QueryRow(ctx, fmt.Sprintf(
			`UPDATE pitches SET `+setCols+`, description = $7, maps_url = COALESCE($8, maps_url) WHERE id = $1 AND deleted_at IS NULL RETURNING %s`,
			2, 2, 3, 3, 4, 4, 5, 5, 6, 6, pitchReturnCols,
		), id, req.Name, req.Neighborhood, req.Surface, req.Format, req.PricePerHour, req.Description, req.MapsURL)
	} else {
		row = m.DB.QueryRow(ctx, fmt.Sprintf(
			`UPDATE pitches SET `+setCols+`, description = $8, maps_url = COALESCE($9, maps_url) WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL RETURNING %s`,
			3, 3, 4, 4, 5, 5, 6, 6, 7, 7, pitchReturnCols,
		), id, actor.UserID, req.Name, req.Neighborhood, req.Surface, req.Format, req.PricePerHour, req.Description, req.MapsURL)
	}

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SetPitchActive flips a pitch's is_active flag (the activate/deactivate toggle),
// scoped to the actor (owner → only their own; admin → any), in a single
// transaction. A missing/foreign/soft-deleted pitch yields pgx.ErrNoRows → 404.
//
// Idempotent: setting is_active to its current value succeeds but performs NO
// write and writes NO audit row (so repeated calls don't pile up audit entries).
// On an actual change it updates the row and writes a pitch_audit_log entry
// ('activated' / 'deactivated') attributing the real actor.
//
// Deactivation only removes the pitch from player-facing surfaces — it does NOT
// touch existing bookings (no future-booking guard, no cascade).
func (m *PitchModel) SetPitchActive(ctx context.Context, id int, actor auth.Actor, isActive bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("SetPitchActive: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Resolve + lock with the ownership predicate.
	var current bool
	if actor.IsAdmin() {
		err = tx.QueryRow(ctx,
			`SELECT is_active FROM pitches WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			id,
		).Scan(&current)
	} else {
		err = tx.QueryRow(ctx,
			`SELECT is_active FROM pitches WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, actor.UserID,
		).Scan(&current)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgx.ErrNoRows // → 404 (not found OR not owned)
		}
		return fmt.Errorf("SetPitchActive: resolve: %w", err)
	}

	// Idempotent no-op: already in the desired state → succeed without a write or
	// a duplicate audit row.
	if current == isActive {
		return tx.Commit(ctx)
	}

	if _, err = tx.Exec(ctx,
		`UPDATE pitches SET is_active = $2 WHERE id = $1`, id, isActive,
	); err != nil {
		return fmt.Errorf("SetPitchActive: update: %w", err)
	}

	action := "deactivated"
	if isActive {
		action = "activated"
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO pitch_audit_log (pitch_id, actor_id, actor_role, action)
		VALUES ($1, $2, $3, $4)
	`, id, actor.UserID, actor.Role, action); err != nil {
		return fmt.Errorf("SetPitchActive: audit: %w", err)
	}

	return tx.Commit(ctx)
}

// SetPitchImage persists a new image (URL + Cloudinary public_id) on a pitch,
// scoped to the actor (owner → only their own; admin → any) and only while the
// pitch is live (deleted_at IS NULL), in a single transaction. It returns the
// PREVIOUS public_id so the caller can destroy the replaced Cloudinary asset
// (best-effort, outside this transaction). A missing / foreign / soft-deleted
// pitch yields pgx.ErrNoRows → 404, never leaking another owner's resource.
//
// Passing empty strings clears the image (used by the "remove image" path); the
// returned previous public_id still drives cleanup of the asset being removed.
func (m *PitchModel) SetPitchImage(ctx context.Context, id int, actor auth.Actor, imageURL, publicID string) (oldPublicID string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("SetPitchImage: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Resolve + lock with the ownership predicate, capturing the current public_id
	// (the asset we may need to destroy after the swap).
	if actor.IsAdmin() {
		err = tx.QueryRow(ctx,
			`SELECT image_public_id FROM pitches WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			id,
		).Scan(&oldPublicID)
	} else {
		err = tx.QueryRow(ctx,
			`SELECT image_public_id FROM pitches WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, actor.UserID,
		).Scan(&oldPublicID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", pgx.ErrNoRows // → 404 (not found OR not owned)
		}
		return "", fmt.Errorf("SetPitchImage: resolve: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`UPDATE pitches SET image_url = $2, image_public_id = $3 WHERE id = $1`,
		id, imageURL, publicID,
	); err != nil {
		return "", fmt.Errorf("SetPitchImage: update: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("SetPitchImage: commit: %w", err)
	}

	// Only report a previous asset worth destroying when it actually changed.
	if oldPublicID == publicID {
		return "", nil
	}
	return oldPublicID, nil
}

// SoftDeletePitch soft-deletes pitch `id` on behalf of the actor, in a single
// transaction:
//
//  1. Resolve + lock the pitch with the ownership predicate (owner → only their
//     own; admin → any). A missing/foreign/already-deleted row yields
//     pgx.ErrNoRows → 404, never leaking another owner's resource.
//  2. Guard: if the pitch has future confirmed bookings (status='confirmed' and
//     the slot has not yet ended), abort with ErrPitchHasFutureBookings and the
//     blocking count. Nothing is modified.
//  3. Set deleted_at = now() (the pitch row is preserved, so bookings.pitch_id
//     FK references stay intact — no 23503 violation, history retained).
//  4. Write a pitch_audit_log entry (actor, pitch_id, 'deleted') in the same TX.
//
// On the guard path it returns (count, ErrPitchHasFutureBookings); on success
// (0, nil).
func (m *PitchModel) SoftDeletePitch(ctx context.Context, id int, actor auth.Actor) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("SoftDeletePitch: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Resolve + lock with the ownership predicate.
	var resolvedID int
	if actor.IsAdmin() {
		err = tx.QueryRow(ctx,
			`SELECT id FROM pitches WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			id,
		).Scan(&resolvedID)
	} else {
		err = tx.QueryRow(ctx,
			`SELECT id FROM pitches WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, actor.UserID,
		).Scan(&resolvedID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, pgx.ErrNoRows // → 404 (not found OR not owned)
		}
		return 0, fmt.Errorf("SoftDeletePitch: resolve: %w", err)
	}

	// 2. Future-confirmed-booking guard. "Future" = the slot has not yet ended
	//    (upper(booking_range) > now); covers both upcoming and in-progress
	//    bookings. booking_range is a tstzrange (true instants), so we compare
	//    against the UTC now passed as a ::timestamptz — timezone-independent.
	now := time.Now().UTC()
	var futureCount int
	if err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM bookings
		WHERE pitch_id = $1
		  AND status = 'confirmed'
		  AND upper(booking_range) > $2::timestamptz
	`, id, now).Scan(&futureCount); err != nil {
		return 0, fmt.Errorf("SoftDeletePitch: count future bookings: %w", err)
	}
	if futureCount > 0 {
		return futureCount, ErrPitchHasFutureBookings
	}

	// 3. Soft delete.
	if _, err = tx.Exec(ctx,
		`UPDATE pitches SET deleted_at = now() WHERE id = $1`, id,
	); err != nil {
		return 0, fmt.Errorf("SoftDeletePitch: mark deleted: %w", err)
	}

	// 4. Audit. actor_id is the authenticated principal (admin or owner).
	if _, err = tx.Exec(ctx, `
		INSERT INTO pitch_audit_log (pitch_id, actor_id, actor_role, action)
		VALUES ($1, $2, $3, 'deleted')
	`, id, actor.UserID, actor.Role); err != nil {
		return 0, fmt.Errorf("SoftDeletePitch: audit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("SoftDeletePitch: commit: %w", err)
	}
	return 0, nil
}

func (m *PitchModel) CreatePitch(ctx context.Context, req CreatePitchRequest) (*Pitch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	hue := pitchHues[req.OwnerID%len(pitchHues)]

	row := m.DB.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO pitches
			(owner_id, name, neighborhood, surface, format, price_per_hour,
			 rating, review_count, is_featured, pitch_hue, amenities,
			 latitude, longitude, image_url, image_public_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, false, $7, '{}', 0, 0, $8, $9, $10)
		RETURNING %s
	`, pitchReturnCols),
		req.OwnerID, req.Name, req.Neighborhood, req.Surface, req.Format,
		req.PricePerHour, hue, req.ImageURL, req.ImagePublicID, req.Description,
	)

	p, err := scanPitch(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
