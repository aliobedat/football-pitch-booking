package handlers

// Isolation tests for owner-scoped staff provisioning (Dashboard PR 2). The core
// invariant: an owner may bind a staff member ONLY to a pitch they own — a bind
// against an unowned pitch surfaces ErrPitchNotOwned → 403. Also: staff/players
// cannot invite at all (route guard), and the sentinel errors map to precise codes.

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeStaffRepo struct {
	createErr    error
	created      *repository.StaffBinding
	createCalls  int
	lastOwnerID  int
	lastPitchID  int
	lastPhone    string
	bindingPitch int
	bindingOwner int
	bindingFound bool
	bindingErr   error

	revokeErr       error
	revokeCalls     int
	lastRevokeOwner int
	lastRevokeUser  int
}

func (f *fakeStaffRepo) StaffBinding(_ context.Context, _ int) (int, int, bool, error) {
	return f.bindingPitch, f.bindingOwner, f.bindingFound, f.bindingErr
}

func (f *fakeStaffRepo) CreateStaffBinding(_ context.Context, ownerID, pitchID int, phone string) (*repository.StaffBinding, error) {
	f.createCalls++
	f.lastOwnerID, f.lastPitchID, f.lastPhone = ownerID, pitchID, phone
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created != nil {
		return f.created, nil
	}
	return &repository.StaffBinding{ID: 1, UserID: 99, PitchID: pitchID, OwnerID: ownerID, Phone: phone}, nil
}

func (f *fakeStaffRepo) ListStaffForOwner(_ context.Context, _ int) ([]repository.StaffBinding, error) {
	return nil, nil
}

func (f *fakeStaffRepo) RevokeStaff(_ context.Context, ownerID, staffUserID int) error {
	f.revokeCalls++
	f.lastRevokeOwner, f.lastRevokeUser = ownerID, staffUserID
	return f.revokeErr
}

func newStaffRouter(h *StaffHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches/:id/staff", inject, middleware.RequireRole("owner", "admin"), h.InviteStaff)
	r.DELETE("/owner/staff/:userId", inject, middleware.RequireRole("owner", "admin"), h.RevokeStaff)
	return r
}

// The KEY isolation guarantee: binding to a pitch the owner does not own → 403.
func TestInviteStaff_NotOwnedPitchForbidden(t *testing.T) {
	repo := &fakeStaffRepo{createErr: repository.ErrPitchNotOwned}
	r := newStaffRouter(NewStaffHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/pitches/7/staff", map[string]any{"phone": "0791234567"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 binding staff to an unowned pitch (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestInviteStaff_StaffCannotInvite(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodPost, "/pitches/7/staff", map[string]any{"phone": "0791234567"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff inviting staff", rec.Code)
	}
	if repo.createCalls != 0 {
		t.Fatalf("repo.CreateStaffBinding ran for a staff caller; route guard must block first")
	}
}

func TestInviteStaff_Success(t *testing.T) {
	repo := &fakeStaffRepo{}
	const ownerID = 42
	r := newStaffRouter(NewStaffHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodPost, "/pitches/7/staff", map[string]any{"phone": "0791234567"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	// The owner id MUST come from the session, and the phone normalised to E.164.
	if repo.lastOwnerID != ownerID {
		t.Fatalf("ownerID = %d, want %d (binding must be scoped to the acting owner)", repo.lastOwnerID, ownerID)
	}
	if repo.lastPitchID != 7 {
		t.Fatalf("pitchID = %d, want 7", repo.lastPitchID)
	}
	if repo.lastPhone != "+962791234567" {
		t.Fatalf("phone = %q, want normalised +962791234567", repo.lastPhone)
	}
}

func TestRevokeStaff_Success(t *testing.T) {
	repo := &fakeStaffRepo{}
	const ownerID = 42
	r := newStaffRouter(NewStaffHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodDelete, "/owner/staff/99", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Owner id MUST come from the session, not the request — the isolation guard.
	if repo.lastRevokeOwner != ownerID || repo.lastRevokeUser != 99 {
		t.Fatalf("revoke args = (owner %d, user %d), want (%d, 99)", repo.lastRevokeOwner, repo.lastRevokeUser, ownerID)
	}
}

func TestRevokeStaff_ForeignBindingNotFound(t *testing.T) {
	// The repo reports no binding under this owner (e.g. another owner's staff) → 404.
	repo := &fakeStaffRepo{revokeErr: repository.ErrStaffBindingNotFound}
	r := newStaffRouter(NewStaffHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodDelete, "/owner/staff/99", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a binding outside the owner's scope", rec.Code)
	}
}

func TestRevokeStaff_StaffCannotRevoke(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodDelete, "/owner/staff/99", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a staff caller", rec.Code)
	}
	if repo.revokeCalls != 0 {
		t.Fatalf("RevokeStaff ran for a staff caller; route guard must block first")
	}
}

func TestInviteStaff_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"already bound", repository.ErrStaffAlreadyBound, http.StatusConflict},
		{"user not found", repository.ErrStaffUserNotFound, http.StatusNotFound},
		{"privileged target", repository.ErrCannotBindPrivileged, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeStaffRepo{createErr: tc.err}
			r := newStaffRouter(NewStaffHandler(repo), 42, "owner")
			rec := doJSON(t, r, http.MethodPost, "/pitches/7/staff", map[string]any{"phone": "0791234567"})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d for %s (body: %s)", rec.Code, tc.want, tc.name, rec.Body.String())
			}
		})
	}
}
