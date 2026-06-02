package otp

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"testing"
	"time"
)

// TestHMACHasher_RoundTrip checks Hash/Verify agree and reject the wrong code,
// and that the digest is not the plaintext.
func TestHMACHasher_RoundTrip(t *testing.T) {
	h, err := NewHMACHasher(testSecret)
	if err != nil {
		t.Fatalf("NewHMACHasher: %v", err)
	}
	const code = "482913"
	digest := h.Hash(code)

	if digest == code {
		t.Fatal("digest equals plaintext")
	}
	if !h.Verify(code, digest) {
		t.Error("Verify rejected the correct code")
	}
	if h.Verify("000000", digest) {
		t.Error("Verify accepted a wrong code")
	}
	if h.Verify(code, "not-hex") {
		t.Error("Verify accepted a malformed digest")
	}
}

// TestHMACHasher_SecretMatters ensures digests are keyed: different secrets yield
// different digests and do not cross-verify.
func TestHMACHasher_SecretMatters(t *testing.T) {
	h1, _ := NewHMACHasher("secret-one")
	h2, _ := NewHMACHasher("secret-two")
	const code = "123456"

	if h1.Hash(code) == h2.Hash(code) {
		t.Fatal("different secrets produced the same digest")
	}
	if h2.Verify(code, h1.Hash(code)) {
		t.Error("digest from one secret verified under another")
	}
}

// TestNewHMACHasher_EmptySecret rejects an empty secret.
func TestNewHMACHasher_EmptySecret(t *testing.T) {
	if _, err := NewHMACHasher(""); !errors.Is(err, ErrEmptySecret) {
		t.Errorf("err = %v, want ErrEmptySecret", err)
	}
}

// TestGenerateNumericCode_Format checks length, zero-padding, and digit-only
// output across many draws.
func TestGenerateNumericCode_Format(t *testing.T) {
	for _, length := range []int{4, 6, 8} {
		for range 200 {
			code, err := generateNumericCode(rand.Reader, length)
			if err != nil {
				t.Fatalf("generateNumericCode(%d): %v", length, err)
			}
			if len(code) != length {
				t.Fatalf("len(%q) = %d, want %d", code, len(code), length)
			}
			if _, err := strconv.Atoi(code); err != nil {
				t.Fatalf("code %q is not all digits", code)
			}
		}
	}
}

// TestGenerateNumericCode_Errors covers the invalid-length and entropy-failure
// paths.
func TestGenerateNumericCode_Errors(t *testing.T) {
	if _, err := generateNumericCode(rand.Reader, 0); err == nil {
		t.Error("expected error for non-positive length")
	}
	// A reader that always fails surfaces the entropy error.
	if _, err := generateNumericCode(failingReader{}, 6); err == nil {
		t.Error("expected error when entropy source fails")
	}
}

// TestService_RandFailurePropagates ensures Request surfaces an entropy failure
// and stores/sends nothing.
func TestService_RandFailurePropagates(t *testing.T) {
	h := newHarness(t, true)
	// Swap in a failing entropy source.
	h.svc.rand = failingReader{}

	err := h.svc.Request(context.Background(), testPhone)
	if err == nil {
		t.Fatal("expected Request to fail when code generation fails")
	}
	if h.fake.Count() != 0 {
		t.Errorf("fake recorded %d sends, want 0", h.fake.Count())
	}
	if _, found, _ := h.store.Get(context.Background(), testPhone); found {
		t.Error("a code was stored despite generation failure")
	}
}

// TestMemoryStore_SaveGetDelete exercises the basic lifecycle.
func TestMemoryStore_SaveGetDelete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	rec := Code{Phone: testPhone, Hash: "h", ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now()}

	if _, found, _ := s.Get(ctx, testPhone); found {
		t.Fatal("Get found a code before Save")
	}
	if err := s.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, _ := s.Get(ctx, testPhone)
	if !found || got.Hash != "h" {
		t.Fatalf("Get = (%+v, %v), want the saved record", got, found)
	}
	if err := s.Delete(ctx, testPhone); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := s.Get(ctx, testPhone); found {
		t.Error("Get found a code after Delete")
	}
	// Delete of an absent phone is a no-op.
	if err := s.Delete(ctx, "absent"); err != nil {
		t.Errorf("Delete absent: %v", err)
	}
}

// TestMemoryStore_SaveReplaces confirms a resend overwrites the previous code and
// resets attempts.
func TestMemoryStore_SaveReplaces(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Save(ctx, Code{Phone: testPhone, Hash: "old", Attempts: 0})
	if _, err := s.IncrementAttempts(ctx, testPhone); err != nil {
		t.Fatalf("IncrementAttempts: %v", err)
	}
	_ = s.Save(ctx, Code{Phone: testPhone, Hash: "new", Attempts: 0})

	got, _, _ := s.Get(ctx, testPhone)
	if got.Hash != "new" || got.Attempts != 0 {
		t.Errorf("after replace = (hash=%q attempts=%d), want (new, 0)", got.Hash, got.Attempts)
	}
}

// TestMemoryStore_IncrementAttempts increments and reports not-found correctly.
func TestMemoryStore_IncrementAttempts(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if _, err := s.IncrementAttempts(ctx, testPhone); !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("err = %v, want ErrCodeNotFound", err)
	}
	_ = s.Save(ctx, Code{Phone: testPhone, Hash: "h"})
	for want := 1; want <= 3; want++ { //nolint:intrange // counting from 1, range-over-int starts at 0
		got, err := s.IncrementAttempts(ctx, testPhone)
		if err != nil {
			t.Fatalf("IncrementAttempts: %v", err)
		}
		if got != want {
			t.Errorf("attempts = %d, want %d", got, want)
		}
	}
}

// TestMemoryStore_MarkPhoneVerified flips the verified flag.
func TestMemoryStore_MarkPhoneVerified(t *testing.T) {
	s := NewMemoryStore()
	if s.IsPhoneVerified(testPhone) {
		t.Fatal("phone reported verified before marking")
	}
	if err := s.MarkPhoneVerified(context.Background(), testPhone); err != nil {
		t.Fatalf("MarkPhoneVerified: %v", err)
	}
	if !s.IsPhoneVerified(testPhone) {
		t.Error("phone not reported verified after marking")
	}
}

// TestMemoryStore_RateLimiter_Window verifies the sliding window: max events are
// admitted, the next is rejected, and capacity returns once events age out.
func TestMemoryStore_RateLimiter_Window(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	const key = "phone:+962790000000"
	const max = 3
	window := time.Minute
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	for i := range max {
		ok, _ := s.Allow(ctx, key, max, window, base.Add(time.Duration(i)*time.Second))
		if !ok {
			t.Fatalf("event %d rejected, want admitted", i+1)
		}
	}
	// Within the window the next event is rejected.
	if ok, _ := s.Allow(ctx, key, max, window, base.Add(4*time.Second)); ok {
		t.Fatal("over-quota event admitted")
	}
	// After the window slides past the earliest events, capacity frees up.
	if ok, _ := s.Allow(ctx, key, max, window, base.Add(2*time.Minute)); !ok {
		t.Error("event after window elapsed was rejected")
	}
}

// TestMemoryStore_RateLimiter_KeysIsolated ensures different keys don't share a
// quota.
func TestMemoryStore_RateLimiter_KeysIsolated(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	if ok, _ := s.Allow(ctx, "a", 1, time.Minute, now); !ok {
		t.Fatal("first event on key a rejected")
	}
	if ok, _ := s.Allow(ctx, "a", 1, time.Minute, now); ok {
		t.Fatal("second event on key a admitted past quota")
	}
	if ok, _ := s.Allow(ctx, "b", 1, time.Minute, now); !ok {
		t.Error("first event on key b rejected; keys must be isolated")
	}
}

// TestContext_WithIP round-trips the IP and treats empty/absent as unknown.
func TestContext_WithIP(t *testing.T) {
	if ip, ok := ipFromContext(context.Background()); ok || ip != "" {
		t.Errorf("absent IP = (%q, %v), want (\"\", false)", ip, ok)
	}
	ctx := WithIP(context.Background(), "198.51.100.5")
	if ip, ok := ipFromContext(ctx); !ok || ip != "198.51.100.5" {
		t.Errorf("present IP = (%q, %v), want (198.51.100.5, true)", ip, ok)
	}
	if ip, ok := ipFromContext(WithIP(context.Background(), "")); ok || ip != "" {
		t.Errorf("empty IP = (%q, %v), want (\"\", false)", ip, ok)
	}
}

// failingReader always returns an error, simulating an entropy-source failure.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }
