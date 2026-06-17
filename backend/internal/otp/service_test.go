package otp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

const (
	testPhone  = "+962790000000"
	testSecret = "test-otp-pepper-secret-value-do-not-use-in-prod"
)

// clock is a controllable time source for deterministic expiry/cooldown tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// harness bundles a wired OTP Service with the collaborators a test inspects.
type harness struct {
	svc   *Service
	store *MemoryStore
	fake  *notification.FakeChannel
	clk   *clock
}

// newHarness wires a real notification.Service (opt-in allowed) over a silent
// FakeChannel, an in-memory store/limiter, and the HMAC hasher. cfgFns may tweak
// the otherwise-default Config.
func newHarness(t *testing.T, optedIn bool, cfgFns ...func(*Config)) *harness {
	t.Helper()

	fake := notification.NewFakeChannel(notification.FakeSilent())
	checker := notification.OptInFunc(func(context.Context, string) (bool, error) {
		return optedIn, nil
	})
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

	cfg := DefaultConfig()
	for _, fn := range cfgFns {
		fn(&cfg)
	}

	clk := &clock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	svc := New(notifier, store, store, hasher, cfg, WithClock(clk.now))

	return &harness{svc: svc, store: store, fake: fake, clk: clk}
}

// lastCode extracts the plaintext code from the most recently delivered OTP
// message — exactly what a real user would read off their device and type back.
func (h *harness) lastCode(t *testing.T) string {
	t.Helper()
	msg, ok := h.fake.Last()
	if !ok {
		t.Fatal("no message was delivered to the fake channel")
	}
	p, ok := msg.Payload.(notification.OTPPayload)
	if !ok {
		t.Fatalf("delivered payload is %T, want notification.OTPPayload", msg.Payload)
	}
	return p.Code
}

// TestRequestVerify_FullCycle is the core acceptance check: a code requested and
// delivered through the Fake channel verifies successfully and marks the phone.
func TestRequestVerify_FullCycle(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if h.fake.Count() != 1 {
		t.Fatalf("fake recorded %d messages, want 1", h.fake.Count())
	}

	// The delivered message must be an OTP for the right recipient with a TTL.
	msg, _ := h.fake.Last()
	if msg.Kind != notification.KindOTP {
		t.Errorf("Kind = %q, want %q", msg.Kind, notification.KindOTP)
	}
	if msg.Recipient != testPhone {
		t.Errorf("Recipient = %q, want %q", msg.Recipient, testPhone)
	}
	p := msg.Payload.(notification.OTPPayload)
	if p.ExpiresInSeconds != int(DefaultConfig().TTL/time.Second) {
		t.Errorf("ExpiresInSeconds = %d, want %d", p.ExpiresInSeconds, int(DefaultConfig().TTL/time.Second))
	}

	code := h.lastCode(t)
	ok, err := h.svc.Verify(ctx, testPhone, code)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify returned false for the correct code")
	}
	if !h.store.IsPhoneVerified(testPhone) {
		t.Error("phone was not marked verified after successful Verify")
	}
}

// TestVerify_InvalidatedAfterSuccess proves a code is one-time use: a second
// Verify with the same code finds no active code.
func TestVerify_InvalidatedAfterSuccess(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	code := h.lastCode(t)

	if ok, err := h.svc.Verify(ctx, testPhone, code); err != nil || !ok {
		t.Fatalf("first Verify: ok=%v err=%v", ok, err)
	}

	ok, err := h.svc.Verify(ctx, testPhone, code)
	if ok {
		t.Error("second Verify succeeded; code should have been invalidated")
	}
	if !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("err = %v, want ErrCodeNotFound", err)
	}
}

// TestVerify_WrongCode checks a mismatch is reported and records a failed
// attempt without verifying the phone.
func TestVerify_WrongCode(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	correct := h.lastCode(t)
	wrong := bumpCode(correct)

	ok, err := h.svc.Verify(ctx, testPhone, wrong)
	if ok {
		t.Error("Verify succeeded for a wrong code")
	}
	if !errors.Is(err, ErrCodeMismatch) {
		t.Errorf("err = %v, want ErrCodeMismatch", err)
	}
	if h.store.IsPhoneVerified(testPhone) {
		t.Error("phone marked verified after a wrong code")
	}

	// The correct code still works afterwards (one wrong try is not lockout).
	if ok, err := h.svc.Verify(ctx, testPhone, correct); err != nil || !ok {
		t.Fatalf("Verify with correct code after one miss: ok=%v err=%v", ok, err)
	}
}

// TestVerify_Expiry checks an expired code is rejected and cleared.
func TestVerify_Expiry(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.TTL = 5 * time.Minute })
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	code := h.lastCode(t)

	// Move past expiry.
	h.clk.advance(5*time.Minute + time.Second)

	ok, err := h.svc.Verify(ctx, testPhone, code)
	if ok {
		t.Error("Verify succeeded for an expired code")
	}
	if !errors.Is(err, ErrCodeExpired) {
		t.Errorf("err = %v, want ErrCodeExpired", err)
	}
	// Expired code is cleared, so a follow-up reports not-found.
	if _, err := h.svc.Verify(ctx, testPhone, code); !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("post-expiry err = %v, want ErrCodeNotFound", err)
	}
}

// TestVerify_AttemptLockout checks lockout after N failed attempts, and that even
// the correct code is refused once locked.
func TestVerify_AttemptLockout(t *testing.T) {
	const maxAttempts = 3
	h := newHarness(t, true, func(c *Config) { c.MaxVerifyAttempts = maxAttempts })
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	correct := h.lastCode(t)
	wrong := bumpCode(correct)

	// First maxAttempts-1 misses report a plain mismatch.
	for i := range maxAttempts - 1 {
		if _, err := h.svc.Verify(ctx, testPhone, wrong); !errors.Is(err, ErrCodeMismatch) {
			t.Fatalf("attempt %d err = %v, want ErrCodeMismatch", i+1, err)
		}
	}

	// The attempt that exhausts the budget flips to lockout.
	if _, err := h.svc.Verify(ctx, testPhone, wrong); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("lockout attempt err = %v, want ErrTooManyAttempts", err)
	}

	// Even the correct code is now refused.
	ok, err := h.svc.Verify(ctx, testPhone, correct)
	if ok {
		t.Error("correct code accepted after lockout")
	}
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("post-lockout err = %v, want ErrTooManyAttempts", err)
	}
}

// TestRequest_PerPhoneRateLimit checks the per-phone request quota. The resend
// cooldown is neutralised so only the rate limit is under test.
func TestRequest_PerPhoneRateLimit(t *testing.T) {
	const maxPerPhone = 3
	h := newHarness(t, true, func(c *Config) {
		c.MaxPerPhone = maxPerPhone
		c.ResendCooldown = 0
	})
	ctx := context.Background()

	for i := range maxPerPhone {
		if err := h.svc.Request(ctx, testPhone); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}

	if err := h.svc.Request(ctx, testPhone); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over-quota request err = %v, want ErrRateLimited", err)
	}
	// The rejected request must not have been delivered.
	if h.fake.Count() != maxPerPhone {
		t.Errorf("fake recorded %d sends, want %d", h.fake.Count(), maxPerPhone)
	}

	// A different phone is unaffected by another phone's quota.
	if err := h.svc.Request(ctx, "+962790000001"); err != nil {
		t.Errorf("request for a fresh phone err = %v, want nil", err)
	}
}

// TestRequest_PerIPRateLimit checks the per-IP quota: distinct phones sharing one
// IP are throttled together once the IP quota is spent.
func TestRequest_PerIPRateLimit(t *testing.T) {
	const maxPerIP = 2
	h := newHarness(t, true, func(c *Config) {
		c.MaxPerIP = maxPerIP
		c.MaxPerPhone = 100 // take phone quota out of the picture
		c.ResendCooldown = 0
	})
	ctx := WithIP(context.Background(), "203.0.113.7")

	if err := h.svc.Request(ctx, "+962790000001"); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if err := h.svc.Request(ctx, "+962790000002"); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	// Third distinct phone from the same IP is throttled by the IP quota.
	if err := h.svc.Request(ctx, "+962790000003"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over-IP-quota request err = %v, want ErrRateLimited", err)
	}

	// The same phone from a different IP is allowed.
	other := WithIP(context.Background(), "203.0.113.8")
	if err := h.svc.Request(other, "+962790000003"); err != nil {
		t.Errorf("request from a different IP err = %v, want nil", err)
	}
}

// TestRequest_ResendCooldown checks the minimum gap between codes for one phone,
// and that the cooldown lifts once enough time passes.
func TestRequest_ResendCooldown(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.ResendCooldown = 60 * time.Second })
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("first request: %v", err)
	}
	// Immediate resend is refused.
	if err := h.svc.Request(ctx, testPhone); !errors.Is(err, ErrResendTooSoon) {
		t.Fatalf("immediate resend err = %v, want ErrResendTooSoon", err)
	}

	// After the cooldown elapses a resend is allowed.
	h.clk.advance(61 * time.Second)
	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("resend after cooldown: %v", err)
	}
	if h.fake.Count() != 2 {
		t.Errorf("fake recorded %d sends, want 2", h.fake.Count())
	}
}

// TestRequest_OptInGate_NoStrandedCooldown verifies that when delivery is refused
// (opt-in absent) the error propagates AND the stored code is dropped, so the
// caller is not stranded behind a cooldown for a code they never received.
func TestRequest_OptInGate_NoStrandedCooldown(t *testing.T) {
	h := newHarness(t, false) // opt-in denied
	ctx := context.Background()

	err := h.svc.Request(ctx, testPhone)
	if !errors.Is(err, notification.ErrOptInRequired) {
		t.Fatalf("err = %v, want it to wrap ErrOptInRequired", err)
	}
	if h.fake.Count() != 0 {
		t.Errorf("fake recorded %d sends, want 0", h.fake.Count())
	}
	if _, found, _ := h.store.Get(ctx, testPhone); found {
		t.Error("a code was left stored after a failed dispatch")
	}
}

// TestRequest_EmptyPhone and Verify with empty phone are rejected up front.
func TestEmptyPhone(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	if err := h.svc.Request(ctx, "   "); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("Request empty err = %v, want ErrInvalidPhone", err)
	}
	if _, err := h.svc.Verify(ctx, "", "123456"); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("Verify empty err = %v, want ErrInvalidPhone", err)
	}
}

// TestVerify_NoActiveCode reports not-found when Verify runs before any Request.
func TestVerify_NoActiveCode(t *testing.T) {
	h := newHarness(t, true)
	if _, err := h.svc.Verify(context.Background(), testPhone, "123456"); !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("err = %v, want ErrCodeNotFound", err)
	}
}

// TestRequest_StoresHashNotPlaintext guards the central security invariant: the
// store never holds the plaintext code.
func TestRequest_StoresHashNotPlaintext(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	if err := h.svc.Request(ctx, testPhone); err != nil {
		t.Fatalf("Request: %v", err)
	}
	code := h.lastCode(t)

	rec, found, _ := h.store.Get(ctx, testPhone)
	if !found {
		t.Fatal("no code stored after Request")
	}
	if rec.Hash == "" {
		t.Fatal("stored hash is empty")
	}
	if rec.Hash == code || strings.Contains(rec.Hash, code) {
		t.Errorf("stored value %q contains the plaintext code %q", rec.Hash, code)
	}
}

// bumpCode returns a code guaranteed to differ from in by changing its last
// digit, preserving length.
func bumpCode(in string) string {
	if in == "" {
		return "0"
	}
	last := in[len(in)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	return in[:len(in)-1] + string(repl)
}
