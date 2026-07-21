package otp

// WO-SECURITY-V1 PR-S1 regression: proves that when the rate limiter fails
// closed with ErrRateLimiterBusy, Service.Request never reaches the paid
// notification provider, and the error surfaced to the caller remains the
// neutral, retryable ErrRateLimiterBusy sentinel (no internal details).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// busyLimiter always reports the bucket as busy, regardless of key/max/window,
// simulating an advisory-lock timeout without needing a live database.
type busyLimiter struct{}

func (busyLimiter) Allow(context.Context, string, int, time.Duration, time.Time) (bool, error) {
	return false, ErrRateLimiterBusy
}

func TestRequest_RateLimiterBusy_NeverInvokesProvider(t *testing.T) {
	fake := notification.NewFakeChannel(notification.FakeSilent())
	checker := notification.OptInFunc(func(context.Context, string) (bool, error) { return true, nil })
	notifier := notification.NewService(
		notification.ChannelFake,
		notification.WithChannel(notification.ChannelFake, fake),
		notification.WithOptInChecker(checker),
	)

	store := NewMemoryStore()
	hasher, err := NewHMACHasher(testSecret)
	if err != nil {
		t.Fatalf("NewHMACHasher: %v", err)
	}

	svc := New(notifier, store, busyLimiter{}, hasher, DefaultConfig())

	err = svc.Request(context.Background(), testPhone)
	if err == nil {
		t.Fatal("Request succeeded, want a rate-limiter-busy failure")
	}
	if !errors.Is(err, ErrRateLimiterBusy) {
		t.Fatalf("Request error = %v, want errors.Is(err, ErrRateLimiterBusy)", err)
	}

	if fake.Count() != 0 {
		t.Fatalf("fake (paid-provider stand-in) recorded %d sends, want 0 — the provider must never be invoked when the limiter fails closed", fake.Count())
	}
}
