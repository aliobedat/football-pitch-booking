package repository

// Integration tests for 1:N staff scoping (migration 027) against a live DB.
// Reuses blockEnv (owner + player "92" + one pitch). SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Staff

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
)

// makeSecondPitch creates another pitch for ownerID and registers cleanup.
func (e *blockEnv) makeSecondPitch(t *testing.T, ownerID int64) int64 {
	t.Helper()
	p, err := e.model.CreatePitch(context.Background(), data.CreatePitchRequest{
		Name: "BK Pitch 2", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 35, OwnerID: int(ownerID),
	})
	if err != nil {
		t.Fatalf("seed pitch 2: %v", err)
	}
	pid := int64(p.ID)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM staff WHERE pitch_id = $1`, pid)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pid)
	})
	return pid
}

func (e *blockEnv) userRole(t *testing.T, userID int64) string {
	t.Helper()
	var role string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT role::text FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

func (e *blockEnv) userPhone(t *testing.T, userID int64) string {
	t.Helper()
	var phone string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT phone FROM users WHERE id = $1`, userID).Scan(&phone); err != nil {
		t.Fatalf("read phone: %v", err)
	}
	return phone
}

// 1:N happy path: one guard bound to two pitches; scope resolves to both; role
// promoted; revoke drops all + demotes.
func TestStaff_MultiPitchBindResolveRevoke(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	pitch2 := e.makeSecondPitch(t, e.ownerID)

	member, err := repo.CreateStaffBindings(context.Background(),
		auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, []int{int(e.pitchID), int(pitch2)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash})
	if err != nil {
		t.Fatalf("CreateStaffBindings: %v", err)
	}
	if len(member.Pitches) != 2 {
		t.Fatalf("member bound to %d pitches, want 2", len(member.Pitches))
	}
	if e.userRole(t, e.playerID) != "staff" {
		t.Fatalf("role = %q, want staff (promoted)", e.userRole(t, e.playerID))
	}

	// Scope resolution returns BOTH pitch ids (the 1:N set the middleware injects).
	pitchIDs, ownerID, found, err := repo.StaffBindings(context.Background(), int(e.playerID))
	if err != nil || !found {
		t.Fatalf("StaffBindings found=%v err=%v, want found", found, err)
	}
	if len(pitchIDs) != 2 || ownerID != int(e.ownerID) {
		t.Fatalf("StaffBindings = pitches %v owner %d, want 2 pitches under owner %d", pitchIDs, ownerID, e.ownerID)
	}

	// Idempotent re-invite (add the same pitches again) is a no-op, not an error.
	if _, err := repo.CreateStaffBindings(context.Background(),
		auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, []int{int(e.pitchID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash}); err != nil {
		t.Fatalf("idempotent re-invite: %v", err)
	}
	if ids, _, _, _ := repo.StaffBindings(context.Background(), int(e.playerID)); len(ids) != 2 {
		t.Fatalf("after re-invite bindings = %d, want still 2 (idempotent)", len(ids))
	}

	// Revoke drops ALL bindings under the owner and demotes to player.
	if err := repo.RevokeStaff(context.Background(), auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, int(e.playerID)); err != nil {
		t.Fatalf("RevokeStaff: %v", err)
	}
	if _, _, found, _ := repo.StaffBindings(context.Background(), int(e.playerID)); found {
		t.Fatalf("bindings still found after revoke, want none")
	}
	if e.userRole(t, e.playerID) != "player" {
		t.Fatalf("role = %q after revoke, want player (demoted)", e.userRole(t, e.playerID))
	}
}

// All-or-nothing ownership guard: if even ONE pitch in the set is not owned, the
// whole bind is rejected and nothing is written.
func TestStaff_MultiPitchRejectsForeignPitchAtomically(t *testing.T) {
	e := newBlockEnv(t)
	repo := NewStaffRepository(e.pool)
	// A pitch owned by the OTHER owner (e.otherID), not e.ownerID.
	foreign, err := e.model.CreatePitch(context.Background(), data.CreatePitchRequest{
		Name: "Foreign Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(e.otherID),
	})
	if err != nil {
		t.Fatalf("seed foreign pitch: %v", err)
	}
	foreignID := int64(foreign.ID)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM staff WHERE pitch_id = $1`, foreignID)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, foreignID)
	})

	// Set mixes an owned pitch with the foreign one → ErrPitchNotOwned, zero writes.
	_, err = repo.CreateStaffBindings(context.Background(),
		auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, []int{int(e.pitchID), int(foreignID)}, e.userPhone(t, e.playerID), StaffProvision{PasswordHash: staffTestHash})
	if !errors.Is(err, ErrPitchNotOwned) {
		t.Fatalf("err = %v, want ErrPitchNotOwned", err)
	}
	if _, _, found, _ := repo.StaffBindings(context.Background(), int(e.playerID)); found {
		t.Fatalf("a binding was written despite the foreign pitch; want all-or-nothing")
	}
	if e.userRole(t, e.playerID) != "player" {
		t.Fatalf("role = %q, want player (no promotion on rejected bind)", e.userRole(t, e.playerID))
	}
}
