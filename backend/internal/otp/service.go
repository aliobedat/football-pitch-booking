package otp

// PART 3A scope: the OTP service core. Service implements the OtpService contract
// from PART 1 (notification.OtpService): Request generates, stores (hashed) and
// dispatches a code; Verify checks it. All delivery flows through the injected
// NotificationService from PART 2 — Service never talks to a provider directly.
// Storage, hashing, and rate limiting live behind the seams in store.go/hasher.go
// so this file holds policy only.

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// Notifier is the slice of the NotificationService that the OTP service needs:
// dispatch one outbound message. *notification.Service satisfies it (and so does
// any notification.NotificationChannel), keeping the OTP service decoupled from
// routing/opt-in details — those are enforced inside the service it is given.
type Notifier interface {
	Send(ctx context.Context, msg notification.OutboundMessage) (notification.DeliveryResult, error)
}

// Config holds the OTP policy knobs. Zero values are not meaningful; build a
// populated Config with DefaultConfig and override as needed.
type Config struct {
	// CodeLength is the number of decimal digits in a generated code.
	CodeLength int
	// TTL is how long a code remains valid after issuance.
	TTL time.Duration
	// MaxVerifyAttempts is the number of failed Verify tries allowed before the
	// code is locked out.
	MaxVerifyAttempts int
	// ResendCooldown is the minimum gap between consecutive codes for one phone.
	ResendCooldown time.Duration
	// RateWindow is the sliding window over which the per-phone / per-IP request
	// quotas are counted.
	RateWindow time.Duration
	// MaxPerPhone is the max Request calls per phone within RateWindow.
	MaxPerPhone int
	// MaxPerIP is the max Request calls per source IP within RateWindow.
	MaxPerIP int
}

// DefaultConfig returns sensible production defaults: a 6-digit code valid for 5
// minutes, locked out after 5 wrong tries, a 60s resend cooldown, and at most 5
// requests per phone / 10 per IP within a 15-minute window.
func DefaultConfig() Config {
	return Config{
		CodeLength:        6,
		TTL:               5 * time.Minute,
		MaxVerifyAttempts: 5,
		ResendCooldown:    60 * time.Second,
		RateWindow:        15 * time.Minute,
		MaxPerPhone:       5,
		MaxPerIP:          10,
	}
}

// Service implements notification.OtpService.
type Service struct {
	notifier Notifier
	store    Store
	limiter  RateLimiter
	hasher   Hasher
	cfg      Config

	now  func() time.Time
	rand io.Reader
}

// Option customises a Service at construction.
type Option func(*Service)

// WithClock overrides the time source (used by tests to drive expiry/cooldown
// deterministically without sleeping).
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithRandReader overrides the entropy source for code generation (used by tests
// to force a known code or simulate an entropy failure).
func WithRandReader(r io.Reader) Option {
	return func(s *Service) { s.rand = r }
}

// New constructs an OTP Service. notifier, store, limiter, and hasher are the
// required collaborators; cfg supplies policy. Passing the same MemoryStore as
// both store and limiter is the expected in-memory wiring.
func New(notifier Notifier, store Store, limiter RateLimiter, hasher Hasher, cfg Config, opts ...Option) *Service {
	s := &Service{
		notifier: notifier,
		store:    store,
		limiter:  limiter,
		hasher:   hasher,
		cfg:      cfg,
		now:      time.Now,
		rand:     rand.Reader,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Request generates a fresh code, stores its hash with an expiry, and dispatches
// it via the NotificationService. It enforces per-phone and per-IP rate limits
// and a resend cooldown before issuing. The IP is read from ctx (see WithIP) so
// the OtpService interface signature stays unchanged.
//
// Order matters: cheap structural and quota checks run before any code is
// generated or delivered, so abuse is rejected without consuming entropy or
// hitting the notification channel.
func (s *Service) Request(ctx context.Context, phone string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ErrInvalidPhone
	}
	now := s.now()

	// Per-phone quota.
	ok, err := s.limiter.Allow(ctx, "phone:"+phone, s.cfg.MaxPerPhone, s.cfg.RateWindow, now)
	if err != nil {
		return fmt.Errorf("otp: phone rate-limit check: %w", err)
	}
	if !ok {
		return ErrRateLimited
	}

	// Per-IP quota (only when an IP is known; the HTTP layer supplies it).
	if ip, present := ipFromContext(ctx); present {
		ok, err := s.limiter.Allow(ctx, "ip:"+ip, s.cfg.MaxPerIP, s.cfg.RateWindow, now)
		if err != nil {
			return fmt.Errorf("otp: ip rate-limit check: %w", err)
		}
		if !ok {
			return ErrRateLimited
		}
	}

	// Resend cooldown: refuse if an active code was issued too recently.
	if existing, found, err := s.store.Get(ctx, phone); err != nil {
		return fmt.Errorf("otp: load existing code: %w", err)
	} else if found && now.Sub(existing.CreatedAt) < s.cfg.ResendCooldown {
		return ErrResendTooSoon
	}

	// Generate and persist only the hash; the plaintext lives just long enough
	// to be handed to the notification channel below.
	code, err := generateNumericCode(s.rand, s.cfg.CodeLength)
	if err != nil {
		return fmt.Errorf("otp: generate code: %w", err)
	}

	rec := Code{
		Phone:     phone,
		Hash:      s.hasher.Hash(code),
		ExpiresAt: now.Add(s.cfg.TTL),
		Attempts:  0,
		CreatedAt: now,
	}
	if err := s.store.Save(ctx, rec); err != nil {
		return fmt.Errorf("otp: store code: %w", err)
	}

	msg := notification.OutboundMessage{
		Recipient: phone,
		Kind:      notification.KindOTP,
		Payload: notification.OTPPayload{
			Code:             code,
			ExpiresInSeconds: int(s.cfg.TTL / time.Second),
		},
	}
	if _, err := s.notifier.Send(ctx, msg); err != nil {
		// Delivery failed (e.g. opt-in gate, channel error). Drop the stored
		// code so the cooldown does not strand the user with a code they never
		// received; the consumed rate-limit slot still guards against abuse.
		_ = s.store.Delete(ctx, phone)
		return fmt.Errorf("otp: dispatch code: %w", err)
	}

	return nil
}

// Verify checks code against the active stored hash for phone. On success it
// marks the phone verified and invalidates the code (one-time use). It returns
// (false, <sentinel>) for the distinct failure modes — missing, expired, locked
// out, or mismatched — so the HTTP layer can respond precisely.
func (s *Service) Verify(ctx context.Context, phone, code string) (bool, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return false, ErrInvalidPhone
	}

	rec, found, err := s.store.Get(ctx, phone)
	if err != nil {
		return false, fmt.Errorf("otp: load code: %w", err)
	}
	if !found {
		return false, ErrCodeNotFound
	}

	// Expiry takes precedence over everything else; clear the dead code.
	if !s.now().Before(rec.ExpiresAt) {
		_ = s.store.Delete(ctx, phone)
		return false, ErrCodeExpired
	}

	// Lockout: once the failure budget is spent the code is dead until expiry,
	// regardless of whether a later guess is correct.
	if rec.Attempts >= s.cfg.MaxVerifyAttempts {
		return false, ErrTooManyAttempts
	}

	if !s.hasher.Verify(code, rec.Hash) {
		attempts, incErr := s.store.IncrementAttempts(ctx, phone)
		if incErr != nil {
			return false, fmt.Errorf("otp: record failed attempt: %w", incErr)
		}
		if attempts >= s.cfg.MaxVerifyAttempts {
			return false, ErrTooManyAttempts
		}
		return false, ErrCodeMismatch
	}

	// Success: mark verified and invalidate the code immediately (one-time use).
	if err := s.store.MarkPhoneVerified(ctx, phone); err != nil {
		return false, fmt.Errorf("otp: mark verified: %w", err)
	}
	if err := s.store.Delete(ctx, phone); err != nil {
		return false, fmt.Errorf("otp: invalidate code: %w", err)
	}
	return true, nil
}

// generateNumericCode returns a cryptographically random decimal string of the
// given length, zero-padded. It draws from r via crypto/rand.Int over [0,10^n),
// which is uniform — no modulo bias.
func generateNumericCode(r io.Reader, length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("otp: code length must be positive, got %d", length)
	}
	upper := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(r, upper)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}

// Compile-time assertion that Service satisfies the PART 1 contract.
var _ notification.OtpService = (*Service)(nil)
