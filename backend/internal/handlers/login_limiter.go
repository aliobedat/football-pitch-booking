package handlers

import (
	"sync"
	"time"
)

// LoginRateLimiter blunts password brute-force on the phone+password login path.
// It is keyed per identifier (the normalised phone) and is DELIBERATELY separate
// from the OTP rate limiter (internal/otp) — password-login attempts must never
// pollute or be masked by OTP send/verify counters.
//
//   - Allow reports whether another attempt may proceed (false once the cap is hit
//     within the window).
//   - Fail records a failed attempt.
//   - Reset clears the counter (called on a successful login).
type LoginRateLimiter interface {
	Allow(key string) bool
	Fail(key string)
	Reset(key string)
}

// MemoryLoginLimiter is a process-local fixed-window limiter: up to `max` failed
// attempts per `window` per key. A single-instance deployment is assumed (the
// admin login surface is low-volume); a multi-instance deployment would swap this
// for a shared-store implementation behind the same interface. Safe for
// concurrent use.
type MemoryLoginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	state  map[string]*loginAttempts
	// now is injectable so tests can drive the window deterministically; it
	// defaults to time.Now.
	now func() time.Time
}

type loginAttempts struct {
	count       int
	windowStart time.Time
}

// NewMemoryLoginLimiter builds a limiter allowing `max` failed attempts per
// `window`. A non-positive max disables limiting (Allow always true).
func NewMemoryLoginLimiter(max int, window time.Duration) *MemoryLoginLimiter {
	return &MemoryLoginLimiter{
		max:    max,
		window: window,
		state:  make(map[string]*loginAttempts),
		now:    time.Now,
	}
}

// Allow reports whether another attempt for key may proceed. An elapsed window
// resets the counter.
func (l *MemoryLoginLimiter) Allow(key string) bool {
	if l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	a := l.state[key]
	if a == nil {
		return true
	}
	if l.now().Sub(a.windowStart) >= l.window {
		delete(l.state, key) // window elapsed → fresh start
		return true
	}
	return a.count < l.max
}

// Fail records a failed attempt for key, opening a new window if the previous one
// has elapsed.
func (l *MemoryLoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	a := l.state[key]
	if a == nil || now.Sub(a.windowStart) >= l.window {
		l.state[key] = &loginAttempts{count: 1, windowStart: now}
		return
	}
	a.count++
}

// Reset clears any recorded attempts for key (used after a successful login so a
// legitimate user is never locked out by their own earlier typos).
func (l *MemoryLoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, key)
}
