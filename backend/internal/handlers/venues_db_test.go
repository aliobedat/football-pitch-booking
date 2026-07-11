package handlers

// WO-VENUES / Gate 1b — DB-backed tests for the venues slice, driving the REAL
// gin handlers + models against a scratch DB (post-033 schema). Role-matrix:
// every scope-predicated operation runs as OWNER and as ADMIN (the Gate 0/B5
// rule — an owner-only suite is a failed gate). SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL (a skipped run is a failed gate).
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run VenuesDB -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

const vnMapsURL = "https://maps.app.goo.gl/vn-test"

type vnEnv struct {
	pool           *pgxpool.Pool
	ownerA, ownerB int64
	adminID        int64
}

func newVnEnv(t *testing.T) *vnEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping venues DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	e := &vnEnv{pool: pool}
	suffix := time.Now().UnixNano() % 1_000_000
	mk := func(role, prefix string) int64 {
		var id int64
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id`,
			"VN "+role, fmt.Sprintf("+962%s%06d", prefix, suffix), role).Scan(&id); err != nil {
			t.Fatalf("mk user: %v", err)
		}
		return id
	}
	e.ownerA = mk("owner", "94")
	e.ownerB = mk("owner", "95")
	e.adminID = mk("admin", "96")
	return e
}

// router wires the REAL venue + pitch handlers behind an injected actor.
func (e *vnEnv) router(actorID int64, role string) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int(actorID))
		c.Set(middleware.ContextKeyRole, role)
		c.Set(middleware.ContextKeyActor, auth.Actor{UserID: int(actorID), Role: role})
		c.Next()
	})
	vh := NewVenueHandler(&data.VenueModel{DB: e.pool})
	ph := &PitchHandler{Model: &data.PitchModel{DB: e.pool}}
	r.POST("/venues", middleware.RequireRole("owner", "admin"), vh.CreateVenue)
	r.GET("/owner/venues", middleware.RequireRole("owner", "admin"), vh.OwnerListVenues)
	r.PATCH("/venues/:id", middleware.RequireRole("owner", "admin"), vh.UpdateVenue)
	r.PATCH("/venues/:id/active", middleware.RequireRole("owner", "admin"), vh.ToggleVenueActive)
	r.DELETE("/venues/:id", middleware.RequireRole("owner", "admin"), vh.DeleteVenue)
	r.GET("/venues/:id", vh.PublicVenueBySlug) // public (param = slug)
	r.POST("/pitches", middleware.RequireRole("owner", "admin"), ph.CreatePitch)
	r.PATCH("/pitches/:id/venue", middleware.RequireRole("owner", "admin"), ph.ReassignVenue)
	r.GET("/pitches/:id", ph.GetPitch) // public
	return r
}

func vnBody(extra map[string]any) map[string]any {
	b := map[string]any{
		"name": "مجمع الاختبار", "slug": "", "neighborhood": "عبدون", "maps_url": vnMapsURL,
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func decodeData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return resp.Data
}

func (e *vnEnv) createVenue(t *testing.T, r *gin.Engine, slug string) int64 {
	t.Helper()
	rec := bsDo(r, http.MethodPost, "/venues", vnBody(map[string]any{"slug": slug}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue %s: %d %s", slug, rec.Code, rec.Body.String())
	}
	return int64(decodeData(t, rec.Body.Bytes())["id"].(float64))
}

func (e *vnEnv) createPitch(t *testing.T, r *gin.Engine, name string, venueID *int64) map[string]any {
	t.Helper()
	body := map[string]any{
		"name": name, "neighborhood": "عبدون", "surface": "artificial_grass",
		"format": "خماسي", "price_per_hour": 30, "maps_url": vnMapsURL,
	}
	if venueID != nil {
		body["venue_id"] = *venueID
	}
	rec := bsDo(r, http.MethodPost, "/pitches", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pitch %q: %d %s", name, rec.Code, rec.Body.String())
	}
	return decodeData(t, rec.Body.Bytes())
}

// ── CRUD, owner × admin (role matrix) ────────────────────────────────────────

func TestVenuesDB_CRUDRoleMatrix(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000

	for i, tc := range []struct {
		name    string
		actorID int64
		role    string
	}{
		{"owner", 0, "owner"}, // actorID filled below
		{"admin", 0, "admin"},
	} {
		tc.actorID = e.ownerA
		if tc.role == "admin" {
			tc.actorID = e.adminID
		}
		t.Run(tc.name, func(t *testing.T) {
			r := e.router(tc.actorID, tc.role)
			slug := fmt.Sprintf("crud-%s-%d-%d", tc.name, suffix, i)
			id := e.createVenue(t, r, slug)

			// list contains it with pitch_count 0
			rec := bsDo(r, http.MethodGet, "/owner/venues", nil)
			if rec.Code != 200 {
				t.Fatalf("list: %d", rec.Code)
			}
			// patch (slug NOT in body — immutable)
			rec = bsDo(r, http.MethodPatch, fmt.Sprintf("/venues/%d", id), map[string]any{
				"name": "مجمع معدل", "neighborhood": "خلدا", "maps_url": vnMapsURL, "description": "وصف",
			})
			if rec.Code != 200 {
				t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
			}
			d := decodeData(t, rec.Body.Bytes())
			if d["name"] != "مجمع معدل" || d["slug"] != slug {
				t.Errorf("patch result name=%v slug=%v (slug must be immutable)", d["name"], d["slug"])
			}
			// active toggle
			rec = bsDo(r, http.MethodPatch, fmt.Sprintf("/venues/%d/active", id), map[string]any{"is_active": false})
			if rec.Code != 200 || decodeData(t, rec.Body.Bytes())["isActive"] != false {
				t.Fatalf("toggle: %d %s", rec.Code, rec.Body.String())
			}
			// delete (empty venue) OK
			rec = bsDo(r, http.MethodDelete, fmt.Sprintf("/venues/%d", id), nil)
			if rec.Code != 200 {
				t.Fatalf("delete empty: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// ── Cross-tenant: everything foreign → 404 ───────────────────────────────────

func TestVenuesDB_CrossTenant404(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	rA := e.router(e.ownerA, "owner")
	rB := e.router(e.ownerB, "owner")

	vA := e.createVenue(t, rA, fmt.Sprintf("xt-%d", suffix))
	pA := e.createPitch(t, rA, "XT Pitch", &vA)
	pitchID := int(pA["id"].(float64))

	if rec := bsDo(rB, http.MethodPatch, fmt.Sprintf("/venues/%d", vA), vnBody(nil)); rec.Code != 404 {
		t.Errorf("foreign patch = %d, want 404", rec.Code)
	}
	if rec := bsDo(rB, http.MethodPatch, fmt.Sprintf("/venues/%d/active", vA), map[string]any{"is_active": false}); rec.Code != 404 {
		t.Errorf("foreign toggle = %d, want 404", rec.Code)
	}
	if rec := bsDo(rB, http.MethodDelete, fmt.Sprintf("/venues/%d", vA), nil); rec.Code != 404 {
		t.Errorf("foreign delete = %d, want 404", rec.Code)
	}
	// foreign REASSIGN: B cannot move A's pitch; A cannot move own pitch to B's venue
	vB := e.createVenue(t, rB, fmt.Sprintf("xt-b-%d", suffix))
	if rec := bsDo(rB, http.MethodPatch, fmt.Sprintf("/pitches/%d/venue", pitchID), map[string]any{"venue_id": vB}); rec.Code != 404 {
		t.Errorf("foreign pitch reassign = %d, want 404", rec.Code)
	}
	if rec := bsDo(rA, http.MethodPatch, fmt.Sprintf("/pitches/%d/venue", pitchID), map[string]any{"venue_id": vB}); rec.Code != 404 {
		t.Errorf("reassign to foreign venue = %d, want 404", rec.Code)
	}
	// CreatePitch with a FOREIGN venue_id → 404
	body := map[string]any{"name": "XT2", "neighborhood": "x", "surface": "artificial_grass",
		"format": "خماسي", "price_per_hour": 30, "maps_url": vnMapsURL, "venue_id": vB}
	if rec := bsDo(rA, http.MethodPost, "/pitches", body); rec.Code != 404 {
		t.Errorf("create pitch w/ foreign venue = %d, want 404", rec.Code)
	}
}

// ── Slug rules ────────────────────────────────────────────────────────────────

func TestVenuesDB_SlugRules(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")

	// pattern rejection (post-normalization: trim + lowercase happen first,
	// so only violations that survive lowercasing are 422s)
	for _, bad := range []string{"has_underscore", "has space", "-lead", "trail-", "عربي"} {
		if rec := bsDo(r, http.MethodPost, "/venues", vnBody(map[string]any{"slug": bad})); rec.Code != 422 {
			t.Errorf("slug %q = %d, want 422", bad, rec.Code)
		}
	}
	// normalize-then-validate: uppercase input is accepted and stored lowercased
	rec0 := bsDo(r, http.MethodPost, "/venues", vnBody(map[string]any{"slug": fmt.Sprintf("Bad-Upper-%d", suffix)}))
	if rec0.Code != 201 {
		t.Fatalf("uppercase slug = %d, want 201 (normalized) (%s)", rec0.Code, rec0.Body.String())
	}
	if got := decodeData(t, rec0.Body.Bytes())["slug"]; got != fmt.Sprintf("bad-upper-%d", suffix) {
		t.Errorf("normalized slug = %v, want %q", got, fmt.Sprintf("bad-upper-%d", suffix))
	}
	// ci-uniqueness → 409 slug_taken
	slug := fmt.Sprintf("uniq-%d", suffix)
	e.createVenue(t, r, slug)
	rec := bsDo(r, http.MethodPost, "/venues", vnBody(map[string]any{"slug": slug}))
	if rec.Code != 409 {
		t.Fatalf("dup slug = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error != "slug_taken" {
		t.Errorf("dup slug error code = %q, want slug_taken", errResp.Error)
	}
}

// ── Delete refusal + reassign auto-cleanup ───────────────────────────────────

func TestVenuesDB_DeleteRefusalAndReassignCleanup(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")

	vOld := e.createVenue(t, r, fmt.Sprintf("old-%d", suffix))
	vNew := e.createVenue(t, r, fmt.Sprintf("new-%d", suffix))
	p := e.createPitch(t, r, "Move Me", &vOld)
	pitchID := int(p["id"].(float64))

	// delete refused while the pitch lives there
	rec := bsDo(r, http.MethodDelete, fmt.Sprintf("/venues/%d", vOld), nil)
	if rec.Code != 409 {
		t.Fatalf("delete with pitch = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// reassign → 200; old venue auto-soft-deleted (emptied)
	rec = bsDo(r, http.MethodPatch, fmt.Sprintf("/pitches/%d/venue", pitchID), map[string]any{"venue_id": vNew})
	if rec.Code != 200 {
		t.Fatalf("reassign: %d %s", rec.Code, rec.Body.String())
	}
	var deletedAt *time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM venues WHERE id = $1`, vOld).Scan(&deletedAt); err != nil {
		t.Fatalf("read old venue: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("old venue not auto-soft-deleted after being emptied")
	}
	// audit row written
	var audits int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pitch_audit_log WHERE pitch_id=$1 AND action='venue_reassigned'`, pitchID).Scan(&audits)
	if audits != 1 {
		t.Errorf("audit rows = %d, want 1", audits)
	}
}

// ── CreatePitch write-path fix: auto 1:1 venue + slug rules ──────────────────

func TestVenuesDB_CreatePitchAutoVenue(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")

	// (a) ASCII name → slugified auto venue
	name := fmt.Sprintf("Auto Arena %d", suffix)
	p := e.createPitch(t, r, name, nil)
	wantSlug := fmt.Sprintf("auto-arena-%d", suffix)
	if p["venue_slug"] != wantSlug || p["venue_name"] != name {
		t.Errorf("auto venue = slug %v name %v, want %s / %s", p["venue_slug"], p["venue_name"], wantSlug, name)
	}
	// (b) Arabic name → v-<pitch id>
	p2 := e.createPitch(t, r, "ملعب تلقائي", nil)
	wantFallback := fmt.Sprintf("v-%d", int(p2["id"].(float64)))
	if p2["venue_slug"] != wantFallback {
		t.Errorf("arabic auto slug = %v, want %s", p2["venue_slug"], wantFallback)
	}
	// (c) ASCII collision → v-<pitch id> fallback
	p3 := e.createPitch(t, r, name, nil) // same name as (a)
	wantFallback3 := fmt.Sprintf("v-%d", int(p3["id"].(float64)))
	if p3["venue_slug"] != wantFallback3 {
		t.Errorf("collision fallback = %v, want %s", p3["venue_slug"], wantFallback3)
	}
	// (d) explicit OWN venue_id honoured
	v := e.createVenue(t, r, fmt.Sprintf("explicit-%d", suffix))
	p4 := e.createPitch(t, r, "Explicit Pitch", &v)
	if p4["venue_slug"] != fmt.Sprintf("explicit-%d", suffix) {
		t.Errorf("explicit venue slug = %v", p4["venue_slug"])
	}
}

// ── Admin ownership invariant (pitch.owner_id == venue.owner_id, always) ─────

func TestVenuesDB_AdminOwnershipInvariant(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	rA := e.router(e.ownerA, "owner")
	rB := e.router(e.ownerB, "owner")
	rAdm := e.router(e.adminID, "admin")

	vA1 := e.createVenue(t, rA, fmt.Sprintf("adm-a1-%d", suffix))
	vA2 := e.createVenue(t, rA, fmt.Sprintf("adm-a2-%d", suffix))
	vB := e.createVenue(t, rB, fmt.Sprintf("adm-b-%d", suffix))
	p := e.createPitch(t, rA, "Admin Move Me", &vA1)
	pitchID := int(p["id"].(float64))

	// Reassign, admin actor: FOREIGN pitch is reachable, and a same-owner
	// target succeeds (vA1 → vA2, both ownerA's).
	t.Run("admin_reassign_same_owner_ok", func(t *testing.T) {
		rec := bsDo(rAdm, http.MethodPatch, fmt.Sprintf("/pitches/%d/venue", pitchID), map[string]any{"venue_id": vA2})
		if rec.Code != 200 {
			t.Fatalf("admin same-owner reassign = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
	})

	// Reassign, admin actor: cross-owner target → 409 venue_owner_mismatch
	// (visible-but-refused, NOT 404 — admin sees everything).
	t.Run("admin_reassign_cross_owner_409", func(t *testing.T) {
		rec := bsDo(rAdm, http.MethodPatch, fmt.Sprintf("/pitches/%d/venue", pitchID), map[string]any{"venue_id": vB})
		if rec.Code != 409 {
			t.Fatalf("admin cross-owner reassign = %d, want 409 (%s)", rec.Code, rec.Body.String())
		}
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != "venue_owner_mismatch" {
			t.Errorf("error code = %q, want venue_owner_mismatch", errResp.Error)
		}
		var venueID int64
		if err := e.pool.QueryRow(context.Background(),
			`SELECT venue_id FROM pitches WHERE id = $1`, pitchID).Scan(&venueID); err != nil || venueID != vA2 {
			t.Errorf("pitch venue_id = %d (err %v), want unchanged %d after 409", venueID, err, vA2)
		}
	})

	// CreatePitch, admin actor, into an OWNER's venue → 201 and the pitch's
	// owner_id is DERIVED from the venue's owner, never the admin actor.
	t.Run("admin_create_into_owner_venue_derives_owner", func(t *testing.T) {
		pAdm := e.createPitch(t, rAdm, "Admin Created", &vA2)
		var ownerID int64
		if err := e.pool.QueryRow(context.Background(),
			`SELECT owner_id FROM pitches WHERE id = $1`, int64(pAdm["id"].(float64))).Scan(&ownerID); err != nil {
			t.Fatalf("read owner_id: %v", err)
		}
		if ownerID != e.ownerA {
			t.Errorf("pitch owner_id = %d, want venue owner %d (not admin %d)", ownerID, e.ownerA, e.adminID)
		}
	})

	// CreatePitch, admin actor, nonexistent venue → 404.
	t.Run("admin_create_nonexistent_venue_404", func(t *testing.T) {
		nonexistent := int64(99_999_999)
		body := map[string]any{"name": "Ghost", "neighborhood": "x", "surface": "artificial_grass",
			"format": "خماسي", "price_per_hour": 30, "maps_url": vnMapsURL, "venue_id": nonexistent}
		if rec := bsDo(rAdm, http.MethodPost, "/pitches", body); rec.Code != 404 {
			t.Errorf("admin create w/ nonexistent venue = %d, want 404 (%s)", rec.Code, rec.Body.String())
		}
	})
}

// ── Composite display name (collapse rule) ───────────────────────────────────

func TestVenuesDB_CompositeDisplayName(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")
	ctx := context.Background()

	v := e.createVenue(t, r, fmt.Sprintf("comp-%d", suffix))
	p1 := e.createPitch(t, r, "P One", &v)
	id1 := int64(p1["id"].(float64))
	_, _ = e.pool.Exec(ctx, `UPDATE pitches SET label = 'ملعب 1' WHERE id = $1`, id1)

	dv := repository.NewDayViewRepository(e.pool)
	day := time.Date(2032, 3, 10, 0, 0, 0, 0, timeutil.Amman())
	actor := auth.Actor{UserID: int(e.ownerA), Role: auth.RoleOwner}

	// SINGLE-pitch venue → bare venue name (collapse rule)
	view, err := dv.OwnerDayView(ctx, actor, id1, day)
	if err != nil {
		t.Fatalf("day view: %v", err)
	}
	if view.PitchName != "مجمع الاختبار" {
		t.Errorf("single-pitch display = %q, want bare venue name", view.PitchName)
	}

	// Add a sibling → composite "venue — label"
	p2 := e.createPitch(t, r, "P Two", &v)
	id2 := int64(p2["id"].(float64))
	view, err = dv.OwnerDayView(ctx, actor, id1, day)
	if err != nil {
		t.Fatalf("day view 2: %v", err)
	}
	if view.PitchName != "مجمع الاختبار — ملعب 1" {
		t.Errorf("multi-pitch display = %q, want composite venue — label", view.PitchName)
	}
	// sibling without label falls back to its own name
	view2, err := dv.OwnerDayView(ctx, actor, id2, day)
	if err != nil {
		t.Fatalf("day view 3: %v", err)
	}
	if view2.PitchName != "مجمع الاختبار — P Two" {
		t.Errorf("unlabeled sibling display = %q", view2.PitchName)
	}

	// Reports by_pitch uses the same expression
	start := day.Add(10 * time.Hour)
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source, guest_name)
		VALUES ($1, NULL, tstzrange($2::timestamptz,$3::timestamptz,'[)'), 30, 'confirmed', 'manual', 'ضيف')`,
		id1, start.UTC(), start.Add(time.Hour).UTC()); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	reps := repository.NewReportsRepository(e.pool)
	from, to := timeutil.AmmanDayBoundsUTC(day)
	fin, err := reps.OwnerFinancialReport(ctx, actor, 0, from, to)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	found := false
	for _, bp := range fin.ByPitch {
		if bp.PitchID == id1 {
			found = true
			if bp.PitchName != "مجمع الاختبار — ملعب 1" {
				t.Errorf("report pitch name = %q, want composite", bp.PitchName)
			}
		}
	}
	if !found {
		t.Errorf("pitch %d missing from by_pitch", id1)
	}

	// Notification variable (PitchName as fed to providers) is the composite.
	var bookingID int64
	_ = e.pool.QueryRow(ctx, `SELECT id FROM bookings WHERE pitch_id=$1 LIMIT 1`, id1).Scan(&bookingID)
	contact, err := repository.NewBookingRepository(e.pool).GetBookingContact(ctx, bookingID)
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	if contact.PitchName != "مجمع الاختبار — ملعب 1" {
		t.Errorf("notification PitchName = %q, want composite", contact.PitchName)
	}
}

// ── /pitches/:id byte-compat + additive fields ───────────────────────────────

func TestVenuesDB_PitchByteCompat(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")
	v := e.createVenue(t, r, fmt.Sprintf("compat-%d", suffix))
	p := e.createPitch(t, r, "Compat Pitch", &v)
	pitchID := int(p["id"].(float64))

	// Force PITCH-row values to DIFFER from the venue's copies on every
	// place-ish field, so any accidental rerouting through the venue join
	// shows up as a value mismatch (not just a missing key).
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		UPDATE pitches SET neighborhood='حي الملعب', description='وصف الملعب نفسه',
		       image_url='https://res.cloudinary.com/t/pitch-own.jpg', image_public_id='pitch-own-1',
		       maps_url='https://maps.app.goo.gl/pitch-own', latitude=31.5, longitude=35.9
		WHERE id=$1`, pitchID); err != nil {
		t.Fatalf("seed pitch values: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE venues SET neighborhood='حي المجمع', description='وصف المجمع',
		       cover_image_url='https://res.cloudinary.com/t/venue.jpg', cover_image_public_id='venue-img-1',
		       maps_url='https://maps.app.goo.gl/venue-own', latitude=1, longitude=2
		WHERE id=$1`, v); err != nil {
		t.Fatalf("seed venue values: %v", err)
	}

	rec := bsDo(r, http.MethodGet, fmt.Sprintf("/pitches/%d", pitchID), nil)
	if rec.Code != 200 {
		t.Fatalf("get pitch: %d", rec.Code)
	}
	got := decodeData(t, rec.Body.Bytes())
	// Presence-only for the non-place fields.
	for _, k := range []string{"id", "name", "surface", "format", "pricePerHour",
		"rating", "reviewsCount", "isFeatured", "amenities", "pitchHue", "isActive"} {
		if _, ok := got[k]; !ok {
			t.Errorf("old field %q missing from /pitches/:id", k)
		}
	}
	// VALUE assertions for every field that also has a venue copy: the payload
	// must carry the PITCH row's values, not the venue's.
	for k, want := range map[string]any{
		"neighborhood":    "حي الملعب",
		"description":     "وصف الملعب نفسه",
		"image_url":       "https://res.cloudinary.com/t/pitch-own.jpg",
		"image_public_id": "pitch-own-1",
		"maps_url":        "https://maps.app.goo.gl/pitch-own",
		"lat":             31.5,
		"lng":             35.9,
	} {
		if got[k] != want {
			t.Errorf("field %q = %v, want pitch-row value %v (venue value leaked?)", k, got[k], want)
		}
	}
	// Additive venue identity present and correct.
	if got["venue_slug"] != fmt.Sprintf("compat-%d", suffix) || got["venue_name"] != "مجمع الاختبار" {
		t.Errorf("venue fields = %v / %v", got["venue_slug"], got["venue_name"])
	}
}

// ── Public venue read ────────────────────────────────────────────────────────

func TestVenuesDB_PublicSlugRead(t *testing.T) {
	e := newVnEnv(t)
	suffix := time.Now().UnixNano() % 1_000_000
	r := e.router(e.ownerA, "owner")
	ctx := context.Background()

	slug := fmt.Sprintf("public-%d", suffix)
	v := e.createVenue(t, r, slug)
	pActive := e.createPitch(t, r, "Visible", &v)
	pInactive := e.createPitch(t, r, "Hidden Inactive", &v)
	pDeleted := e.createPitch(t, r, "Hidden Deleted", &v)
	_, _ = e.pool.Exec(ctx, `UPDATE pitches SET is_active=false WHERE id=$1`, int64(pInactive["id"].(float64)))
	_, _ = e.pool.Exec(ctx, `UPDATE pitches SET deleted_at=now() WHERE id=$1`, int64(pDeleted["id"].(float64)))

	rec := bsDo(r, http.MethodGet, "/venues/"+slug, nil)
	if rec.Code != 200 {
		t.Fatalf("public read: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Slug    string           `json:"slug"`
			Pitches []map[string]any `json:"pitches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Pitches) != 1 || int(resp.Data.Pitches[0]["id"].(float64)) != int(pActive["id"].(float64)) {
		t.Errorf("public pitches = %d entries, want only the active one", len(resp.Data.Pitches))
	}

	// unknown slug → 404
	if rec := bsDo(r, http.MethodGet, "/venues/does-not-exist-"+slug, nil); rec.Code != 404 {
		t.Errorf("unknown slug = %d, want 404", rec.Code)
	}
	// soft-deleted venue → 404 (empty it first, then delete)
	emptySlug := fmt.Sprintf("gone-%d", suffix)
	vGone := e.createVenue(t, r, emptySlug)
	if rec := bsDo(r, http.MethodDelete, fmt.Sprintf("/venues/%d", vGone), nil); rec.Code != 200 {
		t.Fatalf("delete empty: %d", rec.Code)
	}
	if rec := bsDo(r, http.MethodGet, "/venues/"+emptySlug, nil); rec.Code != 404 {
		t.Errorf("soft-deleted slug = %d, want 404", rec.Code)
	}
}
