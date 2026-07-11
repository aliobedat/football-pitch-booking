package handlers

// §5.1 — Cross-tenant staff/owner mutation isolation, exercised at the HTTP layer
// through the REAL middleware chain (RequireAuth → RequireCSRF → ResolveScope →
// RequireRole → handler) against a LIVE database. This is the highest-blast-radius
// invariant on the platform: a staff member bound to owner A's pitch must never be
// able to mutate a booking on another pitch — not owner B's, and not even another
// pitch owned by the SAME owner A that they are not bound to.
//
// Adversarial by design: every 403 case re-reads the row from the DB and asserts
// the attendance/payment fields are UNCHANGED — a "403 returned but the write
// happened" is the nightmare this suite exists to catch. ResolveScope is NOT
// stubbed; bindings are resolved from the real `staff` table.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (same gate as the other
// integration suites):
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run CrossTenantStaffMutation -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/testutil"
)

// staffTestHashH is a placeholder password_hash for the cross-tenant binding
// fixture (which does not exercise login). The column is TEXT; a non-empty value
// satisfies the onboarding "password provided" requirement.
const staffTestHashH = "$2a$10$placeholderhashvaluefortestsonlyxxxxxxxxxxxxxxxxxxxx"

// ── Test environment ──────────────────────────────────────────────────────────

type xtEnv struct {
	pool   *pgxpool.Pool
	jwt    *auth.JWTManager
	router *gin.Engine

	ownerA, ownerB int // user ids
	staffA         int // bound to pitchA1 ONLY
	staffAToken    string
	ownerAToken    string
	admin          int // platform admin (unscoped)
	adminToken     string
	unboundStaff   int // role=staff, zero bindings
	unboundToken   string

	pitchA1, pitchA2, pitchB1 int64
	bkA1, bkA2, bkB           int64 // booking ids
}

func newXTEnv(t *testing.T) *xtEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping cross-tenant staff-mutation integration test")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	jwtManager := auth.NewJWTManager("integration-test-secret-key-min-32-chars-long", 15*time.Minute, 168*time.Hour)
	e := &xtEnv{pool: pool, jwt: jwtManager}

	suffix := testutil.UniqueSuffix() % 1_000_000
	phones := map[int]string{}
	mkUser := func(name, prefix, role string) int {
		var id int
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		phones[id] = phone
		return id
	}

	e.ownerA = mkUser("XT Owner A", "60", auth.RoleOwner)
	e.ownerB = mkUser("XT Owner B", "61", auth.RoleOwner)
	player := mkUser("XT Player", "62", auth.RolePlayer)
	// staffA starts as a player; CreateStaffBindings promotes them to staff.
	e.staffA = mkUser("XT Staff A", "63", auth.RolePlayer)
	// Unprovisioned staff: role=staff directly, with NO rows in the staff table.
	e.unboundStaff = mkUser("XT Unbound Staff", "64", auth.RoleStaff)
	e.admin = mkUser("XT Admin", "65", auth.RoleAdmin)

	pitchModel := &data.PitchModel{DB: pool}
	mkPitch := func(name string, owner int) int64 {
		p, err := pitchModel.CreatePitch(ctx, data.CreatePitchRequest{
			Name: name, Neighborhood: "Amman", Surface: "artificial_grass",
			Format: "خماسي", PricePerHour: 30, OwnerID: owner,
		})
		if err != nil {
			t.Fatalf("seed pitch %s: %v", name, err)
		}
		return int64(p.ID)
	}
	e.pitchA1 = mkPitch("XT Pitch A1", e.ownerA)
	e.pitchA2 = mkPitch("XT Pitch A2", e.ownerA)
	e.pitchB1 = mkPitch("XT Pitch B1", e.ownerB)

	mkBooking := func(pitch int64) int64 {
		start := time.Now().UTC().Add(48 * time.Hour)
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
			VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), 30, 'confirmed', 'player')
			RETURNING id
		`, pitch, player, start, start.Add(time.Hour)).Scan(&id); err != nil {
			t.Fatalf("seed booking on pitch %d: %v", pitch, err)
		}
		return id
	}
	e.bkA1 = mkBooking(e.pitchA1)
	e.bkA2 = mkBooking(e.pitchA2)
	e.bkB = mkBooking(e.pitchB1)

	// Bind staffA to pitchA1 ONLY (promotes them to role=staff). pitchA2 is owned
	// by the SAME owner A but deliberately left unbound — that is case 3.
	staffRepo := repository.NewStaffRepository(pool)
	if _, err := staffRepo.CreateStaffBindings(ctx, auth.Actor{UserID: e.ownerA, Role: auth.RoleOwner}, []int{int(e.pitchA1)}, phones[e.staffA], repository.StaffProvision{PasswordHash: staffTestHashH}); err != nil {
		t.Fatalf("bind staffA to pitchA1: %v", err)
	}

	// Tokens (Bearer → CSRF-exempt, so no cookie/CSRF plumbing needed).
	mkToken := func(uid int, role string) string {
		tok, err := jwtManager.GenerateAccessToken(uid, role)
		if err != nil {
			t.Fatalf("generate token for %d: %v", uid, err)
		}
		return tok
	}
	e.staffAToken = mkToken(e.staffA, auth.RoleStaff)
	e.ownerAToken = mkToken(e.ownerA, auth.RoleOwner)
	e.adminToken = mkToken(e.admin, auth.RoleAdmin)
	e.unboundToken = mkToken(e.unboundStaff, auth.RoleStaff)

	// REAL middleware chain — identical order to routes.go's protected group.
	h := NewScheduleHandler(repository.NewScheduleRepository(pool))
	r := gin.New()
	grp := r.Group("/")
	grp.Use(middleware.RequireAuth(jwtManager))
	grp.Use(middleware.RequireCSRF())
	grp.Use(middleware.ResolveScope(staffRepo))
	grp.PATCH("/bookings/:id/attendance", middleware.RequireRole("staff", "owner", "admin"), h.PatchAttendance)
	grp.PATCH("/bookings/:id/payment", middleware.RequireRole("staff", "owner", "admin"), h.PatchPayment)
	e.router = r

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM staff WHERE pitch_id = ANY($1)`, []int64{e.pitchA1, e.pitchA2, e.pitchB1})
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, []int64{e.pitchA1, e.pitchA2, e.pitchB1})
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, []int64{e.pitchA1, e.pitchA2, e.pitchB1})
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int{e.ownerA, e.ownerB, player, e.staffA, e.unboundStaff, e.admin})
		pool.Close()
	})

	return e
}

// patch issues an authenticated PATCH and returns the recorder.
func (e *xtEnv) patch(t *testing.T, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPatch, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// state reads the attendance + payment_status of a booking; both empty if missing.
func (e *xtEnv) state(t *testing.T, bookingID int64) (attendance, payment string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.pool.QueryRow(ctx,
		`SELECT attendance, payment_status FROM bookings WHERE id = $1`, bookingID).
		Scan(&attendance, &payment)
	return
}

// ── The matrix ────────────────────────────────────────────────────────────────

// endpoint abstracts the two mutating routes so the matrix runs over both.
type endpoint struct {
	name     string
	pathFmt  string // fmt with one %d (booking id)
	bodyKey  string // JSON field name
	validVal string // a value that would succeed if in-scope
	// wantBlocked is the status an out-of-scope/nonexistent booking must yield.
	// attendance kept the original 403 contract; payment (booking-sheet
	// ApplyPayment) follows the locked 404-not-403 invariant — existence is not
	// leaked to out-of-scope actors. Case 4 (unprovisioned staff) is 403 for
	// BOTH: ResolveScope rejects before any handler runs.
	wantBlocked int
	// field selects which column to read from state() for this endpoint.
	read func(att, pay string) string
}

func TestCrossTenantStaffMutation(t *testing.T) {
	e := newXTEnv(t)

	endpoints := []endpoint{
		{
			name: "attendance", pathFmt: "/bookings/%d/attendance",
			bodyKey: "attendance", validVal: "checked_in",
			wantBlocked: http.StatusForbidden,
			read:        func(att, _ string) string { return att },
		},
		{
			name: "payment", pathFmt: "/bookings/%d/payment",
			bodyKey: "payment_status", validVal: "paid_cash",
			wantBlocked: http.StatusNotFound,
			read:        func(_, pay string) string { return pay },
		},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			body := map[string]any{ep.bodyKey: ep.validVal}

			// CASE 1 — Cross-tenant block (the core case): staffA → owner B's booking.
			t.Run("case1_cross_tenant_staff_blocked", func(t *testing.T) {
				attBefore, payBefore := e.state(t, e.bkB)
				before := ep.read(attBefore, payBefore)
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, e.bkB), e.staffAToken, body)
				if rec.Code != ep.wantBlocked {
					t.Fatalf("staffA → BK_B = %d, want %d (body: %s)", rec.Code, ep.wantBlocked, rec.Body.String())
				}
				// ADVERSARIAL: the row must be byte-for-byte unchanged after the block.
				attAfter, payAfter := e.state(t, e.bkB)
				if after := ep.read(attAfter, payAfter); after != before {
					t.Fatalf("CRITICAL: BK_B.%s mutated despite %d: %q → %q", ep.bodyKey, ep.wantBlocked, before, after)
				}
			})

			// CASE 3 — Same owner, out-of-binding pitch: staffA → owner A's pitchA2.
			// If this passes (200), staff scope is leaking to the owner's whole portfolio.
			t.Run("case3_same_owner_out_of_binding_blocked", func(t *testing.T) {
				attBefore, payBefore := e.state(t, e.bkA2)
				before := ep.read(attBefore, payBefore)
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, e.bkA2), e.staffAToken, body)
				if rec.Code != ep.wantBlocked {
					t.Fatalf("staffA → BK_A2 (same owner, unbound pitch) = %d, want %d (body: %s)", rec.Code, ep.wantBlocked, rec.Body.String())
				}
				attAfter, payAfter := e.state(t, e.bkA2)
				if after := ep.read(attAfter, payAfter); after != before {
					t.Fatalf("CRITICAL: BK_A2.%s mutated despite %d (scope leaked to owner portfolio): %q → %q", ep.bodyKey, ep.wantBlocked, before, after)
				}
			})

			// CASE 4 — Unprovisioned staff: 403 at ResolveScope, before the handler.
			t.Run("case4_unprovisioned_staff_blocked", func(t *testing.T) {
				attBefore, payBefore := e.state(t, e.bkA1)
				before := ep.read(attBefore, payBefore)
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, e.bkA1), e.unboundToken, body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("unbound staff = %d, want 403 at ResolveScope (body: %s)", rec.Code, rec.Body.String())
				}
				// Confirm it was the scope guard, not the handler, and nothing wrote.
				attAfter, payAfter := e.state(t, e.bkA1)
				if after := ep.read(attAfter, payAfter); after != before {
					t.Fatalf("CRITICAL: BK_A1.%s mutated by unprovisioned staff: %q → %q", ep.bodyKey, before, after)
				}
			})

			// CASE 5 — Nonexistent booking id: no row matches the scope predicate →
			// blocked status (403/404 per endpoint). MUST NOT 500 / leak internals.
			t.Run("case5_nonexistent_booking", func(t *testing.T) {
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, 999999), e.staffAToken, body)
				if rec.Code != ep.wantBlocked {
					t.Fatalf("nonexistent booking = %d, want %d (never 500) (body: %s)", rec.Code, ep.wantBlocked, rec.Body.String())
				}
				if bytes.Contains(rec.Body.Bytes(), []byte("SetAttendance")) ||
					bytes.Contains(rec.Body.Bytes(), []byte("SetPayment")) ||
					bytes.Contains(rec.Body.Bytes(), []byte("pgx")) {
					t.Fatalf("response leaks internals: %s", rec.Body.String())
				}
			})

			// CASE 6 — Owner cross-tenant (defense in depth): ownerA → owner B's booking.
			t.Run("case6_owner_cross_tenant_blocked", func(t *testing.T) {
				attBefore, payBefore := e.state(t, e.bkB)
				before := ep.read(attBefore, payBefore)
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, e.bkB), e.ownerAToken, body)
				if rec.Code != ep.wantBlocked {
					t.Fatalf("ownerA → BK_B = %d, want %d (body: %s)", rec.Code, ep.wantBlocked, rec.Body.String())
				}
				attAfter, payAfter := e.state(t, e.bkB)
				if after := ep.read(attAfter, payAfter); after != before {
					t.Fatalf("CRITICAL: BK_B.%s mutated by cross-tenant owner: %q → %q", ep.bodyKey, before, after)
				}
			})

			// CASE 2 — In-scope ALLOW (positive control): staffA → their bound pitchA1.
			// Run LAST so the successful mutation can't mask a false-negative elsewhere.
			t.Run("case2_in_scope_allowed", func(t *testing.T) {
				rec := e.patch(t, fmt.Sprintf(ep.pathFmt, e.bkA1), e.staffAToken, body)
				if rec.Code != http.StatusOK {
					t.Fatalf("staffA → BK_A1 (bound) = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
				}
				attAfter, payAfter := e.state(t, e.bkA1)
				if after := ep.read(attAfter, payAfter); after != ep.validVal {
					t.Fatalf("BK_A1.%s = %q after 200, want %q (positive control failed)", ep.bodyKey, after, ep.validVal)
				}
			})
		})
	}
}
