package otp

// Storage seams for the OTP service. The service core is storage-agnostic: it
// depends only on these interfaces. PART 3A ships an in-memory implementation
// (memory.go) that satisfies both and is sufficient for the service logic and
// its unit tests; a Postgres-backed implementation (writing users.phone_verified
// and a codes table) plugs in behind the same contracts in a later PART.

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors surfaced by the OTP service. Callers (the PART 3B HTTP layer)
// match these with errors.Is to map outcomes onto responses. They deliberately
// distinguish the failure modes the spec calls out — expiry, wrong code,
// lockout, and rate limiting — without ever revealing the code itself.
var (
	// ErrInvalidPhone means Request/Verify was called with an empty phone.
	ErrInvalidPhone = errors.New("otp: phone is required")
	// ErrRateLimited means the per-phone or per-IP request quota for the
	// current window is exhausted.
	ErrRateLimited = errors.New("otp: too many requests, try again later")
	// ErrResendTooSoon means a code was issued very recently and the resend
	// cooldown has not yet elapsed.
	ErrResendTooSoon = errors.New("otp: a code was just sent, please wait before requesting another")
	// ErrCodeNotFound means there is no active (unexpired, unconsumed) code for
	// the phone — e.g. Verify before Request, or after a successful Verify.
	ErrCodeNotFound = errors.New("otp: no active code for this phone")
	// ErrCodeExpired means the active code's expiry has passed.
	ErrCodeExpired = errors.New("otp: code has expired")
	// ErrTooManyAttempts means the code is locked out after too many failed
	// verification attempts.
	ErrTooManyAttempts = errors.New("otp: too many incorrect attempts")
	// ErrCodeMismatch means the supplied code did not match the stored digest.
	ErrCodeMismatch = errors.New("otp: incorrect code")
	// ErrRateLimiterBusy means the rate limiter's per-bucket advisory lock could
	// not be acquired before its lock_timeout — another request for the same
	// bucket is mid-check. This is a distinct, fail-closed outcome: no event is
	// recorded and the caller must not proceed to generate or dispatch a code.
	ErrRateLimiterBusy = errors.New("otp: rate limiter busy, try again shortly")
)

// RateLimitError is returned by Request when a quota or the resend cooldown is
// hit. It carries a Retry-After hint and unwraps to the matching sentinel
// (ErrRateLimited or ErrResendTooSoon), so existing errors.Is(...) checks keep
// working while the HTTP layer can also read RetryAfter to set the header.
type RateLimitError struct {
	// RetryAfter is a conservative hint for when the caller may try again: the
	// remaining cooldown for a too-soon resend, or the tripped window for a quota.
	RetryAfter time.Duration
	sentinel   error
}

func (e *RateLimitError) Error() string { return e.sentinel.Error() }

// Unwrap lets errors.Is(err, ErrRateLimited) / ErrResendTooSoon match.
func (e *RateLimitError) Unwrap() error { return e.sentinel }

// Code is the persisted state of an active one-time code for a single phone.
// Only the keyed Hash is stored — never the plaintext. Attempts counts FAILED
// verification tries and drives lockout.
type Code struct {
	Phone     string
	Hash      string
	ExpiresAt time.Time
	Attempts  int
	CreatedAt time.Time
}

// Store persists at most one active Code per phone and tracks phone
// verification. Implementations must be safe for concurrent use.
type Store interface {
	// Save upserts the active code for code.Phone, REPLACING any existing one
	// (a resend invalidates the previous code and resets its attempt count).
	Save(ctx context.Context, code Code) error
	// Get returns the active code for phone. The bool is false when none exists.
	Get(ctx context.Context, phone string) (Code, bool, error)
	// Delete removes the active code for phone (no error if absent). Used to
	// invalidate a code on success or expiry.
	Delete(ctx context.Context, phone string) error
	// IncrementAttempts atomically increments and returns the failed-attempt
	// count for phone's active code. It returns ErrCodeNotFound if none exists.
	IncrementAttempts(ctx context.Context, phone string) (int, error)
	// MarkPhoneVerified records that phone has completed verification. In the
	// Postgres implementation this sets users.phone_verified = true.
	MarkPhoneVerified(ctx context.Context, phone string) error
}

// RateLimiter enforces a sliding quota of events per key. Request uses it for
// both per-phone and per-IP limits. Implementations must be safe for concurrent
// use.
type RateLimiter interface {
	// Allow reports whether an event under key is permitted given at most max
	// events within window ending now, and RECORDS the event when it returns
	// true. When it returns false the event is rejected and not recorded.
	Allow(ctx context.Context, key string, max int, window time.Duration, now time.Time) (bool, error)
}
