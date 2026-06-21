package handlers

// §5.2 — GET /admin/bookings tenant scoping, exercised at the HTTP layer through
// the REAL middleware chain (RequireAuth → RequireCSRF → ResolveScope →
// RequireRole → handler → repo) against a LIVE database.
//
// The repo-level scoping is covered by repository/booking_scoping_test.go; this
// fills the integrated-path gap: prove the listing a caller actually RECEIVES
// over HTTP contains ONLY rows they may see — a staff member sees only their
// bound pitch(es), an owner only owned pitches, an admin everything, and an
// unprovisioned staff is 403'd at ResolveScope before the handler runs.
//
// Reuses the §5.1 xtEnv seeding (two tenants: owner A owns pitches A1+A2 with
// bookings BK_A1/BK_A2; owner B owns B1 with BK_B; staff_A bound to A1 only).
// Assertions are membership checks over the THREE known seeded booking ids, so
// other rows already present in the shared DB never affect the result.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run AdminBookingsScoping -v

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

func TestAdminBookingsScoping(t *testing.T) {
	e := newXTEnv(t)

	// Real chain for the read endpoint. service=nil: GetAllBookings is a pure read
	// path (repo only); the write-path service is never touched.
	h := NewBookingHandler(e.pool, nil)
	r := gin.New()
	grp := r.Group("/")
	grp.Use(middleware.RequireAuth(e.jwt))
	grp.Use(middleware.RequireCSRF())
	grp.Use(middleware.ResolveScope(repository.NewStaffRepository(e.pool)))
	grp.GET("/admin/bookings", middleware.RequireRole("staff", "owner", "admin"), h.GetAllBookings)

	// returnedIDs issues the authed GET and returns the set of booking ids in the
	// response body (fails the test on non-200).
	returnedIDs := func(t *testing.T, token string) map[int64]bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/admin/bookings", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/bookings = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data []struct {
				ID      int64 `json:"id"`
				PitchID int64 `json:"pitch_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		ids := make(map[int64]bool, len(resp.Data))
		for _, b := range resp.Data {
			ids[b.ID] = true
		}
		return ids
	}

	// CASE 1 — Staff bound to A1 sees ONLY BK_A1; never BK_A2 (same owner, unbound)
	// or BK_B (other owner). This is the core tenant-isolation read assertion.
	t.Run("staff_sees_only_bound_pitch", func(t *testing.T) {
		ids := returnedIDs(t, e.staffAToken)
		if !ids[e.bkA1] {
			t.Fatalf("staff bound to A1 must see BK_A1 (%d)", e.bkA1)
		}
		if ids[e.bkA2] {
			t.Fatalf("CRITICAL: staff leaked BK_A2 (%d) — same owner but UNBOUND pitch", e.bkA2)
		}
		if ids[e.bkB] {
			t.Fatalf("CRITICAL: staff leaked BK_B (%d) — another tenant's booking", e.bkB)
		}
	})

	// CASE 2 — Owner A sees both owned pitches (A1+A2), never owner B's BK_B.
	t.Run("owner_sees_owned_not_cross_tenant", func(t *testing.T) {
		ids := returnedIDs(t, e.ownerAToken)
		if !ids[e.bkA1] || !ids[e.bkA2] {
			t.Fatalf("owner A must see both owned bookings A1=%v A2=%v", ids[e.bkA1], ids[e.bkA2])
		}
		if ids[e.bkB] {
			t.Fatalf("CRITICAL: owner A leaked BK_B (%d) — cross-tenant read", e.bkB)
		}
	})

	// CASE 3 — Admin sees all seeded bookings (subset check; the shared DB holds
	// many more rows, so we assert membership of our three, not an exact count).
	t.Run("admin_sees_all_seeded", func(t *testing.T) {
		ids := returnedIDs(t, e.adminToken)
		if !ids[e.bkA1] || !ids[e.bkA2] || !ids[e.bkB] {
			t.Fatalf("admin must see all seeded bookings A1=%v A2=%v B=%v",
				ids[e.bkA1], ids[e.bkA2], ids[e.bkB])
		}
	})

	// CASE 4 — Unprovisioned staff: 403 at ResolveScope, before the handler — never
	// a 200 with an empty list (which would still confirm the endpoint ran for them).
	t.Run("unprovisioned_staff_forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/bookings", nil)
		req.Header.Set("Authorization", "Bearer "+e.unboundToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("unprovisioned staff = %d, want 403 at ResolveScope (body: %s)", rec.Code, rec.Body.String())
		}
	})
}
