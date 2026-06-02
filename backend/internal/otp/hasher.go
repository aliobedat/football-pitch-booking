package otp

// Hashing for one-time codes. We NEVER store the plaintext code: only a keyed
// digest is persisted. A short numeric code (6 digits) has a small keyspace, so
// an UNkeyed hash would be trivially brute-forced from a leaked store via a
// rainbow/precomputed table. HMAC-SHA256 under a server-side secret (a "pepper")
// makes the stored digests useless without that secret, while staying fast — the
// online guessing rate is already bounded by attempt lockout + expiry in the
// service. The secret comes from the environment; it is never hardcoded.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrEmptySecret is returned by NewHMACHasher when constructed without a secret.
var ErrEmptySecret = errors.New("otp: hasher secret must not be empty")

// Hasher derives and verifies the stored representation of an OTP code.
// Implementations must compare in constant time to avoid leaking the digest via
// timing.
type Hasher interface {
	// Hash returns the storable digest of a plaintext code.
	Hash(code string) string
	// Verify reports whether code hashes to the previously stored digest, using
	// a constant-time comparison.
	Verify(code, digest string) bool
}

// hmacHasher is the default Hasher: HMAC-SHA256 keyed with a server-side secret,
// hex-encoded for storage.
type hmacHasher struct {
	secret []byte
}

// NewHMACHasher builds an HMAC-SHA256 hasher keyed with secret. The secret is a
// server-side pepper and MUST be supplied from configuration/environment, never
// hardcoded. An empty secret is rejected so misconfiguration fails loudly.
func NewHMACHasher(secret string) (Hasher, error) {
	if secret == "" {
		return nil, ErrEmptySecret
	}
	return &hmacHasher{secret: []byte(secret)}, nil
}

// Hash returns the hex-encoded HMAC-SHA256 of code under the configured secret.
func (h *hmacHasher) Hash(code string) string {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify recomputes the digest for code and compares it to digest in constant
// time (hmac.Equal), so a partial match cannot be discovered by timing.
func (h *hmacHasher) Verify(code, digest string) bool {
	want, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(h.Hash(code))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
