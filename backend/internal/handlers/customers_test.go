package handlers

// Isolation tests for the Regulars CRM boundary (Cockpit WO1). Contract: staff and
// players are categorically barred from /owner/customers (route RequireRole AND
// the handler's defence-in-depth guard); owners reach the repo as themselves
// (the repo then applies OwnerScopeFilter), admins unscoped. No Postgres required.

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
)

type fakeCustomerRepo struct {
	calls     int
	lastActor auth.Actor
}

func (f *fakeCustomerRepo) ListCustomers(_ context.Context, actor auth.Actor, _, _ string) ([]models.CustomerListItem, error) {
	f.calls++
	f.lastActor = actor
	return []models.CustomerListItem{}, nil
}

func (f *fakeCustomerRepo) GetCustomerProfile(_ context.Context, actor auth.Actor, _ int64) (*models.CustomerProfile, error) {
	f.calls++
	f.lastActor = actor
	return &models.CustomerProfile{}, nil
}

func (f *fakeCustomerRepo) UpdateNotes(_ context.Context, actor auth.Actor, _ int64, _ string) (*models.Customer, error) {
	f.calls++
	f.lastActor = actor
	return &models.Customer{}, nil
}

func (f *fakeCustomerRepo) AssociateBookingCustomer(_ context.Context, _ int64) error { return nil }

func newCustomerRouter(h *CustomerHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	g := middleware.RequireRole("owner", "admin")
	r.GET("/owner/customers", inject, g, h.GetCustomers)
	r.GET("/owner/customers/:id", inject, g, h.GetCustomerProfile)
	r.PATCH("/owner/customers/:id/notes", inject, g, h.PatchCustomerNotes)
	return r
}

func TestCustomers_StaffForbidden(t *testing.T) {
	repo := &fakeCustomerRepo{}
	r := newCustomerRouter(NewCustomerHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodGet, "/owner/customers", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff hitting CRM", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("CRM repo queried %d times for staff; must never run", repo.calls)
	}
}

func TestCustomers_PlayerForbidden(t *testing.T) {
	repo := &fakeCustomerRepo{}
	r := newCustomerRouter(NewCustomerHandler(repo), 3, "player")
	rec := doJSON(t, r, http.MethodGet, "/owner/customers/1", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for player hitting CRM", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for a player; must not run")
	}
}

func TestCustomers_OwnerScopedToSelf(t *testing.T) {
	repo := &fakeCustomerRepo{}
	const ownerID = 42
	r := newCustomerRouter(NewCustomerHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/customers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.lastActor.UserID != ownerID || repo.lastActor.Role != "owner" {
		t.Fatalf("actor = %+v, want owner #%d (owner must only see their own customers)", repo.lastActor, ownerID)
	}
}

func TestCustomers_AdminUnscoped(t *testing.T) {
	repo := &fakeCustomerRepo{}
	r := newCustomerRouter(NewCustomerHandler(repo), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/owner/customers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for admin", rec.Code)
	}
	if !repo.lastActor.IsAdmin() {
		t.Fatalf("actor = %+v, want admin (unscoped)", repo.lastActor)
	}
}

func TestCustomers_NotesTooLong(t *testing.T) {
	repo := &fakeCustomerRepo{}
	r := newCustomerRouter(NewCustomerHandler(repo), 42, "owner")
	long := make([]byte, 2001)
	for i := range long {
		long[i] = 'a'
	}
	rec := doJSON(t, r, http.MethodPatch, "/owner/customers/1/notes", map[string]string{"notes": string(long)})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for oversize notes", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo wrote despite oversize notes; must reject first")
	}
}
