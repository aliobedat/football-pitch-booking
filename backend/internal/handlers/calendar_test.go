package handlers

// Isolation tests for the Visual Calendar boundary (Cockpit WO2). The read calendar
// is owner/admin only — staff and players are barred at the route (RequireRole) AND
// re-asserted in the handler; owners reach the repo as themselves (repo applies
// OwnerScopeFilter), admins unscoped. Bad dates are rejected before any query. No
// Postgres required.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeCalendarRepo struct {
	calls     int
	lastActor auth.Actor
	lastDate  time.Time
}

func (f *fakeCalendarRepo) OwnerDayCalendar(_ context.Context, actor auth.Actor, day time.Time) (*repository.CalendarDay, error) {
	f.calls++
	f.lastActor = actor
	f.lastDate = day
	return &repository.CalendarDay{Date: day.Format("2006-01-02"), Pitches: []repository.CalendarPitchRow{}}, nil
}

func newCalendarRouter(h *CalendarHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.GET("/owner/calendar", inject, middleware.RequireRole("owner", "admin"), h.GetDayCalendar)
	return r
}

func TestCalendar_StaffForbidden(t *testing.T) {
	repo := &fakeCalendarRepo{}
	r := newCalendarRouter(NewCalendarHandler(repo), 9, "staff")
	rec := doJSON(t, r, http.MethodGet, "/owner/calendar", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff hitting calendar", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("calendar repo queried %d times for staff; must never run", repo.calls)
	}
}

func TestCalendar_PlayerForbidden(t *testing.T) {
	repo := &fakeCalendarRepo{}
	r := newCalendarRouter(NewCalendarHandler(repo), 3, "player")
	rec := doJSON(t, r, http.MethodGet, "/owner/calendar", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for player", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for player; must not run")
	}
}

func TestCalendar_OwnerScopedToSelf(t *testing.T) {
	repo := &fakeCalendarRepo{}
	const ownerID = 42
	r := newCalendarRouter(NewCalendarHandler(repo), ownerID, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/calendar?date=2026-06-16", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.lastActor.UserID != ownerID || repo.lastActor.Role != "owner" {
		t.Fatalf("actor = %+v, want owner #%d", repo.lastActor, ownerID)
	}
	if got := repo.lastDate.Format("2006-01-02"); got != "2026-06-16" {
		t.Fatalf("date passed to repo = %s, want 2026-06-16", got)
	}
}

func TestCalendar_AdminUnscoped(t *testing.T) {
	repo := &fakeCalendarRepo{}
	r := newCalendarRouter(NewCalendarHandler(repo), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/owner/calendar", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for admin", rec.Code)
	}
	if !repo.lastActor.IsAdmin() {
		t.Fatalf("actor = %+v, want admin (unscoped)", repo.lastActor)
	}
}

func TestCalendar_InvalidDate(t *testing.T) {
	repo := &fakeCalendarRepo{}
	r := newCalendarRouter(NewCalendarHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/calendar?date=16-06-2026", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed date", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried despite bad date; must reject first")
	}
}
