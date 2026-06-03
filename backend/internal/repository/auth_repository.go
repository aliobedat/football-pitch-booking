package repository

// AuthRepository is the persistence seam for the phone-first auth flow (PART 3B).
// It is deliberately separate from UserRepository (the email/password flow): the
// two share the users table but exercise different columns and lifecycles. A
// phone-first user is born from a verified phone number alone — no password, no
// name, no email — and grants opt-in consent before any OTP is dispatched.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/models"
)

// AuthRepository persists phone-first identities, their OTP opt-in consent, and
// the refresh tokens issued once a phone is verified.
type AuthRepository interface {
	// SetOptIn records the recipient's explicit opt-in consent for AUTHENTICATION
	// (OTP) messages, creating a minimal unverified user row for the phone if one
	// does not yet exist. It is called BEFORE dispatching a code so the
	// notification opt-in gate (which reads users.opt_in via HasOptedIn) can see
	// the consent.
	SetOptIn(ctx context.Context, phone string, optIn bool) error

	// HasOptedIn reports whether the phone has granted opt-in consent. It backs
	// the notification.OptInChecker. An unknown phone reports false (not an error).
	HasOptedIn(ctx context.Context, phone string) (bool, error)

	// SetOptOut records (or clears) an explicit consent WITHDRAWAL for a user by
	// id. Opting out also clears opt_in in the same write, so a withdrawal fully
	// revokes AUTHENTICATION consent rather than leaving a stale grant behind.
	// Returns ErrUserNotFound if no user has that id.
	SetOptOut(ctx context.Context, userID int, optOut bool) error

	// HasOptedOut reports whether the phone has withdrawn consent. It backs the
	// notification.OptOutChecker and gates EVERY message kind. An unknown phone
	// reports false (not an error).
	HasOptedOut(ctx context.Context, phone string) (bool, error)

	// EnsureVerifiedUser returns the user for phone, creating one with
	// phone_verified = true if it does not exist and flipping the flag if it does.
	// Called after a successful OTP verification.
	EnsureVerifiedUser(ctx context.Context, phone string) (*models.User, error)

	// FindByID loads a user by primary key, surfacing nullable phone-first
	// columns as empty strings. Returns ErrUserNotFound when no row matches.
	// Backs GET /auth/me (cookie-session rehydration).
	FindByID(ctx context.Context, userID int) (*models.User, error)

	// StoreRefreshToken persists the SHA-256 hash of a newly issued refresh token.
	StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error
}

type authRepo struct {
	db *pgxpool.Pool
}

// NewAuthRepository constructs a Postgres-backed AuthRepository.
func NewAuthRepository(db *pgxpool.Pool) AuthRepository {
	return &authRepo{db: db}
}

// SetOptIn upserts the phone's user row, setting opt_in. A brand-new phone gets
// a minimal 'player' row (no email/name/password, phone_verified left false);
// an existing row only has its consent flag updated — verification status and
// profile data are untouched.
func (r *authRepo) SetOptIn(ctx context.Context, phone string, optIn bool) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO users (phone, role, opt_in)
		VALUES ($1, 'player', $2)
		ON CONFLICT (phone) DO UPDATE SET
			opt_in     = EXCLUDED.opt_in,
			updated_at = NOW()
	`, phone, optIn)
	if err != nil {
		return fmt.Errorf("SetOptIn: %w", err)
	}
	return nil
}

// HasOptedIn reads users.opt_in for phone. A missing row means no consent.
func (r *authRepo) HasOptedIn(ctx context.Context, phone string) (bool, error) {
	var optIn bool
	err := r.db.QueryRow(ctx,
		`SELECT opt_in FROM users WHERE phone = $1`, phone,
	).Scan(&optIn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("HasOptedIn: %w", err)
	}
	return optIn, nil
}

// SetOptOut sets users.opt_out for the given user id. Opting out also clears
// opt_in so a withdrawal fully revokes consent. A missing user id is reported as
// ErrUserNotFound so the handler can map it to a 404 rather than silently no-op.
func (r *authRepo) SetOptOut(ctx context.Context, userID int, optOut bool) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE users
		SET    opt_out    = $2,
		       opt_in     = CASE WHEN $2 THEN FALSE ELSE opt_in END,
		       updated_at = NOW()
		WHERE  id = $1
	`, userID, optOut)
	if err != nil {
		return fmt.Errorf("SetOptOut: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// HasOptedOut reads users.opt_out for phone. A missing row means no withdrawal.
func (r *authRepo) HasOptedOut(ctx context.Context, phone string) (bool, error) {
	var optOut bool
	err := r.db.QueryRow(ctx,
		`SELECT opt_out FROM users WHERE phone = $1`, phone,
	).Scan(&optOut)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("HasOptedOut: %w", err)
	}
	return optOut, nil
}

// EnsureVerifiedUser upserts the phone's user row as verified and returns it.
// The COALESCE on the nullable phone-first columns surfaces a NULL as an empty
// string so the model never holds a NULL.
func (r *authRepo) EnsureVerifiedUser(ctx context.Context, phone string) (*models.User, error) {
	var u models.User
	err := r.db.QueryRow(ctx, `
		INSERT INTO users (phone, role, phone_verified)
		VALUES ($1, 'player', TRUE)
		ON CONFLICT (phone) DO UPDATE SET
			phone_verified = TRUE,
			updated_at     = NOW()
		RETURNING id, COALESCE(full_name,''), COALESCE(email,''), COALESCE(phone,''),
		          role, created_at, updated_at
	`, phone).Scan(
		&u.ID, &u.FullName, &u.Email, &u.Phone,
		&u.Role, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("EnsureVerifiedUser: %w", err)
	}
	return &u, nil
}

// FindByID loads a user by id. The COALESCE on the nullable phone-first columns
// surfaces a NULL as an empty string so the model never holds a NULL.
func (r *authRepo) FindByID(ctx context.Context, userID int) (*models.User, error) {
	var u models.User
	err := r.db.QueryRow(ctx, `
		SELECT id, COALESCE(full_name,''), COALESCE(email,''), COALESCE(phone,''),
		       role, created_at, updated_at
		FROM   users
		WHERE  id = $1
	`, userID).Scan(
		&u.ID, &u.FullName, &u.Email, &u.Phone,
		&u.Role, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("FindByID: %w", err)
	}
	return &u, nil
}

// StoreRefreshToken persists a hashed refresh token associated with a user.
func (r *authRepo) StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("StoreRefreshToken: %w", err)
	}
	return nil
}
