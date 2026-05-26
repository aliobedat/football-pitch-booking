package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrUserNotFound      = errors.New("user: not found")
	ErrEmailTaken        = errors.New("user: email address is already registered")
	ErrRefreshTokenInvalid = errors.New("user: refresh token is invalid or expired")
)

const pgUniqueViolation = "23505"

// ─────────────────────────────────────────────────────────────────────────────
// Interface
// ─────────────────────────────────────────────────────────────────────────────

type UserRepository interface {
	// FindByEmail returns the full user row including password_hash.
	// Used exclusively by the login flow — never exposed in responses.
	FindByEmail(ctx context.Context, email string) (*models.User, error)

	// CreateUser inserts a new user and returns the safe public record.
	CreateUser(ctx context.Context, params CreateUserParams) (*models.User, error)

	// StoreRefreshToken persists the SHA-256 hash of a new refresh token.
	StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error

	// FindAndConsumeRefreshToken looks up a token hash, validates it is not
	// expired or revoked, marks it as revoked (one-time use), and returns
	// the associated user. All in a single transaction.
	FindAndConsumeRefreshToken(ctx context.Context, tokenHash string) (*models.User, error)

	// RevokeAllUserRefreshTokens invalidates every active refresh token for a
	// user — used on logout-all-devices or after a password change.
	RevokeAllUserRefreshTokens(ctx context.Context, userID int) error
}

// CreateUserParams carries validated data for user creation.
type CreateUserParams struct {
	FullName     string
	Email        string
	Phone        string
	PasswordHash string
	Role         models.UserRole
}

// ─────────────────────────────────────────────────────────────────────────────
// Implementation
// ─────────────────────────────────────────────────────────────────────────────

type userRepo struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) UserRepository {
	return &userRepo{db: db}
}

// FindByEmail retrieves the full user record by email.
// Email is normalised to lowercase before querying — matches the storage format.
func (r *userRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User

	err := r.db.QueryRow(ctx, `
		SELECT id, full_name, email, phone, password_hash, role, created_at, updated_at
		FROM   users
		WHERE  email = $1
	`, strings.ToLower(strings.TrimSpace(email))).Scan(
		&u.ID, &u.FullName, &u.Email, &u.Phone,
		&u.PasswordHash, &u.Role,
		&u.CreatedAt, &u.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("FindByEmail: %w", err)
	}

	return &u, nil
}

// CreateUser inserts a new user within a transaction.
// The email uniqueness constraint on the DB is the ultimate guard;
// we translate the violation into a typed sentinel for the handler.
func (r *userRepo) CreateUser(ctx context.Context, params CreateUserParams) (*models.User, error) {
	var u models.User

	err := r.db.QueryRow(ctx, `
		INSERT INTO users (full_name, email, phone, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, full_name, email, COALESCE(phone,''), password_hash, role, created_at, updated_at
	`,
		params.FullName,
		strings.ToLower(strings.TrimSpace(params.Email)), // normalise on write
		params.Phone,
		params.PasswordHash,
		string(params.Role),
	).Scan(
		&u.ID, &u.FullName, &u.Email, &u.Phone,
		&u.PasswordHash, &u.Role,
		&u.CreatedAt, &u.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("CreateUser: %w", err)
	}

	return &u, nil
}

// StoreRefreshToken persists a hashed refresh token associated with a user.
func (r *userRepo) StoreRefreshToken(
	ctx       context.Context,
	userID    int,
	tokenHash string,
	expiresAt time.Time,
) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt)

	if err != nil {
		return fmt.Errorf("StoreRefreshToken: %w", err)
	}
	return nil
}

// FindAndConsumeRefreshToken validates and atomically revokes a refresh token.
//
// Atomic revocation prevents refresh token replay attacks: if a token is
// stolen and used by an attacker first, the legitimate user's subsequent
// use will fail, alerting them to the compromise.
func (r *userRepo) FindAndConsumeRefreshToken(
	ctx       context.Context,
	tokenHash string,
) (*models.User, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("FindAndConsumeRefreshToken: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the refresh token row and validate it in one query
	var tokenID int
	var userID int

	err = tx.QueryRow(ctx, `
		SELECT id, user_id
		FROM   refresh_tokens
		WHERE  token_hash  = $1
		  AND  revoked     = FALSE
		  AND  expires_at  > NOW()
		FOR UPDATE
	`, tokenHash).Scan(&tokenID, &userID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenInvalid
		}
		return nil, fmt.Errorf("FindAndConsumeRefreshToken: find token: %w", err)
	}

	// Revoke the token — one-time use enforced
	if _, err = tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1`,
		tokenID,
	); err != nil {
		return nil, fmt.Errorf("FindAndConsumeRefreshToken: revoke: %w", err)
	}

	// Fetch the associated user
	var u models.User
	err = tx.QueryRow(ctx, `
		SELECT id, full_name, email, COALESCE(phone,''), password_hash, role, created_at, updated_at
		FROM   users
		WHERE  id = $1
	`, userID).Scan(
		&u.ID, &u.FullName, &u.Email, &u.Phone,
		&u.PasswordHash, &u.Role,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("FindAndConsumeRefreshToken: fetch user: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("FindAndConsumeRefreshToken: commit: %w", err)
	}

	return &u, nil
}

// RevokeAllUserRefreshTokens invalidates all active tokens for a user.
// Call this on: logout-all-devices, password change, account suspension.
func (r *userRepo) RevokeAllUserRefreshTokens(ctx context.Context, userID int) error {
	_, err := r.db.Exec(ctx, `
		UPDATE refresh_tokens
		SET    revoked = TRUE
		WHERE  user_id  = $1
		  AND  revoked  = FALSE
	`, userID)

	if err != nil {
		return fmt.Errorf("RevokeAllUserRefreshTokens: %w", err)
	}
	return nil
}