package handlers

// WO-MONITORING-V1 Gate 1 handler tests. No Postgres required — a fake
// MonitoringRepository stands in, and the router mirrors the production
// RequireRole("admin")-only chain (identity injector standing in for
// RequireAuth, same pattern as analytics_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeMonitoringRepo struct {
	calls int

	summary        repository.MonitoringBookingSummary
	recentBookings []repository.MonitoringBookingRow
	usage          repository.MonitoringWhatsAppUsage
	jobCounts      repository.MonitoringJobCounts
	recentFailures []repository.MonitoringFailedJob

	summaryErr error
}

func (f *fakeMonitoringRepo) BookingSummary(context.Context, time.Time, time.Time, repository.MonitoringFilter) (repository.MonitoringBookingSummary, error) {
	f.calls++
	return f.summary, f.summaryErr
}
func (f *fakeMonitoringRepo) RecentBookings(context.Context, time.Time, time.Time, repository.MonitoringFilter, int) ([]repository.MonitoringBookingRow, error) {
	f.calls++
	return f.recentBookings, nil
}
func (f *fakeMonitoringRepo) WhatsAppUsage(context.Context, string, time.Time) (repository.MonitoringWhatsAppUsage, error) {
	f.calls++
	return f.usage, nil
}
func (f *fakeMonitoringRepo) NotificationJobCounts(context.Context) (repository.MonitoringJobCounts, error) {
	f.calls++
	return f.jobCounts, nil
}
func (f *fakeMonitoringRepo) RecentFailedJobs(context.Context, int) ([]repository.MonitoringFailedJob, error) {
	f.calls++
	return f.recentFailures, nil
}

func newMonitoringRouter(h *MonitoringHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.GET("/admin/monitoring", inject, middleware.RequireRole("admin"), h.GetMonitoring)
	return r
}

func TestMonitoring_AdminOK_ExpectedShape(t *testing.T) {
	repo := &fakeMonitoringRepo{
		summary: repository.MonitoringBookingSummary{Total: 3, Confirmed: 2, Cancelled: 1},
		usage:   repository.MonitoringWhatsAppUsage{Count: 10, Cap: 250, Warning: false},
		recentBookings: []repository.MonitoringBookingRow{
			{ID: 1, ContactName: "P", ContactPhoneMasked: "***1234", Status: "confirmed"},
		},
		recentFailures: []repository.MonitoringFailedJob{
			{Kind: "booking_confirmed", Status: "dead_letter", Attempts: 5, FailureCategory: "quota_exhausted", RecipientMasked: "***5678"},
		},
	}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			SelectedDate   string                                    `json:"selected_date"`
			BookingSummary struct{ Total, Confirmed, Cancelled int } `json:"booking_summary"`
			RecentBookings []map[string]json.RawMessage              `json:"recent_bookings"`
			WhatsAppUsage  struct {
				Count, Cap, Remaining int
				Warning, Blocked      bool
			} `json:"whatsapp_usage"`
			NotificationJobs struct {
				RecentFailures []map[string]json.RawMessage `json:"recent_failures"`
			} `json:"notification_jobs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Data.SelectedDate == "" {
		t.Error("selected_date missing")
	}
	if resp.Data.BookingSummary.Total != 3 {
		t.Errorf("booking_summary.total = %d, want 3", resp.Data.BookingSummary.Total)
	}
	if len(resp.Data.RecentBookings) != 1 {
		t.Fatalf("recent_bookings = %d, want 1", len(resp.Data.RecentBookings))
	}
	if _, ok := resp.Data.RecentBookings[0]["contact_phone_masked"]; !ok {
		t.Error("recent_bookings row missing contact_phone_masked")
	}
	if resp.Data.WhatsAppUsage.Count != 10 || resp.Data.WhatsAppUsage.Cap != 250 || resp.Data.WhatsAppUsage.Remaining != 240 {
		t.Errorf("whatsapp_usage = %+v, want count=10 cap=250 remaining=240", resp.Data.WhatsAppUsage)
	}
	if len(resp.Data.NotificationJobs.RecentFailures) != 1 {
		t.Fatalf("recent_failures = %d, want 1", len(resp.Data.NotificationJobs.RecentFailures))
	}
	if _, ok := resp.Data.NotificationJobs.RecentFailures[0]["last_error"]; ok {
		t.Fatal("response leaked a raw last_error field — must only expose failure_category")
	}
}

func TestMonitoring_OwnerForbidden(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 5, "owner")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for owner (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried %d times for a forbidden owner caller; must never run", repo.calls)
	}
}

func TestMonitoring_StaffForbidden(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 6, "staff")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for staff; must not run")
	}
}

func TestMonitoring_PlayerForbidden(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 7, "player")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for player", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for player; must not run")
	}
}

func TestMonitoring_MalformedDate_Rejected(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring?date=not-a-date", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed date (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried %d times for a rejected date; must fail before hitting the repo", repo.calls)
	}
}

func TestMonitoring_MalformedVenueID_Rejected(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring?venue_id=-1", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for negative venue_id (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestMonitoring_MalformedStatus_Rejected(t *testing.T) {
	repo := &fakeMonitoringRepo{}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring?status=not_a_real_status", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid status (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestMonitoring_RepositoryError_NeutralInternalError(t *testing.T) {
	repo := &fakeMonitoringRepo{summaryErr: errors.New("db exploded")}
	r := newMonitoringRouter(NewMonitoringHandler(repo, "WABA1"), 1, "admin")
	rec := doJSON(t, r, http.MethodGet, "/admin/monitoring", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "db exploded") {
		t.Fatalf("raw repository error leaked into the response: %s", rec.Body.String())
	}
}
