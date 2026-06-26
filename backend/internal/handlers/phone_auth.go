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
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/otp"
	phonepkg "github.com/ali/football-pitch-api/internal/phone"
	"github.com/ali/football-pitch-api/internal/repository"
)

// Phone normalisation lives in the shared internal/phone package (Cockpit WO1) so
// the handler, staff binding, and CRM customer backfill apply the identical
// E.164/+962 identity rule.

// PhoneAuthStore is the persistence the phone-auth handler needs. It is the
// consumer-side view of repository.AuthRepository, kept narrow so tests can
// substitute an in-memory fake.
type PhoneAuthStore interface {
	SetOptIn(ctx context.Context, phone string, optIn bool) error
	EnsureVerifiedUser(ctx context.Context, phone string) (*models.User, error)
	StoreRefreshToken(ctx context.Context, userID int, tokenHash string, expiresAt time.Time) error
	// FindByID loads the user behind the current session cookie, backing
	// GET /auth/me so the client can rehydrate without ever reading a token.
	FindByID(ctx context.Context, userID int) (*models.User, error)
	// UpdateFullName backs PATCH /me (Just-In-Time name capture at checkout).
	UpdateFullName(ctx context.Context, userID int, fullName string) (*models.User, error)
}

// Full-name bounds for JIT capture, counted in runes so Arabic/RTL names are
// measured by character, not byte.
const (
	minFullNameRunes = 2
	maxFullNameRunes = 100
)

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
	// Purpose discriminates the caller's flow. The player BOOKING flow sends
	// "booking", which opts THIS request into the strict Jordan-mobile rule
	// (phone.ValidateJOMobile). Any other value — including the absent default
	// used by the shared owner/staff login flow — keeps the looser Normalize-only
	// rule, so login behaviour is unchanged. Never required.
	Purpose string `json:"purpose"`
}

// purposeBooking is the requestOTPRequest.Purpose value the player booking flow
// sends to opt into strict Jordan-mobile validation.
const purposeBooking = "booking"

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

	// Strict Jordan-mobile gate — BOOKING flow ONLY (delta D). Scoped to
	// purpose="booking" so the shared owner/staff login path keeps the looser
	// Normalize-only rule; the rule is never applied without this explicit flag.
	if req.Purpose == purposeBooking {
		if err := phonepkg.ValidateJOMobile(phone); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   "invalid_phone",
				"message": "يرجى إدخال رقم هاتف أردني محمول صحيح",
			})
			return
		}
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
// GET /api/v1/auth/me
// ─────────────────────────────────────────────────────────────────────────────

// GetCurrentUser returns the authenticated user's profile. RequireAuth resolves
// the session from the httpOnly access cookie and injects the user id; this
// endpoint lets the client rehydrate its in-memory user on page load without
// ever holding a token itself.
func (h *PhoneAuthHandler) GetCurrentUser(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "message": "authentication is required",
		})
		return
	}

	user, err := h.store.FindByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "user_not_found", "message": "no user exists for this session",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load your profile, please try again",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": user.Safe()})
}

// updateMeRequest is the bound body for PATCH /me. Only full_name is mutable
// here (JIT name capture); the user id comes from the session, never the body.
type updateMeRequest struct {
	FullName string `json:"full_name"`
}

// PatchMe sets the authenticated user's full_name (Just-In-Time capture at
// checkout). Strict BOLA: the target id is read from the session only — there is
// no path/body id to tamper with. Validation: trimmed, non-empty, 2–100 runes
// (Arabic/RTL allowed). This endpoint deliberately does NOT touch booking logic.
func (h *PhoneAuthHandler) PatchMe(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "message": "authentication is required",
		})
		return
	}

	var req updateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "bad_request", "message": "صيغة الطلب غير صحيحة",
		})
		return
	}

	name := strings.TrimSpace(req.FullName)
	if n := utf8.RuneCountInString(name); n < minFullNameRunes || n > maxFullNameRunes {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "invalid_full_name",
			"message": "الاسم يجب أن يكون بين 2 و100 حرف",
		})
		return
	}

	user, err := h.store.UpdateFullName(c.Request.Context(), userID, name)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "user_not_found", "message": "no user exists for this session",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not update your profile, please try again",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": user.Safe()})
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

	csrfToken, err := newCSRFToken()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not establish a secure session",
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

	// Deliver the JWTs as httpOnly cookies plus the readable CSRF token; the
	// response body carries no token.
	issueSessionCookies(c, h.cfg, accessToken, rawRefresh, csrfToken, string(user.Role))

	return &authResponse{
		ExpiresIn: int(h.cfg.JWT.AccessExpiry.Seconds()),
		User:      user.Safe(),
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
		setRetryAfter(c, err)
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "rate_limited", "message": "too many requests, please try again later",
		})
	case errors.Is(err, otp.ErrResendTooSoon):
		setRetryAfter(c, err)
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

// setRetryAfter sets the Retry-After header (whole seconds, rounded up, min 1)
// when the rate-limit error carries a hint, so a throttled client knows how long
// to back off. It never leaks anything beyond the delay.
func setRetryAfter(c *gin.Context, err error) {
	var rl *otp.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		return
	}
	secs := max(int(math.Ceil(rl.RetryAfter.Seconds())), 1)
	c.Header("Retry-After", strconv.Itoa(secs))
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
// normalizePhone delegates to the shared phone.Normalize (Cockpit WO1 extraction)
// so the handler, staff binding, and CRM backfill share one identity rule.
func normalizePhone(raw string) (string, error) {
	return phonepkg.Normalize(raw)
}
