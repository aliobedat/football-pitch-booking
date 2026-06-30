package repository

// Permanent DB-gated coverage for the ADMIN staff-management path (the real SQL
// admin-bypass added in fix/staff-admin-permissions), plus a re-assertion that
// OWNER cross-tenant isolation still holds. Exercises the actual queries
// (resolveBindingOwnerTx, OwnerScopeFilter) against a live database — the
// handler-level model tests cannot prove the SQL.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (same gate as the other
// integration suites):
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run StaffAdmin -v

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/auth"
)

// seedUser inserts a user with a unique phone and registers cleanup of any staff
// rows referencing them plus the user row itself (runs before newBlockEnv's own
// cleanup thanks to LIFO ordering, so FK constraints are satisfied).
func (e *blockEnv) seedUser(t *testing.T, name, prefix, role string) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano() % 1_000_000
	var id int64
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
	`, name, fmt.Sprintf("+962%s%06d", prefix, suffix), role).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM staff WHERE user_id = $1 OR owner_id = $1`, id)
		_, _ = e.pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// cleanupStaffOn deletes any staff rows on the given pitch so newBlockEnv's pitch
// delete does not trip the staff.pitch_id FK. Registered per test.
func (e *blockEnv) cleanupStaffOn(t *testing.T, pitchID int64) {
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM staff WHERE pitch_id = $1`, pitchID)
	})
}

func adminActor(id int64) auth.Actor { return auth.Actor{UserID: int(id), Role: auth.RoleAdmin} }
func ownerActorOf(id int64) auth.Actor {
	return auth.Actor{UserID: int(id), Role: auth.RoleOwner}
}

// staffTestHash is a placeholder password_hash for binding/scoping tests that do
// NOT exercise login. The column is TEXT (no format check); a non-empty value is
// all the onboarding flow needs to satisfy "password provided".
const staffTestHash = "$2a$10$placeholderhashvaluefortestsonlyxxxxxxxxxxxxxxxxxxxx"

// TestStaffAdmin_BindForeignPitch: an admin (who owns nothing) can bind staff to a
// pitch owned by someone else; the binding carries the PITCH's real owner_id (not
// the admin's), and the target is promoted to staff.
func TestStaffAdmin_BindForeignPitch(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	admin := e.seedUser(t, "ST Admin", "70", "admin")
	e.cleanupStaffOn(t, e.pitchID)
	ctx := context.Background()

	member, err := repo.CreateStaffBindings(ctx, adminActor(admin),
		[]int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash})
	if err != nil {
		t.Fatalf("admin bind to foreign pitch: %v", err)
	}
	// Binding is attributed to the pitch's REAL owner, never the admin.
	if member.OwnerID != int(e.ownerID) {
		t.Fatalf("binding owner = %d, want pitch owner %d (not the admin)", member.OwnerID, e.ownerID)
	}
	if e.userRole(t, e.playerID) != "staff" {
		t.Fatalf("target role = %q, want staff (promoted)", e.userRole(t, e.playerID))
	}
	_, ownerID, found, err := repo.StaffBindings(ctx, int(e.playerID))
	if err != nil || !found || ownerID != int(e.ownerID) {
		t.Fatalf("StaffBindings owner = %d found=%v err=%v, want owner %d", ownerID, found, err, e.ownerID)
	}
}

// TestStaffAdmin_ListSeesAllOwners: admin ListStaff spans every owner; an owner
// sees only their own.
func TestStaffAdmin_ListSeesAllOwners(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	admin := e.seedUser(t, "ST Admin", "71", "admin")
	player2 := e.seedUser(t, "ST Player2", "72", "player")
	pitchB := e.makeSecondPitch(t, e.otherID) // owned by otherID
	e.cleanupStaffOn(t, e.pitchID)
	e.cleanupStaffOn(t, pitchB)
	ctx := context.Background()

	// One staff under ownerA (own pitch), one under ownerB (own pitch).
	if _, err := repo.CreateStaffBindings(ctx, ownerActorOf(e.ownerID), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash}); err != nil {
		t.Fatalf("bind under ownerA: %v", err)
	}
	if _, err := repo.CreateStaffBindings(ctx, ownerActorOf(e.otherID), []int{int(pitchB)}, e.userPhone(t, player2), StaffProvision{PasswordHash: staffTestHash}); err != nil {
		t.Fatalf("bind under ownerB: %v", err)
	}

	adminList, err := repo.ListStaff(ctx, adminActor(admin))
	if err != nil {
		t.Fatalf("admin ListStaff: %v", err)
	}
	if !containsUser(adminList, int(e.playerID)) || !containsUser(adminList, int(player2)) {
		t.Fatalf("admin list missing a cross-owner member: %+v", adminList)
	}

	ownerList, err := repo.ListStaff(ctx, ownerActorOf(e.ownerID))
	if err != nil {
		t.Fatalf("owner ListStaff: %v", err)
	}
	if !containsUser(ownerList, int(e.playerID)) || containsUser(ownerList, int(player2)) {
		t.Fatalf("owner list must contain only own staff, got: %+v", ownerList)
	}
}

// TestStaffAdmin_RevokeForeignBinding: admin can revoke a binding owned by another
// owner; the binding is removed and the user demoted.
func TestStaffAdmin_RevokeForeignBinding(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	admin := e.seedUser(t, "ST Admin", "73", "admin")
	e.cleanupStaffOn(t, e.pitchID)
	ctx := context.Background()

	if _, err := repo.CreateStaffBindings(ctx, ownerActorOf(e.ownerID), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash}); err != nil {
		t.Fatalf("seed binding under ownerA: %v", err)
	}
	if err := repo.RevokeStaff(ctx, adminActor(admin), int(e.playerID)); err != nil {
		t.Fatalf("admin revoke foreign binding: %v", err)
	}
	if _, _, found, _ := repo.StaffBindings(ctx, int(e.playerID)); found {
		t.Fatalf("binding still present after admin revoke")
	}
	if e.userRole(t, e.playerID) != "player" {
		t.Fatalf("role = %q after revoke, want player (demoted)", e.userRole(t, e.playerID))
	}
}

// TestStaffAdmin_OwnerCrossTenantIsolation: the admin-bypass must NOT loosen owner
// isolation — owner B can neither bind to nor revoke owner A's staff.
func TestStaffAdmin_OwnerCrossTenantIsolation(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	e.cleanupStaffOn(t, e.pitchID)
	ctx := context.Background()

	// Owner B tries to bind to owner A's pitch → ErrPitchNotOwned.
	if _, err := repo.CreateStaffBindings(ctx, ownerActorOf(e.otherID), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash}); !errors.Is(err, ErrPitchNotOwned) {
		t.Fatalf("ownerB bind to ownerA pitch err = %v, want ErrPitchNotOwned", err)
	}

	// Seed a real binding under owner A, then owner B tries to revoke it → not found.
	if _, err := repo.CreateStaffBindings(ctx, ownerActorOf(e.ownerID), []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash}); err != nil {
		t.Fatalf("seed binding under ownerA: %v", err)
	}
	if err := repo.RevokeStaff(ctx, ownerActorOf(e.otherID), int(e.playerID)); !errors.Is(err, ErrStaffBindingNotFound) {
		t.Fatalf("ownerB revoke ownerA binding err = %v, want ErrStaffBindingNotFound", err)
	}
	// And the binding is untouched.
	if _, _, found, _ := repo.StaffBindings(ctx, int(e.playerID)); !found {
		t.Fatalf("ownerA binding was removed by ownerB's failed revoke")
	}
}

// containsUser reports whether members includes a StaffMember with the given id.
func containsUser(members []StaffMember, userID int) bool {
	for _, m := range members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}
