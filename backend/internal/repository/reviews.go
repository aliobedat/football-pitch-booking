package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/models"
)

// maskReviewerName reduces a full name to a privacy-preserving public form:
// first token kept whole, every following token collapsed to its initial letter
// plus ".". "Ahmad Khaled" → "Ahmad K." and "أحمد خالد" → "أحمد خ.". Initials are
// taken by RUNE so Arabic/RTL letters are not split mid-codepoint. An empty or
// whitespace-only name returns "" (caller decides on a fallback label).
func maskReviewerName(full string) string {
	fields := strings.Fields(full)
	if len(fields) == 0 {
		return ""
	}
	out := make([]string, 0, len(fields))
	out = append(out, fields[0])
	for _, tok := range fields[1:] {
		r := []rune(tok)
		if len(r) == 0 {
			continue
		}
		out = append(out, string(r[0])+".")
	}
	return strings.Join(out, " ")
}

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors — handlers map these onto HTTP status codes.
// ─────────────────────────────────────────────────────────────────────────────

var (
	// ErrAlreadyReviewed maps to 409. Raised when the partial unique index
	// uq_review_player_pitch rejects a second live review for the same (player,
	// pitch) pair.
	ErrAlreadyReviewed = errors.New("review: player has already reviewed this pitch")

	// ErrReviewNotFound maps to 404 (or 403 on ownership-scoped updates where we
	// deliberately do not reveal existence).
	ErrReviewNotFound = errors.New("review: review does not exist")

	// ErrReviewBookingInvalid maps to 422/403. Raised when the (booking, player,
	// pitch) triple fails the composite FK — i.e. the qualifying booking does not
	// belong to this player+pitch.
	ErrReviewBookingInvalid = errors.New("review: qualifying booking does not match player and pitch")
)

// Postgres SQLSTATE codes we branch on.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// ─────────────────────────────────────────────────────────────────────────────
// Repository contract
// ─────────────────────────────────────────────────────────────────────────────

// ReviewRepository is the data-access surface for the Verified Review System.
// All reads are implicitly scoped to live (deleted_at IS NULL) rows unless the
// method name says otherwise.
type ReviewRepository interface {
	CreateReview(ctx context.Context, req models.CreateReviewRequest) (*models.Review, error)
	GetPitchReviews(ctx context.Context, pitchID int64, limit, offset int) ([]models.Review, error)
	GetPitchRatingAggregates(ctx context.Context, pitchID int64) (models.RatingAggregate, error)
	GetReviewByID(ctx context.Context, id int64) (*models.Review, error)
	UpdateReview(ctx context.Context, id int64, rating int16, comment *string) (*models.Review, error)
	SoftDeleteReview(ctx context.Context, id int64) error
	FlagReview(ctx context.Context, id int64) error

	// CheckEligibility runs the Derived check (qualifying booking + existing
	// review + owner-exclusion) for (playerID, pitchID).
	CheckEligibility(ctx context.Context, playerID, pitchID int64) (models.ReviewEligibility, error)
}

type reviewRepo struct {
	db *pgxpool.Pool
}

func NewReviewRepository(db *pgxpool.Pool) ReviewRepository {
	return &reviewRepo{db: db}
}

// reviewColumns is the canonical live-row projection shared by the read paths.
const reviewColumns = `
	id, pitch_id, player_id, booking_id, rating, comment,
	is_flagged, created_at, updated_at, deleted_at`

func scanReview(row pgx.Row) (*models.Review, error) {
	var rv models.Review
	if err := row.Scan(
		&rv.ID, &rv.PitchID, &rv.PlayerID, &rv.BookingID, &rv.Rating, &rv.Comment,
		&rv.IsFlagged, &rv.CreatedAt, &rv.UpdatedAt, &rv.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &rv, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CreateReview
// ─────────────────────────────────────────────────────────────────────────────

func (r *reviewRepo) CreateReview(ctx context.Context, req models.CreateReviewRequest) (*models.Review, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO reviews (pitch_id, player_id, booking_id, rating, comment)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING`+reviewColumns,
		req.PitchID, req.PlayerID, req.QualifyingBookingID, req.Rating, req.Comment,
	)

	rv, err := scanReview(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case pgUniqueViolation:
				// Only one unique target can fire here: uq_review_player_pitch.
				return nil, ErrAlreadyReviewed
			case pgForeignKeyViolation:
				// fk_reviews_booking_triple: booking ≠ this player+pitch.
				return nil, ErrReviewBookingInvalid
			}
		}
		return nil, fmt.Errorf("CreateReview: insert: %w", err)
	}
	return rv, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Reads
// ─────────────────────────────────────────────────────────────────────────────

// GetPitchReviews returns live reviews for a pitch, newest first, paginated.
func (r *reviewRepo) GetPitchReviews(ctx context.Context, pitchID int64, limit, offset int) ([]models.Review, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.db.Query(ctx, `
		SELECT
			rv.id, rv.pitch_id, rv.player_id, rv.booking_id, rv.rating, rv.comment,
			rv.is_flagged, rv.created_at, rv.updated_at,
			COALESCE(u.full_name, '')
		FROM reviews rv
		LEFT JOIN users u ON u.id = rv.player_id
		WHERE rv.pitch_id = $1 AND rv.deleted_at IS NULL
		ORDER BY rv.created_at DESC
		LIMIT $2 OFFSET $3`,
		pitchID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("GetPitchReviews: query: %w", err)
	}
	defer rows.Close()

	reviews := make([]models.Review, 0, limit)
	for rows.Next() {
		var rv models.Review
		if err := rows.Scan(
			&rv.ID, &rv.PitchID, &rv.PlayerID, &rv.BookingID, &rv.Rating, &rv.Comment,
			&rv.IsFlagged, &rv.CreatedAt, &rv.UpdatedAt, &rv.ReviewerName,
		); err != nil {
			return nil, fmt.Errorf("GetPitchReviews: scan: %w", err)
		}
		// PII masking: the raw full_name must never leave the server.
		rv.ReviewerName = maskReviewerName(rv.ReviewerName)
		reviews = append(reviews, rv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetPitchReviews: rows: %w", err)
	}
	return reviews, nil
}

// GetPitchRatingAggregates returns only AVG(rating) and COUNT(*) over live rows.
// AVG is NULL for a pitch with no reviews; COALESCE flattens it to 0 alongside a
// 0 count so callers can branch on Count.
func (r *reviewRepo) GetPitchRatingAggregates(ctx context.Context, pitchID int64) (models.RatingAggregate, error) {
	var agg models.RatingAggregate
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(AVG(rating), 0)::float8, COUNT(*)
		FROM reviews
		WHERE pitch_id = $1 AND deleted_at IS NULL`,
		pitchID,
	).Scan(&agg.Average, &agg.Count)
	if err != nil {
		return models.RatingAggregate{}, fmt.Errorf("GetPitchRatingAggregates: %w", err)
	}
	return agg, nil
}

// GetReviewByID fetches a single live review (used by the ownership check on
// update and by the eligibility probe). Returns ErrReviewNotFound when absent.
func (r *reviewRepo) GetReviewByID(ctx context.Context, id int64) (*models.Review, error) {
	row := r.db.QueryRow(ctx, `
		SELECT`+reviewColumns+`
		FROM reviews
		WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	rv, err := scanReview(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReviewNotFound
		}
		return nil, fmt.Errorf("GetReviewByID: %w", err)
	}
	return rv, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Mutations
// ─────────────────────────────────────────────────────────────────────────────

// UpdateReview edits rating/comment and explicitly bumps updated_at. Ownership
// is enforced by the handler (review.player_id == caller) BEFORE this is called;
// the WHERE still guards deleted_at so a soft-deleted review can't be revived.
func (r *reviewRepo) UpdateReview(ctx context.Context, id int64, rating int16, comment *string) (*models.Review, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE reviews
		SET rating = $2, comment = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING`+reviewColumns,
		id, rating, comment,
	)
	rv, err := scanReview(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReviewNotFound
		}
		return nil, fmt.Errorf("UpdateReview: %w", err)
	}
	return rv, nil
}

// SoftDeleteReview is the admin moderation action: stamp deleted_at, freeing the
// (player, pitch) live-uniqueness slot. Idempotent on already-deleted rows via
// the deleted_at IS NULL guard (a second call → ErrReviewNotFound).
func (r *reviewRepo) SoftDeleteReview(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE reviews
		SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("SoftDeleteReview: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReviewNotFound
	}
	return nil
}

// FlagReview marks a live review as reported. Policy: flagged reviews stay
// VISIBLE until an admin soft-deletes them, so this only flips the flag.
func (r *reviewRepo) FlagReview(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE reviews
		SET is_flagged = true, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("FlagReview: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReviewNotFound
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Eligibility (Derived check)
// ─────────────────────────────────────────────────────────────────────────────

// CheckEligibility implements the Derived rule:
//   - owner-exclusion: a pitch owner can never review their own pitch;
//   - qualifying booking: most-recent non-cancelled booking that has already
//     ended (upper(booking_range) < now());
//   - existing review: the player's current live review for this pitch, if any.
//
// Eligible is true iff a qualifying booking exists AND the caller is not the
// owner. ExistingReview being non-nil drives Write-vs-Edit on the client; it is
// returned even when re-eligibility is moot so the UI can prefill the edit form.
func (r *reviewRepo) CheckEligibility(ctx context.Context, playerID, pitchID int64) (models.ReviewEligibility, error) {
	var result models.ReviewEligibility

	// Owner-exclusion: fetch the pitch owner. A missing/soft-deleted pitch yields
	// no owner row → not eligible (no booking could qualify anyway).
	var ownerID *int64
	err := r.db.QueryRow(ctx, `
		SELECT owner_id FROM pitches WHERE id = $1 AND deleted_at IS NULL`,
		pitchID,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, nil // pitch gone → not eligible
		}
		return result, fmt.Errorf("CheckEligibility: owner lookup: %w", err)
	}
	if ownerID != nil && *ownerID == playerID {
		// Self-review barred: not eligible, no qualifying booking surfaced.
		return result, nil
	}

	// Qualifying booking — exact query mandated by the spec.
	var bookingID int64
	err = r.db.QueryRow(ctx, `
		SELECT id FROM bookings
		WHERE player_id = $1 AND pitch_id = $2
		  AND status <> 'cancelled'
		  AND upper(booking_range) < now()
		ORDER BY upper(booking_range) DESC
		LIMIT 1`,
		playerID, pitchID,
	).Scan(&bookingID)
	switch {
	case err == nil:
		result.Eligible = true
		result.QualifyingBookingID = &bookingID
	case errors.Is(err, pgx.ErrNoRows):
		result.Eligible = false
	default:
		return result, fmt.Errorf("CheckEligibility: qualifying booking: %w", err)
	}

	// Existing live review for this (player, pitch).
	existing, err := r.getLiveReviewForPlayerPitch(ctx, playerID, pitchID)
	if err != nil {
		return result, err
	}
	result.ExistingReview = existing

	return result, nil
}

func (r *reviewRepo) getLiveReviewForPlayerPitch(ctx context.Context, playerID, pitchID int64) (*models.Review, error) {
	row := r.db.QueryRow(ctx, `
		SELECT`+reviewColumns+`
		FROM reviews
		WHERE player_id = $1 AND pitch_id = $2 AND deleted_at IS NULL`,
		playerID, pitchID,
	)
	rv, err := scanReview(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getLiveReviewForPlayerPitch: %w", err)
	}
	return rv, nil
}
