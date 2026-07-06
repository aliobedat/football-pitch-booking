package handlers

// Unit tests for the Reports endpoints (R1) — routing, RBAC, validation, and
// error mapping over a real gin router mirroring the production middleware
// chain. No Postgres required (the repo is faked); the DB-backed money and
// predicate tests live in reports_db_test.go / repository/reports_repository_test.go.

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

type fakeReportsRepo struct {
	calls       int
	lastActor   auth.Actor
	lastPitchID int64
	lastFrom    time.Time
	lastTo      time.Time

	resolveErr error
	reportErr  error
	financial  repository.FinancialReport
	bookings   repository.BookingsReport
}

func (f *fakeReportsRepo) ResolveReportPitch(_ context.Context, actor auth.Actor, pitchID int64) (string, error) {
	f.lastActor = actor
	f.lastPitchID = pitchID
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return "Test Pitch", nil
}

func (f *fakeReportsRepo) OwnerFinancialReport(_ context.Context, actor auth.Actor, pitchID int64, from, to time.Time) (repository.FinancialReport, error) {
	f.calls++
	f.lastActor = actor
	f.lastPitchID = pitchID
	f.lastFrom = from
	f.lastTo = to
	return f.financial, f.reportErr
}

func (f *fakeReportsRepo) OwnerBookingsReport(_ context.Context, actor auth.Actor, pitchID int64, from, to time.Time, _ int) (repository.BookingsReport, error) {
	f.calls++
	f.lastActor = actor
	f.lastPitchID = pitchID
	f.lastFrom = from
	f.lastTo = to
	return f.bookings, f.reportErr
}

// newReportsRouter mounts both report routes behind the SAME RequireRole guard
// used in production, with an identity injector standing in for RequireAuth.
func newReportsRouter(h *ReportsHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.GET("/owner/reports/financial", inject, middleware.RequireRole("owner", "admin"), h.GetFinancialReport)
	r.GET("/owner/reports/bookings", inject, middleware.RequireRole("owner", "admin"), h.GetBookingsReport)
	return r
}

func TestReports_StaffForbidden(t *testing.T) {
	repo := &fakeReportsRepo{}
	r := newReportsRouter(NewReportsHandler(repo), 9, "staff")
	for _, path := range []string{
		"/owner/reports/financial?from=2026-06-01&to=2026-06-30",
		"/owner/reports/bookings?from=2026-06-01&to=2026-06-30",
	} {
		rec := doJSON(t, r, http.MethodGet, path, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403 for staff (body: %s)", path, rec.Code, rec.Body.String())
		}
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried %d times for a staff caller; must never run", repo.calls)
	}
}

func TestReports_PlayerForbidden(t *testing.T) {
	repo := &fakeReportsRepo{}
	r := newReportsRouter(NewReportsHandler(repo), 3, "player")
	rec := doJSON(t, r, http.MethodGet, "/owner/reports/financial?from=2026-06-01&to=2026-06-30", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a player", rec.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repo queried for a player; must not run")
	}
}

func TestReports_Validation400(t *testing.T) {
	cases := []struct {
		name, query, wantErr string
	}{
		{"missing both", "", "missing_param"},
		{"missing to", "?from=2026-06-01", "missing_param"},
		{"missing from", "?to=2026-06-30", "missing_param"},
		{"bad from", "?from=01/06/2026&to=2026-06-30", "invalid_date"},
		{"bad to", "?from=2026-06-01&to=June-30", "invalid_date"},
		{"to before from", "?from=2026-06-30&to=2026-06-01", "invalid_range"},
		{"range 93 days", "?from=2026-01-01&to=2026-04-03", "range_too_large"},
		{"bad pitch_id", "?from=2026-06-01&to=2026-06-30&pitch_id=abc", "invalid_pitch_id"},
		{"zero pitch_id", "?from=2026-06-01&to=2026-06-30&pitch_id=0", "invalid_pitch_id"},
	}
	for _, path := range []string{"/owner/reports/financial", "/owner/reports/bookings"} {
		for _, tc := range cases {
			repo := &fakeReportsRepo{}
			r := newReportsRouter(NewReportsHandler(repo), 42, "owner")
			rec := doJSON(t, r, http.MethodGet, path+tc.query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %s: status = %d, want 400 (body: %s)", path, tc.name, rec.Code, rec.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: decode: %v", path, tc.name, err)
			}
			if body.Error != tc.wantErr {
				t.Fatalf("%s %s: error = %q, want %q", path, tc.name, body.Error, tc.wantErr)
			}
			if repo.calls != 0 {
				t.Fatalf("%s %s: repo ran on invalid input", path, tc.name)
			}
		}
	}
}

func TestReports_RangeExactly92DaysAccepted(t *testing.T) {
	// 2026-01-01 .. 2026-04-02 inclusive = 92 days — the cap itself is allowed.
	repo := &fakeReportsRepo{}
	r := newReportsRouter(NewReportsHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/reports/financial?from=2026-01-01&to=2026-04-02", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 at the 92-day cap (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestReports_WindowIsAmmanCivilDays(t *testing.T) {
	// from=2026-03-05 to=2026-03-06 must hand the repo the UTC instants of
	// 2026-03-05 00:00 Amman (UTC+3 → 03-04T21:00Z) and 2026-03-07 00:00 Amman
	// (exclusive end → 03-06T21:00Z).
	repo := &fakeReportsRepo{}
	r := newReportsRouter(NewReportsHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/reports/financial?from=2026-03-05&to=2026-03-06", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	wantFrom := time.Date(2026, 3, 4, 21, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 6, 21, 0, 0, 0, time.UTC)
	if !repo.lastFrom.Equal(wantFrom) || !repo.lastTo.Equal(wantTo) {
		t.Fatalf("window = [%v, %v), want [%v, %v)", repo.lastFrom, repo.lastTo, wantFrom, wantTo)
	}
}

func TestReports_ForeignPitch404(t *testing.T) {
	repo := &fakeReportsRepo{resolveErr: repository.ErrPitchNotFound}
	r := newReportsRouter(NewReportsHandler(repo), 42, "owner")
	for _, path := range []string{"/owner/reports/financial", "/owner/reports/bookings"} {
		rec := doJSON(t, r, http.MethodGet, path+"?from=2026-06-01&to=2026-06-30&pitch_id=777", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 for out-of-scope pitch (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error != "not_found" {
			t.Fatalf("%s: error = %q, want not_found", path, body.Error)
		}
	}
	if repo.calls != 0 {
		t.Fatalf("report query ran despite an unresolvable pitch")
	}
}

func TestReports_TooManyRows422(t *testing.T) {
	repo := &fakeReportsRepo{reportErr: repository.ErrReportTooLarge}
	r := newReportsRouter(NewReportsHandler(repo), 42, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/reports/bookings?from=2026-06-01&to=2026-06-30", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for oversized result (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error != "too_many_rows" {
		t.Fatalf("error = %q, want too_many_rows", body.Error)
	}
}

func TestReports_EnvelopeShape(t *testing.T) {
	repo := &fakeReportsRepo{}
	r := newReportsRouter(NewReportsHandler(repo), 42, "owner")

	// Unfiltered → by_pitch present, no pitch_id/pitch_name.
	rec := doJSON(t, r, http.MethodGet, "/owner/reports/financial?from=2026-06-01&to=2026-06-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var unfiltered struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &unfiltered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := unfiltered.Data["by_pitch"]; !ok {
		t.Fatalf("unfiltered report missing by_pitch (keys: %v)", keysOf(unfiltered.Data))
	}
	if _, ok := unfiltered.Data["pitch_name"]; ok {
		t.Fatalf("unfiltered report must not carry pitch_name")
	}

	// Filtered → pitch_id + pitch_name present, no by_pitch.
	rec = doJSON(t, r, http.MethodGet, "/owner/reports/financial?from=2026-06-01&to=2026-06-30&pitch_id=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var filtered struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := filtered.Data["pitch_name"]; !ok {
		t.Fatalf("filtered report missing pitch_name (keys: %v)", keysOf(filtered.Data))
	}
	if _, ok := filtered.Data["by_pitch"]; ok {
		t.Fatalf("filtered report must not carry by_pitch")
	}
	// The from/to echo is the raw civil-date strings.
	var fromStr, toStr string
	_ = json.Unmarshal(filtered.Data["from"], &fromStr)
	_ = json.Unmarshal(filtered.Data["to"], &toStr)
	if fromStr != "2026-06-01" || toStr != "2026-06-30" {
		t.Fatalf("from/to echo = %q/%q, want the request's civil dates", fromStr, toStr)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
