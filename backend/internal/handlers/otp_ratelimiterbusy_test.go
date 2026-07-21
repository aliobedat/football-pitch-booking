package handlers

// WO-SECURITY-V1 PR-S1 HTTP-contract regression: when the OTP rate limiter
// fails closed with otp.ErrRateLimiterBusy (the advisory-lock-timeout path),
// POST /auth/request-otp must respond 429 with Retry-After: 1 and a body that
// reveals no internal database/lock detail.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/otp"
)

// busyLimiter always reports the bucket as busy, standing in for a real
// advisory-lock timeout without needing a live database in this HTTP test.
type busyLimiter struct{}

func (busyLimiter) Allow(context.Context, string, int, time.Duration, time.Time) (bool, error) {
	return false, otp.ErrRateLimiterBusy
}

func TestRequestOTP_RateLimiterBusy_Returns429NoInternalDetail(t *testing.T) {
	store := newFakeAuthStore()
	fake := notification.NewFakeChannel(notification.FakeSilent())
	notifier := notification.NewService(
		notification.ChannelFake,
		notification.WithChannel(notification.ChannelFake, fake),
		notification.WithOptInChecker(notification.OptInFunc(store.HasOptedIn)),
	)

	hasher, err := otp.NewHMACHasher(testPepper)
	if err != nil {
		t.Fatalf("NewHMACHasher: %v", err)
	}

	mem := otp.NewMemoryStore()
	otpSvc := otp.New(notifier, mem, busyLimiter{}, hasher, otp.DefaultConfig())

	jwtManager := auth.NewJWTManager(testJWTSecret, 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret:        testJWTSecret,
		AccessExpiry:  15 * time.Minute,
		RefreshExpiry: 168 * time.Hour,
	}}

	h := NewPhoneAuthHandler(otpSvc, store, jwtManager, cfg)
	r := gin.New()
	r.POST("/auth/request-otp", h.RequestOTP)

	hh := &harness{router: r, fake: fake, store: store, jwt: jwtManager}

	rec := hh.do(t, http.MethodPost, "/auth/request-otp", map[string]any{
		"phone": "+962790000999", "opt_in": true,
	}, "")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "1" {
		t.Fatalf("Retry-After = %q, want %q", ra, "1")
	}

	body := rec.Body.String()
	forbidden := []string{
		"otp_rate_events", "postgres", "advisory", "lock_timeout", "55P03",
		"pg_advisory", "sql", "database", "hashtextextended",
	}
	lower := strings.ToLower(body)
	for _, term := range forbidden {
		if strings.Contains(lower, strings.ToLower(term)) {
			t.Fatalf("response body leaks internal detail %q: %s", term, body)
		}
	}

	if fake.Count() != 0 {
		t.Fatalf("fake (paid-provider stand-in) recorded %d sends, want 0", fake.Count())
	}
}
