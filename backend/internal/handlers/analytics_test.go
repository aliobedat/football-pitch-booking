package handlers

// Isolation tests for the finance/analytics boundary (Dashboard PR 2). The
// contract: staff (and players) are categorically barred from /owner/analytics —
// at the route (RequireRole) AND in the handler (defence in depth) — while
// owners see their own revenue and admins see everything. These run over a real
// gin router mirroring the production middleware chain; no Postgres required.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeAnalyticsRepo struct {
	calls       int
	lastActor   auth.Actor
	lastPitchID int
	summary     repository.RevenueSummary
}

func (f *fakeAnalyticsRepo) OwnerRevenueSummary(_ context.Context, actor auth.Actor, pitchID int) (repository.RevenueSummary, error) {
	f.calls++
	f.lastActor = actor
	f.lastPitchID = pitchID
	return f.summary, nil
}

func (f *fakeAnalyticsRepo) RevenueByDay(_ context.Context, _ auth.Actor, _, _ time.Time) ([]repository.DayPoint, error) {
	return nil, nil
}
func (f *fakeAnalyticsRepo) RevenueByMonth(_ context.Context, _ auth.Actor, _, _ time.Time) ([]repository.MonthPoint, error) {
	return nil, nil
}
func (f *fakeAnalyticsRepo) BookingHeatmap(_ context.Context, _ auth.Actor, _, _ time.Time) ([]repository.HeatCell, error) {
	return nil, nil
}
func (f *fakeAnalyticsRepo) Totals(_ context.Context, _ auth.Actor, _, _ time.Time) (repository.PeriodTotals, error) {
	return repository.PeriodTotals{}, nil
}

// newAnalyticsRouter mounts the finance route behind the SAME RequireRole guard
// used in production, with an identity injector standing in for RequireAuth.
func newAnalyticsRouter(h *AnalyticsHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.GET("/owner/analytics", inject, middleware.RequireRole("owner", "admin"), h.GetRevenueSummary)
	return r
}

func TestAnalytics_StaffForbidden(t *testing.T) {
	repo := &fakeAnalyticsRepo{}
	r := newAnalyticsRouter(NewAnalyticsHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodGet, "/owner/analytics", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff hitting finance (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.calls != 0 {
		t.Fatalf("analytics repo was queried %d times for a staff caller; the financial query must never run", repo.calls)
	}
}

func TestAnalytics_PlayerForbidden(t *testing.T) {
	repo := &fakeAnalyticsRepo{}
	r := newAnalyticsRouter(NewAnalyticsHandler(repo), 3, "player")
	rec := doJSON(t, r, http.MethodGet, "/owner/analytics", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a player hitting finance", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for a player; must not run")
	}
}

func TestAnalytics_OwnerScopedToSelf(t *testing.T) {
	repo := &fakeAnalyticsRepo{summary: repository.RevenueSummary{TotalRevenue: 1234.5, BookingCount: 7}}
	const ownerID = 42
	r := newAnalyticsRouter(NewAnalyticsHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/analytics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for owner (body: %s)", rec.Code, rec.Body.String())
	}
	// An owner is scoped to their own pitches — the actor handed to the repo must
	// be the owner themselves (the repo then applies OwnerScopeFilter).
	if repo.lastActor.UserID != ownerID || repo.lastActor.Role != "owner" {
		t.Fatalf("actor = %+v, want owner #%d (owner must only see their own revenue)", repo.lastActor, ownerID)
	}
	var body struct {
		Data repository.RevenueSummary `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.TotalRevenue != 1234.5 || body.Data.BookingCount != 7 {
		t.Fatalf("summary not surfaced: %+v", body.Data)
	}
}

func TestAnalytics_AdminUnscoped(t *testing.T) {
	repo := &fakeAnalyticsRepo{}
	r := newAnalyticsRouter(NewAnalyticsHandler(repo), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/owner/analytics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for admin", rec.Code)
	}
	// Admin is unscoped: the actor handed to the repo is the admin, whose
	// OwnerScopeFilter yields "TRUE" (all pitches).
	if !repo.lastActor.IsAdmin() {
		t.Fatalf("actor = %+v, want admin (unscoped)", repo.lastActor)
	}
}
