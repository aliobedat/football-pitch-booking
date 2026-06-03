package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// AuthHandler owns the session lifecycle shared by every login path: refreshing
// the token pair and logging out. Identity itself is established exclusively by
// the phone-first OTP flow (see PhoneAuthHandler) — there is no email/password
// authentication.
type AuthHandler struct {
	userRepo   repository.UserRepository
	jwtManager *auth.JWTManager
	cfg        *config.Config
}

func NewAuthHandler(db *pgxpool.Pool, jwtManager *auth.JWTManager, cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		userRepo:   repository.NewUserRepository(db),
		jwtManager: jwtManager,
		cfg:        cfg,
	}
}

// authResponse is the body returned on successful authentication. It carries NO
// tokens: the access + refresh JWTs are delivered exclusively as httpOnly
// cookies (see issueSessionCookies), so they never reach JavaScript. The client
// keeps only the non-sensitive expiry for UX and the user profile for display.
type authResponse struct {
	ExpiresIn int             `json:"expires_in_seconds"`
	User      models.SafeUser `json:"user"`
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/refresh
// ─────────────────────────────────────────────────────────────────────────────

// Refresh validates the presented refresh token (read from its httpOnly
// cookie), revokes it (one-time use), and issues a fresh access + refresh pair.
func (h *AuthHandler) Refresh(c *gin.Context) {
	// The refresh token rides in an httpOnly cookie set at sign-in — never the
	// request body, so it stays out of JavaScript's reach.
	rawRefresh, err := c.Cookie(cookieRefresh)
	if err != nil || rawRefresh == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_token", "message": "refresh token is missing",
		})
		return
	}

	tokenHash := auth.HashRefreshToken(rawRefresh)

	user, err := h.userRepo.FindAndConsumeRefreshToken(c.Request.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenInvalid) {
			// Stale/replayed token: scrub the cookies so the browser stops
			// presenting a dead session.
			clearSessionCookies(c, h.cfg)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "invalid_token",
				"message": "refresh token is invalid or has expired",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "token refresh failed",
		})
		return
	}

	resp, err := h.issueTokenPair(c, user)
	if err != nil {
		return // issueTokenPair writes the error response
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "token refreshed",
		"data":    resp,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/logout
// ─────────────────────────────────────────────────────────────────────────────

// Logout revokes all refresh tokens for the authenticated user and clears the
// session cookies. The access token will expire naturally — implement a token
// denylist if immediate access-token invalidation is required.
func (h *AuthHandler) Logout(c *gin.Context) {
	userID := c.GetInt("malaab.user_id")

	if err := h.userRepo.RevokeAllUserRefreshTokens(c.Request.Context(), userID); err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "logout failed",
		})
		return
	}

	// Expire the session cookies so the browser is left clean.
	clearSessionCookies(c, h.cfg)

	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// issueTokenPair generates an access token, generates + persists a refresh
// token, mints a CSRF token, delivers all three as cookies, and returns the
// token-free auth response. On any error it writes the HTTP response itself and
// returns a non-nil error.
func (h *AuthHandler) issueTokenPair(c *gin.Context, user *models.User) (*authResponse, error) {
	accessToken, err := h.jwtManager.GenerateAccessToken(user.ID, string(user.Role))
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not issue access token",
		})
		return nil, err
	}

	rawRefresh, refreshHash, err := h.jwtManager.GenerateRefreshToken()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not issue refresh token",
		})
		return nil, err
	}

	csrfToken, err := newCSRFToken()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not establish a secure session",
		})
		return nil, err
	}

	refreshExpiresAt := time.Now().Add(h.cfg.JWT.RefreshExpiry)
	if err = h.userRepo.StoreRefreshToken(
		c.Request.Context(), user.ID, refreshHash, refreshExpiresAt,
	); err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not store session",
		})
		return nil, err
	}

	// Deliver the JWTs as httpOnly cookies plus the readable CSRF token; the
	// response body carries no token.
	issueSessionCookies(c, h.cfg, accessToken, rawRefresh, csrfToken, string(user.Role))

	return &authResponse{
		ExpiresIn: int(h.cfg.JWT.AccessExpiry.Seconds()),
		User:      user.Safe(),
	}, nil
}
