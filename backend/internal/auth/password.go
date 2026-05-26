package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword produces a bcrypt hash of the plaintext password using the
// provided cost factor. Cost 12 requires ~300ms on modern hardware — expensive
// enough to make offline brute-force infeasible, cheap enough for a login endpoint.
func HashPassword(plaintext string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), cost)
	if err != nil {
		return "", fmt.Errorf("HashPassword: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword compares a bcrypt hash against a plaintext candidate.
// Returns nil on match, an error otherwise.
// Callers must not distinguish between "wrong password" and "hash malformed" —
// both must produce the same generic response to the client.
func VerifyPassword(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}

// DummyVerify performs a bcrypt comparison that is guaranteed to fail.
// It is called when a user email is not found during login to normalise
// response latency and prevent timing-based email enumeration.
//
// Without this, an attacker can distinguish "email not found" (fast response,
// no bcrypt work) from "wrong password" (slow response, bcrypt ran) by
// measuring response time differences of ~250ms.
func DummyVerify() {
	// This hash is a valid bcrypt string that will never match any input.
	// Its only purpose is to consume the same CPU time as a real comparison.
	const sentinelHash = "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	//nolint:errcheck
	bcrypt.CompareHashAndPassword([]byte(sentinelHash), []byte("dummy_password_for_timing"))
}