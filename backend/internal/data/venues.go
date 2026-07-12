package data

// VenueModel — the venues grouping layer above pitches (WO-VENUES / Gate 1b).
// Mirrors the PitchModel layering: owner/admin scoping happens HERE in SQL
// (owner → owner_id = actor; admin → unscoped), cross-tenant reads/writes
// resolve to ErrVenueNotFound (404 — existence never leaked, the pitches
// convention). Reviews stay per-pitch (Gate 0 ruling); venue-level rating is
// AGGREGATED live across the venue's pitches, correlated-subquery style.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ErrVenueNotFound — unknown venue OR a venue outside the caller's scope.
// Both collapse to 404 (existence never leaked).
var ErrVenueNotFound = errors.New("venue not found or not owned")

// ErrSlugTaken — case-insensitive slug collision on create (409 slug_taken).
var ErrSlugTaken = errors.New("venue slug already taken")

// ErrVenueHasPitches — soft-delete refused while non-deleted pitches remain
// (409 venue_has_pitches; a venue is never orphaned out from under its pitches).
var ErrVenueHasPitches = errors.New("venue still has pitches")

// ErrVenueOwnerMismatch — the ownership invariant (pitch.owner_id ==
// venue.owner_id, CLAUDE.md principle 5) would be violated. Surfaced ONLY to
// admins (409 venue_owner_mismatch — visible-but-refused); owners get
// ErrVenueNotFound instead (existence not leaked).
var ErrVenueOwnerMismatch = errors.New("venue belongs to a different owner than the pitch")

// slugPattern mirrors the DB CHECK (migration 033) exactly.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether s satisfies the venues slug CHECK.
func ValidSlug(s string) bool { return slugPattern.MatchString(s) }

// asciiSlugify lowercases and collapses non-alphanumerics to single dashes —
// the same transform as the migration-033 backfill. Returns "" when the name
// is not ASCII-slugifiable (e.g. Arabic).
func asciiSlugify(name string) string {
	for _, r := range name {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// Venue is the canonical venue row. JSON mirrors the pitch conventions
// (lat/lng + snake_case media/link fields).
type Venue struct {
	ID                 int64    `json:"id"`
	OwnerID            int      `json:"owner_id,omitempty"`
	Name               string   `json:"name"`
	Slug               string   `json:"slug"`
	Neighborhood       string   `json:"neighborhood"`
	MapsURL            string   `json:"maps_url"`
	Latitude           float64  `json:"lat"`
	Longitude          float64  `json:"lng"`
	Description        string   `json:"description"`
	CoverImageURL      string   `json:"cover_image_url"`
	CoverImagePublicID string   `json:"cover_image_public_id"`
	IsActive           bool     `json:"isActive"`
	PitchCount         int      `json:"pitchCount"`
	Rating             *float64 `json:"rating"`       // live AVG across the venue's pitches' reviews
	ReviewsCount       int      `json:"reviewsCount"` // live SUM
	MinPricePerHour    *int     `json:"minPricePerHour,omitempty"`
	DistanceKm         *float64 `json:"distance_km,omitempty"`

	// ── WO-1C-PAYLOAD (additive, POPULATED ONLY BY PublicList — the B2C
	//    listing): card-parity aggregates over the venue's ACTIVE, non-deleted
	//    pitches. /venues/:slug and the owner list do not carry them. ─────────
	ImageURL    string   `json:"image_url,omitempty"`   // cover, fallback: first pitch image
	Format      *string  `json:"format,omitempty"`      // uniform across active pitches, else null
	Surface     *string  `json:"surface,omitempty"`     // uniform across active pitches, else null
	Formats     []string `json:"formats,omitempty"`     // DISTINCT set (client any-match filter)
	PriceVaries bool     `json:"price_varies,omitempty"`
}

// VenuePitch is one pitch inside the public venue read — the play-specific
// subset (place-level fields live on the venue).
type VenuePitch struct {
	ID            int      `json:"id"`
	Label         string   `json:"label"`
	Name          string   `json:"name"`
	Surface       string   `json:"surface"`
	Format        string   `json:"format"`
	PricePerHour  int      `json:"pricePerHour"`
	Amenities     []string `json:"amenities"`
	PitchHue      string   `json:"pitchHue"`
	Rating        *float64 `json:"rating"`
	ReviewsCount  int      `json:"reviewsCount"`
	ImageURL      string   `json:"image_url"`
	ImagePublicID string   `json:"image_public_id"`
}

// PublicVenue is the GET /venues/:slug payload: the venue + its bookable pitches.
type PublicVenue struct {
	Venue
	Pitches []VenuePitch `json:"pitches"`
}

type CreateVenueRequest struct {
	Name               string  `json:"name"           binding:"required"`
	Slug               string  `json:"slug"           binding:"required"`
	Neighborhood       string  `json:"neighborhood"   binding:"required"`
	MapsURL            string  `json:"maps_url"       binding:"required"`
	Latitude           float64 `json:"lat"`
	Longitude          float64 `json:"lng"`
	Description        string  `json:"description"`
	CoverImageURL      string  `json:"cover_image_url"`
	CoverImagePublicID string  `json:"cover_image_public_id"`
	OwnerID            int     `json:"-"`
}

// UpdateVenueRequest — every editable field EXCEPT slug (immutable) and owner.
type UpdateVenueRequest struct {
	Name               string  `json:"name"           binding:"required"`
	Neighborhood       string  `json:"neighborhood"   binding:"required"`
	MapsURL            string  `json:"maps_url"       binding:"required"`
	Latitude           float64 `json:"lat"`
	Longitude          float64 `json:"lng"`
	Description        string  `json:"description"`
	CoverImageURL      string  `json:"cover_image_url"`
	CoverImagePublicID string  `json:"cover_image_public_id"`
}

type VenueModel struct {
	DB *pgxpool.Pool
}

// venueCols — canonical SELECT list; requires alias v. rating/reviews are
// live aggregates over the venue's non-deleted pitches' non-deleted reviews;
// pitch_count counts non-deleted pitches; min price spans active ones.
const venueCols = `
	v.id, COALESCE(v.owner_id, 0), v.name, v.slug, v.neighborhood, v.maps_url,
	COALESCE(v.latitude, 0), v.longitude,
	COALESCE(v.description, ''), COALESCE(v.cover_image_url, ''), COALESCE(v.cover_image_public_id, ''),
	v.is_active,
	(SELECT COUNT(*)::int FROM pitches p WHERE p.venue_id = v.id AND p.deleted_at IS NULL),
	(SELECT ROUND(AVG(r.rating), 1)::double precision
	   FROM reviews r JOIN pitches p ON p.id = r.pitch_id
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND r.deleted_at IS NULL),
	(SELECT COUNT(r.id)::int
	   FROM reviews r JOIN pitches p ON p.id = r.pitch_id
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND r.deleted_at IS NULL),
	(SELECT MIN(p.price_per_hour)::int FROM pitches p
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true)
`

func scanVenue(row pgx.Row) (Venue, error) {
	var v Venue
	err := row.Scan(&v.ID, &v.OwnerID, &v.Name, &v.Slug, &v.Neighborhood, &v.MapsURL,
		&v.Latitude, &v.Longitude, &v.Description, &v.CoverImageURL, &v.CoverImagePublicID,
		&v.IsActive, &v.PitchCount, &v.Rating, &v.ReviewsCount, &v.MinPricePerHour)
	return v, err
}

// isSlugUniqueViolation matches the venues_slug_lower_unique index (23505).
func isSlugUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "slug")
}

func (m *VenueModel) Create(ctx context.Context, req CreateVenueRequest) (*Venue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var id int64
	err := m.DB.QueryRow(ctx, `
		INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url, latitude, longitude,
		                    description, cover_image_url, cover_image_public_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,''), NULLIF($9,''), NULLIF($10,''))
		RETURNING id
	`, req.OwnerID, req.Name, req.Slug, req.Neighborhood, req.MapsURL,
		req.Latitude, req.Longitude, req.Description, req.CoverImageURL, req.CoverImagePublicID).Scan(&id)
	if err != nil {
		if isSlugUniqueViolation(err) {
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("CreateVenue: %w", err)
	}
	return m.getByID(ctx, id)
}

func (m *VenueModel) getByID(ctx context.Context, id int64) (*Venue, error) {
	v, err := scanVenue(m.DB.QueryRow(ctx,
		`SELECT `+venueCols+` FROM venues v WHERE v.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("getVenue: %w", err)
	}
	return &v, nil
}

// OwnerList — owner-scoped (admin unscoped) non-deleted venues with pitch_count.
func (m *VenueModel) OwnerList(ctx context.Context, actor auth.Actor) ([]Venue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clause, args := actor.OwnerScopeFilter("v.owner_id", 1)
	rows, err := m.DB.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM venues v WHERE v.deleted_at IS NULL AND %s ORDER BY v.id`, venueCols, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("OwnerListVenues: %w", err)
	}
	defer rows.Close()

	out := []Venue{}
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, fmt.Errorf("OwnerListVenues: scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Update edits every venue field EXCEPT slug and owner. Cross-tenant/unknown →
// ErrVenueNotFound.
func (m *VenueModel) Update(ctx context.Context, actor auth.Actor, id int64, req UpdateVenueRequest) (*Venue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clause, scopeArgs := actor.OwnerScopeFilter("owner_id", 10)
	args := append([]any{id, req.Name, req.Neighborhood, req.MapsURL, req.Latitude, req.Longitude,
		req.Description, req.CoverImageURL, req.CoverImagePublicID}, scopeArgs...)
	tag, err := m.DB.Exec(ctx, fmt.Sprintf(`
		UPDATE venues
		SET name = $2, neighborhood = $3, maps_url = $4, latitude = $5, longitude = $6,
		    description = NULLIF($7,''), cover_image_url = NULLIF($8,''), cover_image_public_id = NULLIF($9,''),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND %s`, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("UpdateVenue: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrVenueNotFound
	}
	return m.getByID(ctx, id)
}

// ToggleActive mirrors the pitch active toggle. Cross-tenant → ErrVenueNotFound.
func (m *VenueModel) ToggleActive(ctx context.Context, actor auth.Actor, id int64, active bool) (*Venue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clause, scopeArgs := actor.OwnerScopeFilter("owner_id", 3)
	args := append([]any{id, active}, scopeArgs...)
	tag, err := m.DB.Exec(ctx, fmt.Sprintf(
		`UPDATE venues SET is_active = $2, updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL AND %s`, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("ToggleVenueActive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrVenueNotFound
	}
	return m.getByID(ctx, id)
}

// SoftDelete refuses (ErrVenueHasPitches) while the venue still has non-deleted
// pitches — a venue is never deleted out from under live pitches.
func (m *VenueModel) SoftDelete(ctx context.Context, actor auth.Actor, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("SoftDeleteVenue: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	clause, scopeArgs := actor.OwnerScopeFilter("owner_id", 2)
	// Lock + scope-resolve the venue row.
	var exists bool
	err = tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT true FROM venues WHERE id = $1 AND deleted_at IS NULL AND %s FOR UPDATE`, clause),
		append([]any{id}, scopeArgs...)...).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVenueNotFound
	}
	if err != nil {
		return fmt.Errorf("SoftDeleteVenue: resolve: %w", err)
	}

	var livePitches int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM pitches WHERE venue_id = $1 AND deleted_at IS NULL`, id).Scan(&livePitches); err != nil {
		return fmt.Errorf("SoftDeleteVenue: count: %w", err)
	}
	if livePitches > 0 {
		return ErrVenueHasPitches
	}

	if _, err := tx.Exec(ctx,
		`UPDATE venues SET deleted_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("SoftDeleteVenue: update: %w", err)
	}
	return tx.Commit(ctx)
}

// PublicBySlug — the unauthenticated B2C venue read: active, non-deleted venue
// (404 otherwise) + its active, non-deleted pitches.
func (m *VenueModel) PublicBySlug(ctx context.Context, slug string) (*PublicVenue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	v, err := scanVenue(m.DB.QueryRow(ctx,
		`SELECT `+venueCols+` FROM venues v
		  WHERE lower(v.slug) = lower($1) AND v.deleted_at IS NULL AND v.is_active = true`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVenueNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("PublicVenueBySlug: %w", err)
	}
	v.OwnerID = 0 // public payload never leaks the owner

	rows, err := m.DB.Query(ctx, `
		SELECT p.id, COALESCE(p.label, ''), p.name, p.surface, p.format, p.price_per_hour,
		       p.amenities, p.pitch_hue,
		       ROUND(AVG(r.rating), 1)::double precision, COUNT(r.id)::int,
		       p.image_url, p.image_public_id
		FROM pitches p
		LEFT JOIN reviews r ON r.pitch_id = p.id AND r.deleted_at IS NULL
		WHERE p.venue_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
		GROUP BY p.id
		ORDER BY p.id`, v.ID)
	if err != nil {
		return nil, fmt.Errorf("PublicVenueBySlug: pitches: %w", err)
	}
	defer rows.Close()

	out := &PublicVenue{Venue: v, Pitches: []VenuePitch{}}
	for rows.Next() {
		var p VenuePitch
		if err := rows.Scan(&p.ID, &p.Label, &p.Name, &p.Surface, &p.Format, &p.PricePerHour,
			&p.Amenities, &p.PitchHue, &p.Rating, &p.ReviewsCount,
			&p.ImageURL, &p.ImagePublicID); err != nil {
			return nil, fmt.Errorf("PublicVenueBySlug: scan pitch: %w", err)
		}
		out.Pitches = append(out.Pitches, p)
	}
	return out, rows.Err()
}

// venueListExtraCols — WO-1C-PAYLOAD card-parity aggregates, appended to
// venueCols ONLY on the public listing read. All subqueries scope to the
// venue's ACTIVE, non-deleted pitches (correlated style per house convention).
// format/surface are uniform-or-NULL (the HAVING makes the subquery return
// zero rows — NULL — on a mixed venue); formats is the DISTINCT set for the
// client's any-match filter; image falls back to the first pitch's image when
// the cover is unset (a pitch image uploaded after creation never syncs to
// the venue cover).
const venueListExtraCols = `,
	COALESCE(NULLIF(v.cover_image_url, ''),
	         (SELECT p.image_url FROM pitches p
	           WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true
	           ORDER BY p.id LIMIT 1), ''),
	(SELECT MIN(p.format::text) FROM pitches p
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true
	  HAVING COUNT(DISTINCT p.format) = 1),
	(SELECT MIN(p.surface::text) FROM pitches p
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true
	  HAVING COUNT(DISTINCT p.surface) = 1),
	(SELECT COALESCE(array_agg(DISTINCT p.format::text), '{}') FROM pitches p
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true),
	(SELECT COUNT(DISTINCT p.price_per_hour) > 1 FROM pitches p
	  WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true)
`

// PublicList — one card per active venue (with at least the venue itself
// visible), for the B2C listing page. Carries the WO-1C-PAYLOAD card-parity
// aggregates on top of the canonical venue row.
func (m *VenueModel) PublicList(ctx context.Context) ([]Venue, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := m.DB.Query(ctx,
		`SELECT `+venueCols+venueListExtraCols+` FROM venues v
		  WHERE v.deleted_at IS NULL AND v.is_active = true
		  ORDER BY v.id`)
	if err != nil {
		return nil, fmt.Errorf("PublicListVenues: %w", err)
	}
	defer rows.Close()

	out := []Venue{}
	for rows.Next() {
		var v Venue
		if err := rows.Scan(&v.ID, &v.OwnerID, &v.Name, &v.Slug, &v.Neighborhood, &v.MapsURL,
			&v.Latitude, &v.Longitude, &v.Description, &v.CoverImageURL, &v.CoverImagePublicID,
			&v.IsActive, &v.PitchCount, &v.Rating, &v.ReviewsCount, &v.MinPricePerHour,
			&v.ImageURL, &v.Format, &v.Surface, &v.Formats, &v.PriceVaries); err != nil {
			return nil, fmt.Errorf("PublicListVenues: scan: %w", err)
		}
		v.OwnerID = 0
		out = append(out, v)
	}
	return out, rows.Err()
}
