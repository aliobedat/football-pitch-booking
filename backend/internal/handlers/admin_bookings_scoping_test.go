package handlers

// WO-STAFF-BOOKINGS-LOCKOUT — GET /admin/bookings role gating + tenant scoping,
// exercised at the HTTP layer through the REAL middleware chain (RequireAuth →
// RequireCSRF → ResolveScope → RequireRole → handler → repo) against a LIVE
// database.
//
// The route is owner/admin ONLY: staff — bound or unprovisioned — receive 403,
// including on every filter variant (the filters are query params on the same
// endpoint, so a param must never bypass the role gate). Owner/admin behavior is
// unchanged: same scoping (owner sees owned pitches only, admin sees all) and
// the same response shape as before the lockout.
//
// Regression guards for the shared-endpoint risk: the staff Day View surface
// (GET /schedule, PATCH /bookings/:id/attendance) must keep working for a bound
// staff member after the lockout.
//
// Reuses the §5.1 xtEnv seeding (two tenants: owner A owns pitches A1+A2 with
// bookings BK_A1/BK_A2; owner B owns B1 with BK_B; staff_A bound to A1 only).
// Assertions are membership checks over the THREE known seeded booking ids, so
// other rows already present in the shared DB never affect the result. The env
// setup asserts scratch-schema freshness (testutil.AssertSchemaBaseline).
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run AdminBookingsScoping -v

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

func TestAdminBookingsScoping(t *testing.T) {
	e := newXTEnv(t)

	// Real chain for the read endpoint, mirroring routes.go EXACTLY — including
	// the owner/admin-only RequireRole (staff removed, WO-STAFF-BOOKINGS-LOCKOUT).
	// service=nil: GetAllBookings is a pure read path (repo only).
	h := NewBookingHandler(e.pool, nil)
	r := gin.New()
	grp := r.Group("/")
	grp.Use(middleware.RequireAuth(e.jwt))
	grp.Use(middleware.RequireCSRF())
	grp.Use(middleware.ResolveScope(repository.NewStaffRepository(e.pool)))
	grp.GET("/admin/bookings", middleware.RequireRole("owner", "admin"), h.GetAllBookings)

	get := func(t *testing.T, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// rows decodes a 200 response into raw per-row JSON objects keyed by id.
	rows := func(t *testing.T, token string) map[int64]map[string]json.RawMessage {
		t.Helper()
		rec := get(t, "/admin/bookings", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/bookings = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		out := make(map[int64]map[string]json.RawMessage, len(resp.Data))
		for _, row := range resp.Data {
			var id int64
			if err := json.Unmarshal(row["id"], &id); err != nil {
				t.Fatalf("row missing numeric id: %v", err)
			}
			out[id] = row
		}
		return out
	}

	// assertShape asserts the pre-lockout response shape is intact on one row —
	// the fields the dashboard Bookings page renders (owner/admin unchanged).
	assertShape := func(t *testing.T, row map[string]json.RawMessage) {
		t.Helper()
		for _, k := range []string{
			"id", "pitch_id", "pitch_name", "user_name", "user_phone",
			"start_time", "end_time", "status", "source", "total_price",
			"payment_status", "created_at",
		} {
			if _, ok := row[k]; !ok {
				t.Fatalf("response shape changed: field %q missing from row (row: %v)", k, row)
			}
		}
	}

	// CASE 1 — Bound staff: 403 at RequireRole. A provisioned, correctly bound
	// staff member is the strongest case — scope is valid, the ROLE is what bars.
	t.Run("staff_forbidden", func(t *testing.T) {
		rec := get(t, "/admin/bookings", e.staffAToken)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("bound staff = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	// CASE 2 — Filter variants must not bypass the role gate: every query-param
	// combination the Bookings page sends still yields 403 for staff.
	t.Run("staff_forbidden_all_filter_variants", func(t *testing.T) {
		for _, q := range []string{
			"?status=confirmed",
			"?from=2026-01-01",
			"?to=2026-12-31",
			"?status=cancelled&from=2026-01-01&to=2026-12-31",
		} {
			rec := get(t, "/admin/bookings"+q, e.staffAToken)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("staff %s = %d, want 403 (body: %s)", q, rec.Code, rec.Body.String())
			}
		}
	})

	// CASE 3 — Unprovisioned staff: also 403 (ResolveScope or RequireRole — either
	// way, never a 200).
	t.Run("unprovisioned_staff_forbidden", func(t *testing.T) {
		rec := get(t, "/admin/bookings", e.unboundToken)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("unprovisioned staff = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	// CASE 4 — Owner: 200, owned pitches only (A1+A2, never B), shape unchanged.
	t.Run("owner_ok_scoped_shape_unchanged", func(t *testing.T) {
		got := rows(t, e.ownerAToken)
		if got[e.bkA1] == nil || got[e.bkA2] == nil {
			t.Fatalf("owner A must see both owned bookings A1=%v A2=%v", got[e.bkA1] != nil, got[e.bkA2] != nil)
		}
		if got[e.bkB] != nil {
			t.Fatalf("CRITICAL: owner A leaked BK_B (%d) — cross-tenant read", e.bkB)
		}
		assertShape(t, got[e.bkA1])
	})

	// CASE 5 — Admin: 200, sees all seeded bookings (membership, not exact count —
	// the shared DB holds other rows), shape unchanged.
	t.Run("admin_ok_sees_all_shape_unchanged", func(t *testing.T) {
		got := rows(t, e.adminToken)
		if got[e.bkA1] == nil || got[e.bkA2] == nil || got[e.bkB] == nil {
			t.Fatalf("admin must see all seeded bookings A1=%v A2=%v B=%v",
				got[e.bkA1] != nil, got[e.bkA2] != nil, got[e.bkB] != nil)
		}
		assertShape(t, got[e.bkB])
	})

	// CASE 6 — Regression guard: the staff Day View read (GET /schedule) still
	// returns 200 for bound staff after the lockout (separate endpoint, e.router).
	t.Run("staff_schedule_still_ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
		req.Header.Set("Authorization", "Bearer "+e.staffAToken)
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("staff GET /schedule = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	// CASE 7 — Regression guard: staff attendance marking on their bound pitch
	// still succeeds (the other Day View surface).
	t.Run("staff_attendance_still_ok", func(t *testing.T) {
		rec := e.patch(t, fmt.Sprintf("/bookings/%d/attendance", e.bkA1), e.staffAToken,
			map[string]any{"attendance": "checked_in"})
		if rec.Code != http.StatusOK {
			t.Fatalf("staff PATCH attendance (bound) = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})
}
