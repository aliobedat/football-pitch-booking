package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sentinel errors — typed so middleware can switch on them precisely.
var (
	ErrTokenExpired   = errors.New("token has expired")
	ErrTokenInvalid   = errors.New("token is invalid")
	ErrTokenMalformed = errors.New("token is malformed")
)

// tokenType distinguishes access tokens from refresh tokens at the claims level.
// This prevents a refresh token from being accepted as an access token by the
// RequireAuth middleware (token type confusion attack).
type tokenType string

const (
	tokenTypeAccess  tokenType = "access"
	tokenTypeRefresh tokenType = "refresh"
)

// Claims is the JWT payload for مالعب tokens.
//
// Wire contract (Dashboard PR 2): the access token serialises the standard
// registered `sub` (the user id, as a string) plus `role` and `exp`. This is the
// exact { sub, role, exp } shape the frontend @malaab/shared client decodes for
// UX routing — there is deliberately NO `scope` claim; scope is resolved
// per-request from the DB server-side (see middleware.ResolveScope). `uid` is
// retained as the numeric id the backend reads directly off the struct, and
// `typ` guards against access/refresh token-type confusion.
type Claims struct {
	UserID    int       `json:"uid"`
	Role      string    `json:"role"`
	TokenType tokenType `json:"typ"`
	jwt.RegisteredClaims
}

// JWTManager holds the signing key and expiry configuration.
// It is constructed once at startup and injected into handlers and middleware.
type JWTManager struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

// NewJWTManager constructs a JWTManager from config values.
func NewJWTManager(secret string, accessExpiry, refreshExpiry time.Duration) *JWTManager {
	return &JWTManager{
		secret:        []byte(secret),
		accessExpiry:  accessExpiry,
		refreshExpiry: refreshExpiry,
	}
}

// GenerateAccessToken issues a short-lived signed JWT for API authentication.
// The token embeds user_id, role, and token type — sufficient for RBAC decisions
// without a database lookup on every request.
func (m *JWTManager) GenerateAccessToken(userID int, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Role:      role,
		TokenType: tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			// Subject carries the user id as the standard `sub` claim so the
			// frontend shared client reads { sub, role, exp } off the token.
			Subject:   strconv.Itoa(userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessExpiry)),
			// Issuer and Audience can be added when deploying multiple services
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("GenerateAccessToken: sign: %w", err)
	}
	return signed, nil
}

// GenerateRefreshToken creates a cryptographically random opaque token.
// The raw token is returned to the client (stored in an httpOnly cookie).
// The SHA-256 hash of the raw token is stored in the database —
// so a database breach does not expose usable refresh tokens.
//
// Returns: rawToken (for client), tokenHash (for DB storage)
func (m *JWTManager) GenerateRefreshToken() (rawToken string, tokenHash string, err error) {
	b := make([]byte, 32) // 256 bits of entropy
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("GenerateRefreshToken: rand: %w", err)
	}

	rawToken = hex.EncodeToString(b)
	tokenHash = hashRefreshToken(rawToken)
	return rawToken, tokenHash, nil
}

// ValidateAccessToken parses and validates a signed JWT string.
// It explicitly rejects tokens with type != "access" to prevent
// refresh tokens from being used as access tokens.
func (m *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	return m.validateToken(tokenString, tokenTypeAccess)
}

// HashRefreshToken computes the SHA-256 hash of a raw refresh token.
// Exposed as a package-level function so the repository can compute the hash
// when looking up a token presented by the client, without needing the manager.
func HashRefreshToken(raw string) string {
	return hashRefreshToken(raw)
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

func (m *JWTManager) validateToken(tokenString string, expectedType tokenType) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			// Explicitly verify the signing algorithm.
			// Without this check, an attacker could craft a token signed with
			// the "none" algorithm and bypass signature verification entirely.
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
	)

	if err != nil {
		// Map jwt library errors to our own sentinel types so middleware
		// can produce precise HTTP responses without importing the jwt package.
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, ErrTokenExpired
		case errors.Is(err, jwt.ErrTokenMalformed):
			return nil, ErrTokenMalformed
		default:
			return nil, ErrTokenInvalid
		}
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	// Token type confusion guard
	if claims.TokenType != expectedType {
		return nil, ErrTokenInvalid
	}

	return claims, nil
}

func hashRefreshToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
