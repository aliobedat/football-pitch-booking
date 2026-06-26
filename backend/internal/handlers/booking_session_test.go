package handlers

// MVP no-OTP booking unblock — tests for POST /auth/booking-session. The endpoint
// mints a player session from name + JO phone WITHOUT an OTP, but ONLY while
// BOOKING_OTP_REQUIRED is false; it still enforces ValidateJOMobile, and it never
// touches the owner/staff login path.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
)

// TestBookingSession_FlagOff_ValidJOMobile_CreatesSession: a valid JO mobile with
// the flag off yields a session (200 + httpOnly access cookie), no OTP involved.
func TestBookingSession_FlagOff_ValidJOMobile_CreatesSession(t *testing.T) {
	h := newHarness(t) // cfg.BookingOTPRequired defaults false
	rec := h.do(t, http.MethodPost, "/auth/booking-session",
		map[string]any{"phone": "0791234567", "full_name": "لاعب تجريبي"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) == nil {
		t.Error("no access cookie set; want a minted session")
	}
}

// TestBookingSession_FlagOff_Landline_Rejected: the JO-mobile rule still applies.
func TestBookingSession_FlagOff_Landline_Rejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/booking-session",
		map[string]any{"phone": "+96265001234", "full_name": "X"}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set for a rejected number; want none")
	}
}

// TestBookingSession_FlagOn_Refused: when OTP is required, the no-OTP shortcut is
// closed (403) and no session is minted — booking must go through OTP instead.
func TestBookingSession_FlagOn_Refused(t *testing.T) {
	store := newFakeAuthStore()
	jwtManager := auth.NewJWTManager(testJWTSecret, 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{
		BookingOTPRequired: true,
		JWT: config.JWTConfig{
			Secret: testJWTSecret, AccessExpiry: 15 * time.Minute, RefreshExpiry: 168 * time.Hour,
		},
	}
	h := NewPhoneAuthHandler(nil, store, jwtManager, cfg)
	r := gin.New()
	r.POST("/auth/booking-session", h.CreateBookingSession)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/booking-session", nil)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (flag on closes the shortcut); body=%s", rec.Code, rec.Body.String())
	}
}
