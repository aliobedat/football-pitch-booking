package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// AuthHandler handles registration, login, token refresh, and logout.
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

// ─────────────────────────────────────────────────────────────────────────────
// Request / Response types (local to this file)
// ─────────────────────────────────────────────────────────────────────────────

type registerRequest struct {
	FullName string `json:"full_name" binding:"required"`
	Email    string `json:"email"     binding:"required"`
	Password string `json:"password"  binding:"required"`
	Role     string `json:"role"      binding:"required"`
}

type loginRequest struct {
	Email    string `json:"email"    binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type authResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	ExpiresIn    int             `json:"expires_in_seconds"`
	User         models.SafeUser `json:"user"`
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/register
// ─────────────────────────────────────────────────────────────────────────────

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	// ── Input validation ─────────────────────────────────────────────────────
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)

	if validationErrs := validateRegistration(req); len(validationErrs) > 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "validation_failed",
			"fields": validationErrs,
		})
		return
	}

	// ── Hash password ─────────────────────────────────────────────────────────
	hash, err := auth.HashPassword(req.Password, h.cfg.BcryptCost)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "registration failed, please try again",
		})
		return
	}

	// ── Persist user ──────────────────────────────────────────────────────────
	user, err := h.userRepo.CreateUser(c.Request.Context(), repository.CreateUserParams{
		FullName:     req.FullName,
		Email:        req.Email,
		Phone:        "",
		PasswordHash: hash,
		Role:         models.UserRole(req.Role),
	})
	if err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			// Use a generic message — do not confirm the email exists
			c.JSON(http.StatusConflict, gin.H{
				"error":   "registration_failed",
				"message": "an account with that email address already exists",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "registration failed, please try again",
		})
		return
	}

	// ── Issue tokens (auto-login on registration) ─────────────────────────────
	resp, err := h.issueTokenPair(c, user)
	if err != nil {
		return // issueTokenPair writes the error response
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "account created successfully",
		"data":    resp,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/login
// ─────────────────────────────────────────────────────────────────────────────

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// ── Attempt to find user ──────────────────────────────────────────────────
	user, err := h.userRepo.FindByEmail(c.Request.Context(), req.Email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			// User not found — run dummy bcrypt to prevent timing-based
			// email enumeration. An attacker timing this endpoint would see
			// the same ~300ms latency whether or not the email exists.
			auth.DummyVerify()
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_credentials", "message": "invalid email or password",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "login failed, please try again",
		})
		return
	}

	// ── Verify password ───────────────────────────────────────────────────────
	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		// Same message as "not found" — prevents distinguishing the two cases
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_credentials", "message": "invalid email or password",
		})
		return
	}

	// ── Issue tokens ──────────────────────────────────────────────────────────
	resp, err := h.issueTokenPair(c, user)
	if err != nil {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "login successful",
		"data":    resp,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/refresh
// ─────────────────────────────────────────────────────────────────────────────

// Refresh validates a presented refresh token, revokes it (one-time use),
// and issues a fresh access + refresh token pair.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	tokenHash := auth.HashRefreshToken(req.RefreshToken)

	user, err := h.userRepo.FindAndConsumeRefreshToken(c.Request.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenInvalid) {
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
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "token refreshed",
		"data":    resp,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/logout
// ─────────────────────────────────────────────────────────────────────────────

// Logout revokes all refresh tokens for the authenticated user.
// The access token will expire naturally — implement a token denylist
// (e.g., Redis) if immediate access token invalidation is required.
func (h *AuthHandler) Logout(c *gin.Context) {
	userID := c.GetInt("malaab.user_id")

	if err := h.userRepo.RevokeAllUserRefreshTokens(c.Request.Context(), userID); err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "logout failed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// issueTokenPair generates both an access and refresh token for a user,
// persists the refresh token hash, and returns the full auth response.
// On any error it writes the HTTP response itself and returns a non-nil error.
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

	return &authResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(h.cfg.JWT.AccessExpiry.Seconds()),
		User:         user.Safe(),
	}, nil
}

// validateRegistration returns a map of field → error message for invalid inputs.
// Returning all errors at once gives clients a better UX than fail-on-first.
func validateRegistration(req registerRequest) map[string]string {
	errs := make(map[string]string)

	if len(req.FullName) < 2 || len(req.FullName) > 100 {
		errs["full_name"] = "must be between 2 and 100 characters"
	}

	if !emailRegex.MatchString(req.Email) {
		errs["email"] = "must be a valid email address"
	}

	if pwErr := validatePassword(req.Password); pwErr != "" {
		errs["password"] = pwErr
	}

	if req.Role != string(models.RolePlayer) && req.Role != string(models.RoleOwner) {
		errs["role"] = "must be either 'player' or 'owner'"
	}

	return errs
}

// validatePassword enforces password complexity.
// Minimum: 8 characters, 1 uppercase, 1 lowercase, 1 digit.
func validatePassword(pw string) string {
	if len(pw) < 8 {
		return "must be at least 8 characters"
	}
	if len(pw) > 128 {
		return "must not exceed 128 characters"
	}

	var hasUpper, hasLower, hasDigit bool
	for _, ch := range pw {
		switch {
		case unicode.IsUpper(ch):
			hasUpper = true
		case unicode.IsLower(ch):
			hasLower = true
		case unicode.IsDigit(ch):
			hasDigit = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit {
		return "must contain at least one uppercase letter, one lowercase letter, and one digit"
	}
	return ""
}