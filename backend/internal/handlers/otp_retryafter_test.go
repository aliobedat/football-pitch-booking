package handlers

// HTTP-contract test: a throttled OTP request returns 429 WITH a Retry-After
// header (whole seconds). Exercises the full handler path including the
// RateLimitError → setRetryAfter mapping. Uses the in-memory OTP service from the
// shared harness (DefaultConfig: 60s resend cooldown), so a back-to-back resend
// for the same phone trips the cooldown.

import (
	"net/http"
	"strconv"
	"testing"
)

func TestRequestOTP_RateLimited_SetsRetryAfter(t *testing.T) {
	h := newHarness(t)

	body := map[string]any{"phone": "+962790000123", "opt_in": true}

	// First request succeeds and delivers a code.
	if rec := h.do(t, http.MethodPost, "/auth/request-otp", body, ""); rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Immediate resend is inside the cooldown → 429 with a Retry-After header.
	rec := h.do(t, http.MethodPost, "/auth/request-otp", body, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resend status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("missing Retry-After header on 429")
	}
	secs, err := strconv.Atoi(ra)
	if err != nil {
		t.Fatalf("Retry-After = %q, want integer seconds: %v", ra, err)
	}
	if secs < 1 || secs > 60 {
		t.Errorf("Retry-After = %d, want within (0, 60] for a 60s cooldown", secs)
	}
}
