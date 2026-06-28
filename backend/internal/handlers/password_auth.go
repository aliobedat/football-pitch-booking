package handlers

// Phone + password authentication for dashboard roles (owner/admin/staff).
// This is the admin login path: it does NOT use OTP/SMS and shares
// NOTHING with the player phone-first OTP flow (request-otp/verify-otp), which
// stays intact for later re-enablement. A session is minted ONLY on a correct
// phone + password; phone alone, a wrong/missing password, a NULL password_hash,
// or a player-role account all fail closed with a generic 401 (no field-level
// oracle). The minted role is read from the DB row — correct here because the
// verified password is the proof of identity.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// PasswordLoginStore is the narrow persistence the password-login handler needs.
// repository.AuthRepository satisfies it; tests substitute an in-memory fake.
type PasswordLoginStore interface {
	// FindLoginByPhone returns the user + bcrypt password_hash (empty when NULL),
	// or repository.ErrUserNotFound. It makes no authorization decision.
	FindLoginByPhone(ctx context.Context, phone string) (*models.User, string, error)
	// StoreRefreshToken persists the SHA-256 hash of a newly issued refresh token.
	StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error
}

// passwordLoginRoles is the allow-list for this endpoint: the dashboard cohort
// that the production user_role enum actually supports — {owner, admin, staff}.
// "guard" maps to the staff role in this codebase (owner-provisioned operator).
// A player is categorically barred — it can never obtain an admin session here.
// NOTE: super_admin is intentionally NOT listed — it is not a value of the
// production user_role enum (player|owner|admin|staff). Adding it would require a
// separate, explicit enum migration first.
var passwordLoginRoles = map[string]struct{}{
	auth.RoleOwner: {},
	auth.RoleAdmin: {},
	auth.RoleStaff: {},
}

// PasswordAuthHandler serves POST /auth/password-login.
type PasswordAuthHandler struct {
	store      PasswordLoginStore
	jwtManager *auth.JWTManager
	cfg        *config.Config
	limiter    LoginRateLimiter
}

// NewPasswordAuthHandler wires the handler. limiter may be nil to disable rate
// limiting (not recommended outside tests).
func NewPasswordAuthHandler(store PasswordLoginStore, jwtManager *auth.JWTManager, cfg *config.Config, limiter LoginRateLimiter) *PasswordAuthHandler {
	return &PasswordAuthHandler{store: store, jwtManager: jwtManager, cfg: cfg, limiter: limiter}
}

type passwordLoginRequest struct {
	// Neither field is binding:"required": a missing password must yield a generic
	// 401 (credential failure), NOT a 400 that reveals which field was absent.
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// PasswordLogin authenticates a dashboard-role user by phone + password and mints
// the same httpOnly-cookie session the OTP path issues.
func (h *PasswordAuthHandler) PasswordLogin(c *gin.Context) {
	var req passwordLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Malformed JSON (not a credential decision) — generic bad request.
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "could not read the request"})
		return
	}

	// Normalise first so the limiter key + lookup use the canonical phone. An
	// unparseable phone is a credential failure (generic 401), not an oracle.
	phone, err := normalizePhone(strings.TrimSpace(req.Phone))
	if err != nil {
		h.unauthorized(c)
		return
	}

	// Per-phone brute-force gate — checked BEFORE any password work so a locked
	// key cannot keep probing. Distinct 429 (back-off signal), not a credential oracle.
	if h.limiter != nil && !h.limiter.Allow(phone) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "too_many_attempts", "message": "too many attempts, please try again later",
		})
		return
	}

	// Phone alone is never enough: an empty password fails closed.
	if strings.TrimSpace(req.Password) == "" {
		h.fail(c, phone)
		return
	}

	user, hash, err := h.store.FindLoginByPhone(c.Request.Context(), phone)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			h.fail(c, phone) // unknown phone → generic 401 (no user-enumeration oracle)
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not process the request, please try again",
		})
		return
	}

	// Only dashboard roles may use this endpoint; a player (or any non-listed role)
	// is rejected with the same generic 401 — never granted an admin session.
	if _, ok := passwordLoginRoles[string(user.Role)]; !ok {
		h.fail(c, phone)
		return
	}

	// A roled user with no provisioned password (NULL/empty hash) cannot log in.
	if hash == "" {
		h.fail(c, phone)
		return
	}

	// Constant-time bcrypt comparison; the cost is encoded in the stored hash.
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		h.fail(c, phone)
		return
	}

	// Success — clear the attempt counter and mint the roled session.
	if h.limiter != nil {
		h.limiter.Reset(phone)
	}

	resp, err := h.issueTokenPair(c, user)
	if err != nil {
		return // issueTokenPair already wrote the error response
	}
	c.JSON(http.StatusOK, gin.H{"message": "login successful", "data": resp})
}

// fail records a failed attempt and writes the single generic 401 used for EVERY
// credential failure (wrong/missing password, unknown phone, null hash, bad role)
// so the response never reveals which check failed.
func (h *PasswordAuthHandler) fail(c *gin.Context, phone string) {
	if h.limiter != nil {
		h.limiter.Fail(phone)
	}
	h.unauthorized(c)
}

func (h *PasswordAuthHandler) unauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": "invalid_credentials", "message": "invalid phone or password",
	})
}

// issueTokenPair mirrors the OTP path's token issuance: generate an access token
// (role from the DB row), generate + persist a refresh token, mint CSRF, and
// deliver all three as cookies via the shared package helpers. On any error it
// writes the HTTP response itself and returns a non-nil error.
func (h *PasswordAuthHandler) issueTokenPair(c *gin.Context, user *models.User) (*authResponse, error) {
	accessToken, err := h.jwtManager.GenerateAccessToken(user.ID, string(user.Role))
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not issue access token"})
		return nil, err
	}

	rawRefresh, refreshHash, err := h.jwtManager.GenerateRefreshToken()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not issue refresh token"})
		return nil, err
	}

	csrfToken, err := newCSRFToken()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not establish a secure session"})
		return nil, err
	}

	refreshExpiresAt := time.Now().Add(h.cfg.JWT.RefreshExpiry)
	if err = h.store.StoreRefreshToken(c.Request.Context(), user.ID, refreshHash, refreshExpiresAt); err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not store session"})
		return nil, err
	}

	issueSessionCookies(c, h.cfg, accessToken, rawRefresh, csrfToken, string(user.Role))

	return &authResponse{
		ExpiresIn: int(h.cfg.JWT.AccessExpiry.Seconds()),
		User:      user.Safe(),
	}, nil
}
