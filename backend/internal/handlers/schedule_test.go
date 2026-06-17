package handlers

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

type fakeScheduleRepo struct {
	setErr        error
	row           *repository.ScheduleRow
	lastBound     []int
	lastBookingID int
	lastAttend    string
	lastPayment   string
	setCalls      int
}

func (f *fakeScheduleRepo) DailySchedule(_ context.Context, _ auth.Actor, boundPitchIDs []int, _ int, _, _ time.Time) ([]repository.ScheduleRow, error) {
	f.lastBound = boundPitchIDs
	return []repository.ScheduleRow{}, nil
}

func (f *fakeScheduleRepo) SetAttendance(_ context.Context, _ auth.Actor, boundPitchIDs []int, bookingID int, attendance string) (*repository.ScheduleRow, error) {
	f.setCalls++
	f.lastBound, f.lastBookingID, f.lastAttend = boundPitchIDs, bookingID, attendance
	if f.setErr != nil {
		return nil, f.setErr
	}
	if f.row != nil {
		return f.row, nil
	}
	return &repository.ScheduleRow{ID: int64(bookingID), Attendance: attendance}, nil
}

func (f *fakeScheduleRepo) SetPayment(_ context.Context, _ auth.Actor, boundPitchIDs []int, bookingID int, payment string) (*repository.ScheduleRow, error) {
	f.setCalls++
	f.lastBound, f.lastBookingID, f.lastPayment = boundPitchIDs, bookingID, payment
	if f.setErr != nil {
		return nil, f.setErr
	}
	if f.row != nil {
		return f.row, nil
	}
	return &repository.ScheduleRow{ID: int64(bookingID), PaymentStatus: payment}, nil
}

// inject mimics RequireAuth + ResolveScope: sets actor + bound-pitch scope.
func scheduleRouter(h *ScheduleHandler, userID int, role string, boundPitch int) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Set(middleware.ContextKeyActor, auth.Actor{UserID: userID, Role: role})
		c.Set(middleware.ContextKeyScope, auth.Scope{BoundPitchIDs: []int{boundPitch}, ProvisionedBy: 1})
		c.Next()
	}
	r.GET("/schedule", inject, middleware.RequireRole("staff", "owner", "admin"), h.GetDailySchedule)
	r.PATCH("/bookings/:id/attendance", inject, middleware.RequireRole("staff", "owner", "admin"), h.PatchAttendance)
	r.PATCH("/bookings/:id/payment", inject, middleware.RequireRole("staff", "owner", "admin"), h.PatchPayment)
	return r
}

func TestSchedule_PlayerForbidden(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 3, "player", 0)
	if rec := doJSON(t, r, http.MethodGet, "/schedule", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("GET /schedule player = %d, want 403", rec.Code)
	}
	rec := doJSON(t, r, http.MethodPatch, "/bookings/5/attendance", map[string]any{"attendance": "checked_in"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH attendance player = %d, want 403", rec.Code)
	}
	if repo.setCalls != 0 {
		t.Fatalf("repo touched for a player; route guard must block")
	}
}

func TestAttendance_OutOfScopeForbidden(t *testing.T) {
	repo := &fakeScheduleRepo{setErr: repository.ErrBookingNotInScope}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "staff", 7)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/attendance", map[string]any{"attendance": "no_show"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff acting on another pitch's booking (body: %s)", rec.Code, rec.Body.String())
	}
	// Staff's bound pitch must be what the repo is scoped to.
	if len(repo.lastBound) != 1 || repo.lastBound[0] != 7 {
		t.Fatalf("boundPitchIDs = %v, want [7]", repo.lastBound)
	}
}

func TestAttendance_InvalidValueRejected(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "staff", 7)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/attendance", map[string]any{"attendance": "present"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid attendance", rec.Code)
	}
	if repo.setCalls != 0 {
		t.Fatalf("repo called with invalid attendance; must validate first")
	}
}

func TestAttendance_StaffCheckInOK(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "staff", 7)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/attendance", map[string]any{"attendance": "checked_in"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.lastBookingID != 55 || repo.lastAttend != "checked_in" {
		t.Fatalf("repo got booking=%d attendance=%q, want 55/checked_in", repo.lastBookingID, repo.lastAttend)
	}
}

func TestPayment_InvalidValueRejected(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "owner", 0)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/payment", map[string]any{"payment_status": "card"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid payment_status", rec.Code)
	}
	if repo.setCalls != 0 {
		t.Fatalf("repo called with invalid payment; must validate first")
	}
}

func TestPayment_OutOfScopeForbidden(t *testing.T) {
	repo := &fakeScheduleRepo{setErr: repository.ErrBookingNotInScope}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "staff", 7)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/payment", map[string]any{"payment_status": "paid_cash"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for out-of-scope payment toggle", rec.Code)
	}
}

func TestPayment_PaidCashOK(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 9, "owner", 0)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/55/payment", map[string]any{"payment_status": "paid_cash"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.lastBookingID != 55 || repo.lastPayment != "paid_cash" {
		t.Fatalf("repo got booking=%d payment=%q, want 55/paid_cash", repo.lastBookingID, repo.lastPayment)
	}
}

func TestPayment_PlayerForbidden(t *testing.T) {
	repo := &fakeScheduleRepo{}
	r := scheduleRouter(NewScheduleHandler(repo), 3, "player", 0)
	rec := doJSON(t, r, http.MethodPatch, "/bookings/5/payment", map[string]any{"payment_status": "paid_cash"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH payment player = %d, want 403", rec.Code)
	}
	if repo.setCalls != 0 {
		t.Fatalf("repo touched for a player; route guard must block")
	}
}
