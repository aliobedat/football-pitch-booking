package handlers

// PART 3B: phone-first authentication over HTTP. Two endpoints expose the OTP
// service — request a code, then exchange a correct code for a session. All
// delivery flows through the injected notification.OtpService (Fake/SMS/WhatsApp
// behind it); this layer never talks to a provider. On a successful verification
// a user is materialised (phone_verified = true) and issued the same access +
// refresh token pair as the email flow, so the existing RequireAuth middleware,
// /auth/refresh, and /auth/logout work unchanged for phone users.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/otp"
)

// e164Regex mirrors the users_phone_e164_chk DB constraint: a leading '+', a
// non-zero country-code digit, then up to 14 more digits (15 total max).
var e164Regex = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// defaultCountryCode is prepended to local-format numbers that omit one. Malaeb
// launches in Jordan (+962); see CLAUDE.md (app-layer normalisation).
const defaultCountryCode = "962"

// PhoneAuthStore is the persistence the phone-auth handler needs. It is the
// consumer-side view of repository.AuthRepository, kept narrow so tests can
// substitute an in-memory fake.
type PhoneAuthStore interface {
	SetOptIn(ctx context.Context, phone string, optIn bool) error
	EnsureVerifiedUser(ctx context.Context, phone string) (*models.User, error)
	StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error
}

// PhoneAuthHandler serves the OTP request/verify endpoints.
type PhoneAuthHandler struct {
	otp        notification.OtpService
	store      PhoneAuthStore
	jwtManager *auth.JWTManager
	cfg        *config.Config
}

// NewPhoneAuthHandler wires the handler with its collaborators.
func NewPhoneAuthHandler(otpSvc notification.OtpService, store PhoneAuthStore, jwtManager *auth.JWTManager, cfg *config.Config) *PhoneAuthHandler {
	return &PhoneAuthHandler{
		otp:        otpSvc,
		store:      store,
		jwtManager: jwtManager,
		cfg:        cfg,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Request / Response types
// ─────────────────────────────────────────────────────────────────────────────

type requestOTPRequest struct {
	Phone string `json:"phone" binding:"required"`
	// OptIn is a pointer so a missing field (nil) is distinguishable from an
	// explicit false. binding:"required" rejects only the missing case — an
	// explicit false is allowed through and then refused by the opt-in gate.
	OptIn *bool `json:"opt_in" binding:"required"`
}

type verifyOTPRequest struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code"  binding:"required"`
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/request-otp
// ─────────────────────────────────────────────────────────────────────────────

// RequestOTP captures opt-in consent for the phone, then asks the OTP service to
// generate and dispatch a code. Consent is persisted BEFORE dispatch so the
// notification opt-in gate can verify it; a request without consent is refused
// by that gate (mapped to 403). The response never echoes the code.
func (h *PhoneAuthHandler) RequestOTP(c *gin.Context) {
	var req requestOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_phone", "message": err.Error(),
		})
		return
	}

	// Persist consent first so the opt-in gate inside the notification service
	// sees it. Storing an explicit false is intentional — it records that the
	// user declined, and the dispatch below will be refused.
	if err := h.store.SetOptIn(c.Request.Context(), phone, *req.OptIn); err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not process request, please try again",
		})
		return
	}

	// Carry the caller IP so the service can rate-limit per source IP without
	// changing the OtpService interface (see otp.WithIP).
	ctx := otp.WithIP(c.Request.Context(), c.ClientIP())
	if err := h.otp.Request(ctx, phone); err != nil {
		h.writeRequestOTPError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "a verification code has been sent",
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/auth/verify-otp
// ─────────────────────────────────────────────────────────────────────────────

// VerifyOTP checks the supplied code. On success it materialises the verified
// user (creating it if this is a first login) and issues an access + refresh
// token pair.
func (h *PhoneAuthHandler) VerifyOTP(c *gin.Context) {
	var req verifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_phone", "message": err.Error(),
		})
		return
	}

	ok, err := h.otp.Verify(c.Request.Context(), strings.TrimSpace(phone), strings.TrimSpace(req.Code))
	if err != nil {
		h.writeVerifyOTPError(c, err)
		return
	}
	if !ok {
		// Defensive: a well-behaved OtpService returns an error on every failure,
		// but never trust a bare false.
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_code", "message": "the code is invalid or has expired",
		})
		return
	}

	user, err := h.store.EnsureVerifiedUser(c.Request.Context(), phone)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not complete sign-in, please try again",
		})
		return
	}

	resp, err := h.issueTokenPair(c, user)
	if err != nil {
		return // issueTokenPair has already written the error response
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "phone verified",
		"data":    resp,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// issueTokenPair mirrors AuthHandler.issueTokenPair: generate an access token,
// generate + persist a refresh token, and assemble the auth response. On any
// error it writes the HTTP response itself and returns a non-nil error.
func (h *PhoneAuthHandler) issueTokenPair(c *gin.Context, user *models.User) (*authResponse, error) {
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
	if err = h.store.StoreRefreshToken(c.Request.Context(), user.ID, refreshHash, refreshExpiresAt); err != nil {
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

// writeRequestOTPError maps the OTP/notification sentinels from Request onto
// precise HTTP responses without ever revealing the code.
func (h *PhoneAuthHandler) writeRequestOTPError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, notification.ErrOptInRequired):
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "opt_in_required",
			"message": "consent (opt_in) is required before a verification code can be sent",
		})
	case errors.Is(err, otp.ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "rate_limited", "message": "too many requests, please try again later",
		})
	case errors.Is(err, otp.ErrResendTooSoon):
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "resend_too_soon", "message": "a code was just sent, please wait before requesting another",
		})
	case errors.Is(err, otp.ErrInvalidPhone):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_phone", "message": "a valid phone number is required",
		})
	default:
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not send a verification code, please try again",
		})
	}
}

// writeVerifyOTPError maps the OTP sentinels from Verify onto HTTP responses.
// Missing / expired / mismatched codes collapse to a single 401 so the response
// does not reveal which precise failure occurred (avoids oracle leakage),
// while lockout surfaces as 429 so the client knows to back off.
func (h *PhoneAuthHandler) writeVerifyOTPError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, otp.ErrTooManyAttempts):
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "too_many_attempts", "message": "too many incorrect attempts, request a new code",
		})
	case errors.Is(err, otp.ErrCodeNotFound),
		errors.Is(err, otp.ErrCodeExpired),
		errors.Is(err, otp.ErrCodeMismatch):
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_code", "message": "the code is invalid or has expired",
		})
	case errors.Is(err, otp.ErrInvalidPhone):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_phone", "message": "a valid phone number is required",
		})
	default:
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not verify the code, please try again",
		})
	}
}

// normalizePhone coerces a user-supplied phone into canonical E.164, matching
// the DB CHECK constraint. Rules:
//   - strip spaces, dashes, parentheses;
//   - a leading "00" international prefix becomes "+";
//   - a leading "+" is kept as-is;
//   - a leading "0" (local trunk prefix) is replaced by the default country code;
//   - a bare national number gets the default country code prepended.
//
// The result is validated against the E.164 pattern; an unparseable number is
// rejected so a bad value never reaches the OTP store or notification channel.
func normalizePhone(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("phone number is required")
	}

	// Drop common separators.
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	s = replacer.Replace(s)

	switch {
	case strings.HasPrefix(s, "+"):
		// already international
	case strings.HasPrefix(s, "00"):
		s = "+" + strings.TrimPrefix(s, "00")
	case strings.HasPrefix(s, "0"):
		s = "+" + defaultCountryCode + strings.TrimPrefix(s, "0")
	default:
		s = "+" + defaultCountryCode + s
	}

	if !e164Regex.MatchString(s) {
		return "", errors.New("phone number must be a valid E.164 number (e.g. +9627XXXXXXXX)")
	}
	return s, nil
}
