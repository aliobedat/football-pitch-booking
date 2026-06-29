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

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeStaffRepo struct {
	createErr      error
	created        *repository.StaffMember
	createCalls    int
	lastActor      auth.Actor
	lastPitchIDs   []int
	lastPhone      string
	bindingPitches []int
	bindingOwner   int
	bindingFound   bool
	bindingErr     error

	revokeErr       error
	revokeCalls     int
	lastRevokeActor auth.Actor
	lastRevokeUser  int
}

func (f *fakeStaffRepo) StaffBindings(_ context.Context, _ int) ([]int, int, bool, error) {
	return f.bindingPitches, f.bindingOwner, f.bindingFound, f.bindingErr
}

func (f *fakeStaffRepo) CreateStaffBindings(_ context.Context, actor auth.Actor, pitchIDs []int, phone string) (*repository.StaffMember, error) {
	f.createCalls++
	f.lastActor, f.lastPitchIDs, f.lastPhone = actor, pitchIDs, phone
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created != nil {
		return f.created, nil
	}
	pitches := make([]repository.StaffPitch, 0, len(pitchIDs))
	for _, pid := range pitchIDs {
		pitches = append(pitches, repository.StaffPitch{PitchID: pid})
	}
	return &repository.StaffMember{UserID: 99, OwnerID: actor.UserID, Phone: phone, Pitches: pitches}, nil
}

func (f *fakeStaffRepo) ListStaff(_ context.Context, _ auth.Actor) ([]repository.StaffMember, error) {
	return nil, nil
}

func (f *fakeStaffRepo) RevokeStaff(_ context.Context, actor auth.Actor, staffUserID int) error {
	f.revokeCalls++
	f.lastRevokeActor, f.lastRevokeUser = actor, staffUserID
	return f.revokeErr
}

func newStaffRouter(h *StaffHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/owner/staff", inject, middleware.RequireRole("owner", "admin"), h.InviteStaff)
	r.DELETE("/owner/staff/:userId", inject, middleware.RequireRole("owner", "admin"), h.RevokeStaff)
	return r
}

// inviteBody is the multi-pitch invite payload helper.
func inviteBody(phone string, pitchIDs ...int) map[string]any {
	return map[string]any{"phone": phone, "pitch_ids": pitchIDs}
}

// The KEY isolation guarantee: binding to a pitch the owner does not own → 403.
func TestInviteStaff_NotOwnedPitchForbidden(t *testing.T) {
	repo := &fakeStaffRepo{createErr: repository.ErrPitchNotOwned}
	r := newStaffRouter(NewStaffHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 binding staff to an unowned pitch (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestInviteStaff_StaffCannotInvite(t *testing.T) {
	repo := &fakeStaffRepo{}
	r := newStaffRouter(NewStaffHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff inviting staff", rec.Code)
	}
	if repo.createCalls != 0 {
		t.Fatalf("repo.CreateStaffBindings ran for a staff caller; route guard must block first")
	}
}

func TestInviteStaff_Success(t *testing.T) {
	repo := &fakeStaffRepo{}
	const ownerID = 42
	r := newStaffRouter(NewStaffHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7, 9))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	// The actor MUST come from the session (id + role), and the phone normalised.
	if repo.lastActor.UserID != ownerID || repo.lastActor.Role != "owner" {
		t.Fatalf("actor = %+v, want {UserID:%d Role:owner} (binding scoped to the acting owner)", repo.lastActor, ownerID)
	}
	// The full multi-pitch set is passed through (1:N).
	if len(repo.lastPitchIDs) != 2 || repo.lastPitchIDs[0] != 7 || repo.lastPitchIDs[1] != 9 {
		t.Fatalf("pitchIDs = %v, want [7 9]", repo.lastPitchIDs)
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
	// Actor MUST come from the session, not the request — the isolation guard.
	if repo.lastRevokeActor.UserID != ownerID || repo.lastRevokeUser != 99 {
		t.Fatalf("revoke args = (actor %+v, user %d), want (owner %d, 99)", repo.lastRevokeActor, repo.lastRevokeUser, ownerID)
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
		{"user not found", repository.ErrStaffUserNotFound, http.StatusNotFound},
		{"privileged target", repository.ErrCannotBindPrivileged, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeStaffRepo{createErr: tc.err}
			r := newStaffRouter(NewStaffHandler(repo), 42, "owner")
			rec := doJSON(t, r, http.MethodPost, "/owner/staff", inviteBody("0791234567", 7))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d for %s (body: %s)", rec.Code, tc.want, tc.name, rec.Body.String())
			}
		})
	}
}
