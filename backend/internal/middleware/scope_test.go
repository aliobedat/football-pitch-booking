package middleware

// Tests for the central scope guard (Dashboard PR 2). ResolveScope must:
//   - inject the DB-resolved binding for a staff actor,
//   - hard-reject (403) a staff actor with NO binding (un-provisioned),
//   - pass non-staff through with an empty scope (no DB hit).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
)

type fakeResolver struct {
	pitchID int
	ownerID int
	found   bool
	err     error
	calls   int
}

func (f *fakeResolver) StaffBinding(_ context.Context, _ int) (int, int, bool, error) {
	f.calls++
	return f.pitchID, f.ownerID, f.found, f.err
}

func runScope(role string, resolver StaffScopeResolver) (*httptest.ResponseRecorder, auth.Scope, bool) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var captured auth.Scope
	var reached bool
	inject := func(c *gin.Context) {
		c.Set(ContextKeyUserID, 9)
		c.Set(ContextKeyRole, role)
		c.Next()
	}
	r.GET("/x", inject, ResolveScope(resolver), func(c *gin.Context) {
		reached = true
		captured = GetScope(c)
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(rec, req)
	return rec, captured, reached
}

func TestResolveScope_StaffWithBindingInjected(t *testing.T) {
	resolver := &fakeResolver{pitchID: 7, ownerID: 42, found: true}
	rec, scope, reached := runScope(auth.RoleStaff, resolver)
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d reached=%v, want 200 and handler reached", rec.Code, reached)
	}
	if scope.BoundPitchID != 7 || scope.ProvisionedBy != 42 {
		t.Fatalf("scope = %+v, want pitch 7 / owner 42 injected", scope)
	}
}

func TestResolveScope_StaffWithoutBindingForbidden(t *testing.T) {
	resolver := &fakeResolver{found: false}
	rec, _, reached := runScope(auth.RoleStaff, resolver)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for un-provisioned staff", rec.Code)
	}
	if reached {
		t.Fatalf("handler was reached for un-provisioned staff; guard must abort")
	}
}

func TestResolveScope_NonStaffPassThroughNoDBHit(t *testing.T) {
	for _, role := range []string{auth.RoleOwner, auth.RoleAdmin, auth.RolePlayer} {
		resolver := &fakeResolver{pitchID: 7, ownerID: 42, found: true}
		rec, scope, reached := runScope(role, resolver)
		if rec.Code != http.StatusOK || !reached {
			t.Fatalf("role %s: status = %d reached=%v, want 200/reached", role, rec.Code, reached)
		}
		if resolver.calls != 0 {
			t.Fatalf("role %s: resolver hit %d times; non-staff must not query the staff table", role, resolver.calls)
		}
		if scope.IsStaffBound() {
			t.Fatalf("role %s: got staff-bound scope %+v, want empty", role, scope)
		}
	}
}
