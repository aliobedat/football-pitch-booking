package otp

// WO-SECURITY-V1 PR-S1 regression: the LOCAL-DEV bypass path must never write
// the plaintext OTP code to logs, in any environment, and must not print the
// full phone number either.

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/ali/football-pitch-api/internal/notification"
)

// zeroReader is an io.Reader that always yields zero bytes, making
// generateNumericCode deterministically produce "000000" for a 6-digit code —
// a known plaintext value the test can assert is absent from logs.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)
	fn()
	return buf.String()
}

func TestRequest_DevBypass_LogsNeverContainPlaintextCodeOrFullPhone(t *testing.T) {
	fake := notification.NewFakeChannel(notification.FakeSilent())
	notifier := notification.NewService(
		notification.ChannelFake,
		notification.WithChannel(notification.ChannelFake, fake),
	)

	store := NewMemoryStore()
	hasher, err := NewHMACHasher(testSecret)
	if err != nil {
		t.Fatalf("NewHMACHasher: %v", err)
	}

	const knownCode = "000000" // deterministic under zeroReader for a 6-digit code
	svc := New(notifier, store, store, hasher, DefaultConfig(),
		WithDevBypass(true), WithRandReader(zeroReader{}))

	out := captureLog(t, func() {
		if err := svc.Request(context.Background(), testPhone); err != nil {
			t.Fatalf("Request: %v", err)
		}
	})

	rec, found, err := store.Get(context.Background(), testPhone)
	if err != nil || !found {
		t.Fatalf("expected a stored code after devBypass Request: found=%v err=%v", found, err)
	}
	if !hasher.Verify(knownCode, rec.Hash) {
		t.Fatalf("test assumption failed: stored hash does not match the expected deterministic code %q", knownCode)
	}

	if strings.Contains(out, testPhone) {
		t.Fatalf("devBypass log output contains the full unmasked phone number: %s", out)
	}
	if strings.Contains(out, knownCode) {
		t.Fatalf("devBypass log output contains the plaintext OTP code %q: %s", knownCode, out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("devBypass log output does not indicate the code was redacted: %s", out)
	}
}
