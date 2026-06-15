package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeCRMRepo struct {
	calls     int
	lastActor auth.Actor
}

func (f *fakeCRMRepo) OwnerCRM(_ context.Context, actor auth.Actor) ([]repository.CRMRow, error) {
	f.calls++
	f.lastActor = actor
	return []repository.CRMRow{}, nil
}

func roleRouter(method, path string, h gin.HandlerFunc, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Set(middleware.ContextKeyActor, auth.Actor{UserID: userID, Role: role})
		c.Next()
	}
	r.Handle(method, path, inject, middleware.RequireRole("owner", "admin"), h)
	return r
}

func TestCRM_StaffAndPlayerForbidden(t *testing.T) {
	for _, role := range []string{"staff", "player"} {
		repo := &fakeCRMRepo{}
		r := roleRouter(http.MethodGet, "/owner/crm", NewCRMHandler(repo).GetCRM, 9, role)
		if rec := doJSON(t, r, http.MethodGet, "/owner/crm", nil); rec.Code != http.StatusForbidden {
			t.Fatalf("role %s CRM = %d, want 403", role, rec.Code)
		}
		if repo.calls != 0 {
			t.Fatalf("role %s reached CRM repo; route guard must block", role)
		}
	}
}

func TestCRM_OwnerScopedToSelf(t *testing.T) {
	repo := &fakeCRMRepo{}
	r := roleRouter(http.MethodGet, "/owner/crm", NewCRMHandler(repo).GetCRM, 42, "owner")
	if rec := doJSON(t, r, http.MethodGet, "/owner/crm", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner CRM = %d, want 200", rec.Code)
	}
	if repo.lastActor.UserID != 42 || repo.lastActor.Role != "owner" {
		t.Fatalf("CRM actor = %+v, want owner #42 (scoped to self)", repo.lastActor)
	}
}

func TestAnalyticsOverview_StaffForbidden(t *testing.T) {
	repo := &fakeAnalyticsRepo{}
	r := roleRouter(http.MethodGet, "/owner/analytics/overview", NewAnalyticsHandler(repo).GetAnalyticsOverview, 9, "staff")
	if rec := doJSON(t, r, http.MethodGet, "/owner/analytics/overview", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("staff overview = %d, want 403", rec.Code)
	}
}

func TestAnalyticsOverview_OwnerOK(t *testing.T) {
	repo := &fakeAnalyticsRepo{}
	r := roleRouter(http.MethodGet, "/owner/analytics/overview", NewAnalyticsHandler(repo).GetAnalyticsOverview, 42, "owner")
	if rec := doJSON(t, r, http.MethodGet, "/owner/analytics/overview", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner overview = %d, want 200", rec.Code)
	}
}
