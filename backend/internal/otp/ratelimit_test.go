package otp

// Anti-AIT (toll-fraud / SMS-pumping) rate-limit tests for the layered quotas
// added on top of the legacy per-phone/per-IP burst windows: the hourly/daily
// per-phone caps, the per-minute/per-hour per-IP caps, the platform-wide global
// circuit breaker, and the Retry-After hint. Every blocked request must cost
// ZERO messages (the predicate runs upstream of the send).

import (
	"context"
	"errors"
	"testing"
	"time"
)

// isolate strips the legacy burst windows so a single layered cap is under test
// without the 15-minute MaxPerPhone/MaxPerIP windows tripping first.
func isolate(c *Config) {
	c.ResendCooldown = 0
	c.MaxPerPhone = 0
	c.MaxPerIP = 0
	c.MaxPerPhoneHour = 0
	c.MaxPerPhoneDay = 0
	c.MaxPerIPMinute = 0
	c.MaxPerIPHour = 0
	c.MaxGlobalHour = 0
	c.MaxGlobalDay = 0
}

// TestRequest_PerPhoneDayCap: the daily per-phone cap blocks once spent, costs no
// message, and a different phone is unaffected.
func TestRequest_PerPhoneDayCap(t *testing.T) {
	const quota = 3
	h := newHarness(t, true, func(c *Config) { isolate(c); c.MaxPerPhoneDay = quota })
	ctx := context.Background()

	for i := range quota {
		if err := h.svc.Request(ctx, testPhone); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := h.svc.Request(ctx, testPhone); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over-day-cap err = %v, want ErrRateLimited", err)
	}
	if h.fake.Count() != quota {
		t.Errorf("blocked request cost a message: sends = %d, want %d", h.fake.Count(), quota)
	}
	// Cap is per-phone: another number still works.
	if err := h.svc.Request(ctx, "+962790000099"); err != nil {
		t.Errorf("fresh phone err = %v, want nil", err)
	}
}

// TestRequest_PerIPMinuteCap: the per-minute IP cap throttles distinct phones
// from one IP, and lifts after the minute window elapses.
func TestRequest_PerIPMinuteCap(t *testing.T) {
	const quota = 2
	h := newHarness(t, true, func(c *Config) { isolate(c); c.MaxPerIPMinute = quota })
	ctx := WithIP(context.Background(), "203.0.113.50")

	if err := h.svc.Request(ctx, "+962790000001"); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if err := h.svc.Request(ctx, "+962790000002"); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	if err := h.svc.Request(ctx, "+962790000003"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over-IP-minute err = %v, want ErrRateLimited", err)
	}

	// After the minute passes, the window drains and a request is allowed again.
	h.clk.advance(61 * time.Second)
	if err := h.svc.Request(ctx, "+962790000004"); err != nil {
		t.Errorf("after minute window err = %v, want nil", err)
	}
}

// TestRequest_GlobalCircuitBreaker: the platform-wide hourly ceiling trips
// regardless of phone or IP — proving it is a true global backstop — and blocks
// an otherwise-fresh phone/IP at zero message cost.
func TestRequest_GlobalCircuitBreaker(t *testing.T) {
	const ceiling = 3
	h := newHarness(t, true, func(c *Config) { isolate(c); c.MaxGlobalHour = ceiling })

	// Spend the global ceiling across DISTINCT phones AND distinct IPs, so only
	// the global bucket — not any per-phone/per-IP bucket — can be the limiter.
	for i := range ceiling {
		ctx := WithIP(context.Background(), "198.51.100."+string(rune('1'+i)))
		phone := "+96279000010" + string(rune('0'+i))
		if err := h.svc.Request(ctx, phone); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}

	// A brand-new phone from a brand-new IP is still rejected: the breaker is global.
	fresh := WithIP(context.Background(), "198.51.100.250")
	if err := h.svc.Request(fresh, "+962790009999"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("breaker-tripped err = %v, want ErrRateLimited", err)
	}
	if h.fake.Count() != ceiling {
		t.Errorf("breaker-blocked request cost a message: sends = %d, want %d", h.fake.Count(), ceiling)
	}

	// The breaker drains with its window: after an hour, sends resume.
	h.clk.advance(time.Hour + time.Second)
	if err := h.svc.Request(fresh, "+962790009999"); err != nil {
		t.Errorf("after hour window err = %v, want nil", err)
	}
}

// TestRequest_RetryAfter_Cooldown: a too-soon resend returns a RateLimitError
// whose RetryAfter reflects the remaining cooldown and unwraps to ErrResendTooSoon.
func TestRequest_RetryAfter_Cooldown(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.ResendCooldown = 60 * time.Second })
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("first request: %v", err)
	}
	h.clk.advance(20 * time.Second)

	err := h.svc.Request(ctx, testPhone)
	if !errors.Is(err, ErrResendTooSoon) {
		t.Fatalf("err = %v, want ErrResendTooSoon", err)
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err is %T, want *RateLimitError", err)
	}
	// 60s cooldown, 20s elapsed → ~40s remaining.
	if rl.RetryAfter <= 0 || rl.RetryAfter > 40*time.Second {
		t.Errorf("RetryAfter = %v, want (0, 40s]", rl.RetryAfter)
	}
}

// TestRequest_RetryAfter_QuotaWindow: a quota breach carries a RetryAfter sized
// to the tripped window.
func TestRequest_RetryAfter_QuotaWindow(t *testing.T) {
	const quota = 1
	h := newHarness(t, true, func(c *Config) { isolate(c); c.MaxPerPhoneHour = quota })
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	err := h.svc.Request(ctx, testPhone)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err is %T, want *RateLimitError", err)
	}
	if rl.RetryAfter != time.Hour {
		t.Errorf("RetryAfter = %v, want 1h (the tripped window)", rl.RetryAfter)
	}
}

// TestRequest_LayeredOrdering_NoSendOnBlock is a belt-and-braces check that with
// the full DefaultConfig a single request succeeds and is delivered exactly once
// (the layered checks don't accidentally block a legitimate first request).
func TestRequest_LayeredOrdering_FirstRequestSucceeds(t *testing.T) {
	h := newHarness(t, true) // full DefaultConfig: all layers active
	ctx := WithIP(context.Background(), "203.0.113.9")
	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("first request under full config: %v", err)
	}
	if h.fake.Count() != 1 {
		t.Errorf("sends = %d, want 1", h.fake.Count())
	}
}
