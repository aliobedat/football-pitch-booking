package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword bcrypt-hashes a plaintext password at the given cost. It is the
// single hashing entry point shared by the provisioning paths (the dbadmin
// set-password CLI and staff onboarding) so they never diverge. Verification
// stays with bcrypt.CompareHashAndPassword at the login site — the cost is read
// from the stored hash, so it is not needed here.
//
// The caller passes cfg.BcryptCost (validated 10–31 at config load). bcrypt itself
// also rejects an out-of-range cost, so a misconfiguration fails closed rather
// than producing a weak hash.
func HashPassword(password string, cost int) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}
