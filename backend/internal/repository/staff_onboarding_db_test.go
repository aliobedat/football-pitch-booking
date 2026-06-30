package repository

// Permanent DB-gated coverage for the staff ONBOARDING ensure-flow (the role-aware
// create/promote/rebind path added in fix/staff-onboarding-password-flow). Exercises
// the real SQL (FOR UPDATE read, conditional INSERT/UPDATE, password_hash handling)
// against a live database.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set:
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run StaffOnboard -v

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// hashOf returns a real bcrypt hash for assertions that exercise verification.
func hashOf(t *testing.T, pw string) string {
	t.Helper()
	h, err := auth.HashPassword(pw, bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// passwordHashOf reads the stored hash ("" when NULL).
func (e *blockEnv) passwordHashOf(t *testing.T, userID int64) string {
	t.Helper()
	var h string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE(password_hash,'') FROM users WHERE id = $1`, userID).Scan(&h); err != nil {
		t.Fatalf("read password_hash: %v", err)
	}
	return h
}

// setPasswordHash forces a user's stored hash (to model a player who already has one).
func (e *blockEnv) setPasswordHash(t *testing.T, userID int64, hash string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
		t.Fatalf("set password_hash: %v", err)
	}
}

// unusedPhone returns a JO mobile not present in users, registering cleanup of any
// row the onboarding flow creates for it.
func (e *blockEnv) unusedPhone(t *testing.T) string {
	t.Helper()
	phone := fmt.Sprintf("+96279%07d", time.Now().UnixNano()%10_000_000)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM staff WHERE user_id IN (SELECT id FROM users WHERE phone=$1)`, phone)
		_, _ = e.pool.Exec(cctx, `DELETE FROM users WHERE phone = $1`, phone)
	})
	return phone
}

func (e *blockEnv) userIDByPhone(t *testing.T, phone string) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE phone=$1`, phone).Scan(&id); err != nil {
		t.Fatalf("user by phone: %v", err)
	}
	return id
}

// ── Case 1: phone not in users + password → creates staff + binds ──────────────
func TestStaffOnboard_NewPhone_WithPassword_CreatesStaff(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	phone := e.unusedPhone(t)

	member, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, phone, StaffProvision{FullName: "حارس", PasswordHash: hashOf(t, "newpass123")})
	if err != nil {
		t.Fatalf("create staff for new phone: %v", err)
	}
	if len(member.Pitches) != 1 {
		t.Fatalf("bound to %d pitches, want 1", len(member.Pitches))
	}
	uid := e.userIDByPhone(t, phone)
	if e.userRole(t, uid) != "staff" {
		t.Fatalf("role = %q, want staff", e.userRole(t, uid))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHashOf(t, uid)), []byte("newpass123")); err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
}

// ── Case 2: new phone, no password → ErrPasswordRequired, no user created ───────
func TestStaffOnboard_NewPhone_NoPassword_Required(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	phone := e.unusedPhone(t)

	_, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, phone, StaffProvision{})
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
	var n int
	if e.pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE phone=$1`, phone).Scan(&n); n != 0 {
		t.Fatalf("user rows = %d for new phone, want 0 (rolled back)", n)
	}
}

// ── Case 3: existing player, no hash, no password → ErrPasswordRequired ─────────
func TestStaffOnboard_Player_NoHash_NoPassword_Required(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)

	_, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{})
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
	if e.userRole(t, e.playerID) != "player" {
		t.Fatalf("role = %q, want player (unchanged)", e.userRole(t, e.playerID))
	}
}

// ── Case 4: existing player + password → promoted, hash set, bound ─────────────
func TestStaffOnboard_Player_WithPassword_Promotes(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)

	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: hashOf(t, "promote123")}); err != nil {
		t.Fatalf("promote player: %v", err)
	}
	if e.userRole(t, e.playerID) != "staff" {
		t.Fatalf("role = %q, want staff", e.userRole(t, e.playerID))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHashOf(t, e.playerID)), []byte("promote123")); err != nil {
		t.Fatalf("hash does not verify: %v", err)
	}
}

// ── Case 5: existing staff + no password → rebind ok, hash unchanged ────────────
func TestStaffOnboard_Staff_NoPassword_RebindKeepsHash(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	pitch2 := e.makeSecondPitch(t, e.ownerID)
	e.cleanupStaffOn(t, pitch2)

	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: hashOf(t, "first123")}); err != nil {
		t.Fatalf("initial promote: %v", err)
	}
	before := e.passwordHashOf(t, e.playerID)

	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(pitch2)}, e.userPhone(t, e.playerID), StaffProvision{}); err != nil {
		t.Fatalf("rebind without password: %v", err)
	}
	if after := e.passwordHashOf(t, e.playerID); after != before {
		t.Fatalf("hash changed on a no-password rebind: before=%q after=%q", before, after)
	}
	if _, _, found, _ := repo.StaffBindings(context.Background(), int(e.playerID)); !found {
		t.Fatal("expected bindings present")
	}
}

// ── Case 6: existing staff + password → rebind ok, hash reset ───────────────────
func TestStaffOnboard_Staff_WithPassword_ResetsHash(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)

	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: hashOf(t, "first123")}); err != nil {
		t.Fatalf("initial promote: %v", err)
	}
	before := e.passwordHashOf(t, e.playerID)

	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: hashOf(t, "second456")}); err != nil {
		t.Fatalf("rebind with new password: %v", err)
	}
	after := e.passwordHashOf(t, e.playerID)
	if after == before {
		t.Fatal("hash unchanged on a password reset")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(after), []byte("second456")); err != nil {
		t.Fatalf("new hash does not verify: %v", err)
	}
}

// ── Case 7: owner phone → refused, role/hash untouched ──────────────────────────
func TestStaffOnboard_OwnerPhone_Refused(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	admin := e.seedUser(t, "OB Admin", "74", "admin")
	hashBefore := e.passwordHashOf(t, e.otherID) // a second owner

	// admin tries to enroll the OTHER owner's phone onto ownerID's pitch.
	_, err := repo.CreateStaffBindings(context.Background(), adminActor(admin),
		[]int{int(e.pitchID)}, e.userPhone(t, e.otherID), StaffProvision{PasswordHash: hashOf(t, "whatever1")})
	if !errors.Is(err, ErrCannotBindPrivileged) {
		t.Fatalf("err = %v, want ErrCannotBindPrivileged", err)
	}
	if e.userRole(t, e.otherID) != "owner" {
		t.Fatalf("role = %q, want owner (unchanged)", e.userRole(t, e.otherID))
	}
	if e.passwordHashOf(t, e.otherID) != hashBefore {
		t.Fatal("owner password_hash was touched")
	}
}

// ── Case 8: admin phone → refused ───────────────────────────────────────────────
func TestStaffOnboard_AdminPhone_Refused(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	admin := e.seedUser(t, "OB Admin2", "75", "admin")
	target := e.seedUser(t, "OB Target Admin", "76", "admin")

	_, err := repo.CreateStaffBindings(context.Background(), adminActor(admin),
		[]int{int(e.pitchID)}, e.userPhone(t, target), StaffProvision{PasswordHash: hashOf(t, "whatever1")})
	if !errors.Is(err, ErrCannotBindPrivileged) {
		t.Fatalf("err = %v, want ErrCannotBindPrivileged", err)
	}
	if e.userRole(t, target) != "admin" {
		t.Fatalf("role = %q, want admin (unchanged)", e.userRole(t, target))
	}
}

// ── Case 9: idempotent re-submit → no duplicate binding ─────────────────────────
func TestStaffOnboard_Idempotent_NoDuplicate(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)

	body := StaffProvision{PasswordHash: hashOf(t, "idem1234")}
	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), body); err != nil {
		t.Fatalf("first onboard: %v", err)
	}
	if _, err := repo.CreateStaffBindings(context.Background(), e.ownerActor(), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{}); err != nil {
		t.Fatalf("re-submit: %v", err)
	}
	ids, _, _, _ := repo.StaffBindings(context.Background(), int(e.playerID))
	if len(ids) != 1 {
		t.Fatalf("bindings = %d after idempotent re-submit, want 1", len(ids))
	}
}
