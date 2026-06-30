package handlers

// Role→outcome tests for staff management after the admin-permission fix:
//   - admin: invite/list/revoke across ANY owner's pitches/staff.
//   - owner: invite/list/revoke ONLY within their own pitches/staff.
//   - staff/player: blocked by the route guard (covered here for invite/revoke and
//     by the existing staff_test.go).
//
// These are handler-level tests over a modelStaffRepo that faithfully encodes the
// repository's authorization contract (admin-bypass vs strict owner scope), so the
// role→HTTP outcome is asserted end-to-end without a database. The actual SQL that
// implements the bypass is exercised by the DB-gated integration tests
// (staff_multi_pitch_test.go / cross_tenant_staff_mutation_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// modelStaffRepo models ownership: which owner owns each pitch, and which owner
// each staff user is bound under. It applies the SAME authority rule as the real
// repo (admin → any; owner → own only) so the handler's role→outcome is testable.
type modelStaffRepo struct {
	pitchOwner map[int]int // pitchID → ownerID (live pitches only)
	staffOwner map[int]int // staff userID → ownerID they're bound under
}

func (m *modelStaffRepo) StaffBindings(_ context.Context, _ int) ([]int, int, bool, error) {
	return nil, 0, false, nil
}

// resolveOwner mirrors resolveBindingOwnerTx: owner must own every pitch; admin
// needs all pitches live and sharing one owner. Returns the binding owner or
// ErrPitchNotOwned.
func (m *modelStaffRepo) resolveOwner(actor auth.Actor, pitchIDs []int) (int, error) {
	owners := map[int]struct{}{}
	for _, pid := range pitchIDs {
		oid, live := m.pitchOwner[pid]
		if !live {
			return 0, repository.ErrPitchNotOwned // deleted / non-existent
		}
		owners[oid] = struct{}{}
	}
	if actor.IsAdmin() {
		if len(owners) != 1 {
			return 0, repository.ErrPitchNotOwned // mixed-owner / empty set
		}
		for oid := range owners {
			return oid, nil
		}
	}
	// owner: must own every pitch.
	if len(owners) != 1 {
		return 0, repository.ErrPitchNotOwned
	}
	if _, ok := owners[actor.UserID]; !ok {
		return 0, repository.ErrPitchNotOwned
	}
	return actor.UserID, nil
}

func (m *modelStaffRepo) CreateStaffBindings(_ context.Context, actor auth.Actor, pitchIDs []int, phone string, _ repository.StaffProvision) (*repository.StaffMember, error) {
	ownerID, err := m.resolveOwner(actor, pitchIDs)
	if err != nil {
		return nil, err
	}
	pitches := make([]repository.StaffPitch, 0, len(pitchIDs))
	for _, pid := range pitchIDs {
		pitches = append(pitches, repository.StaffPitch{PitchID: pid})
	}
	return &repository.StaffMember{UserID: 99, OwnerID: ownerID, Phone: phone, Pitches: pitches}, nil
}

func (m *modelStaffRepo) ListStaff(_ context.Context, actor auth.Actor) ([]repository.StaffMember, error) {
	out := []repository.StaffMember{}
	for uid, oid := range m.staffOwner {
		if actor.IsAdmin() || oid == actor.UserID {
			out = append(out, repository.StaffMember{UserID: uid, OwnerID: oid})
		}
	}
	return out, nil
}

func (m *modelStaffRepo) RevokeStaff(_ context.Context, actor auth.Actor, staffUserID int) error {
	oid, ok := m.staffOwner[staffUserID]
	if !ok {
		return repository.ErrStaffBindingNotFound
	}
	if !actor.IsAdmin() && oid != actor.UserID {
		return repository.ErrStaffBindingNotFound // owner cannot revoke another owner's staff
	}
	return nil
}

// modelRouter wires invite/list/revoke with the SAME middleware the API uses,
// injecting the given session identity.
func modelRouter(repo repository.StaffRepository, userID int, role string) *gin.Engine {
	h := NewStaffHandler(repo, 10)
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/owner/staff", inject, middleware.RequireRole("owner", "admin"), h.InviteStaff)
	r.GET("/owner/staff", inject, middleware.RequireRole("owner", "admin"), h.ListStaff)
	r.DELETE("/owner/staff/:userId", inject, middleware.RequireRole("owner", "admin"), h.RevokeStaff)
	return r
}

// pitch 7 is owned by owner 100; pitch 8 by owner 200.
func twoOwnerModel() *modelStaffRepo {
	return &modelStaffRepo{
		pitchOwner: map[int]int{7: 100, 8: 200},
		staffOwner: map[int]int{99: 100, 88: 200},
	}
}

// ── Invite ────────────────────────────────────────────────────────────────────

// TestStaffInvite_Admin_AnyPitch: an admin (who owns nothing) can invite staff to
// a pitch owned by someone else → 201.
func TestStaffInvite_Admin_AnyPitch(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 1, "admin")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (admin → any pitch); body=%s", rec.Code, rec.Body.String())
	}
}

// TestStaffInvite_Owner_OwnPitch: owner invites to a pitch they own → 201.
func TestStaffInvite_Owner_OwnPitch(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 100, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (owner → own pitch); body=%s", rec.Code, rec.Body.String())
	}
}

// TestStaffInvite_Owner_ForeignPitch_403: owner invites to a pitch owned by a
// DIFFERENT owner → 403 (owner isolation preserved).
func TestStaffInvite_Owner_ForeignPitch_403(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 100, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 8)) // pitch 8 → owner 200
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (owner → foreign pitch); body=%s", rec.Code, rec.Body.String())
	}
}

// TestStaffInvite_Staff_403: a staff caller is blocked by the route guard before
// the repo runs.
func TestStaffInvite_Staff_403(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 99, "staff")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (staff barred at route)", rec.Code)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

// TestStaffList_Admin_SeesAll: admin sees staff across every owner.
func TestStaffList_Admin_SeesAll(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/owner/staff", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countStaff(t, rec.Body.Bytes()); n != 2 {
		t.Fatalf("admin sees %d staff, want 2 (all owners)", n)
	}
}

// TestStaffList_Owner_OwnOnly: owner sees only their own staff.
func TestStaffList_Owner_OwnOnly(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 100, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/staff", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countStaff(t, rec.Body.Bytes()); n != 1 {
		t.Fatalf("owner sees %d staff, want 1 (own only)", n)
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

// TestStaffRevoke_Admin_Any: admin can revoke a binding owned by another owner.
func TestStaffRevoke_Admin_Any(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 1, "admin")
	rec := doJSON(t, r, http.MethodDelete, "/owner/staff/88", nil) // 88 belongs to owner 200
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (admin → revoke any); body=%s", rec.Code, rec.Body.String())
	}
}

// TestStaffRevoke_Owner_OwnOnly: owner can revoke their own staff (200) but NOT
// another owner's (404) — isolation preserved.
func TestStaffRevoke_Owner_OwnOnly(t *testing.T) {
	r := modelRouter(twoOwnerModel(), 100, "owner")
	if rec := doJSON(t, r, http.MethodDelete, "/owner/staff/99", nil); rec.Code != http.StatusOK { // 99 → owner 100
		t.Fatalf("own revoke status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodDelete, "/owner/staff/88", nil); rec.Code != http.StatusNotFound { // 88 → owner 200
		t.Fatalf("foreign revoke status = %d, want 404 (owner isolation)", rec.Code)
	}
}

// countStaff decodes the {"data":[...]} envelope and returns the staff count.
func countStaff(t *testing.T, body []byte) int {
	t.Helper()
	var resp struct {
		Data []repository.StaffMember `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode list response: %v; body=%s", err, string(body))
	}
	return len(resp.Data)
}
