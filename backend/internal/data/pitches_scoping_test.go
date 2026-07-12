package data

// Integration tests for Admin-vs-Owner pitch scoping and the soft-delete flow.
// They exercise the REAL SQL (ownership predicates, deleted_at filtering, the
// future-booking guard, and the audit insert) against a live database, and are
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set — so the default
// `go test ./...` run (and CI without a database) stays green.
//
// To run: point the env var at a database with migrations 002–008 applied:
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/data/ -run Scoping

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/testutil"
)

type scopingEnv struct {
	pool               *pgxpool.Pool
	model              *PitchModel
	adminID            int
	ownerAID, ownerBID int
	playerID           int
	pitchA, pitchB     int // pitchA owned by ownerA, pitchB by ownerB
}

func newScopingEnv(t *testing.T) *scopingEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping pitch scoping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	testutil.AssertSchemaBaseline(t, pool)

	suffix := testutil.UniqueSuffix() % 1_000_000
	model := &PitchModel{DB: pool}

	mkUser := func(name, prefix, role string) int {
		var id int
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1, $2, $3, TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}

	env := &scopingEnv{pool: pool, model: model}
	env.adminID = mkUser("Scoping Admin", "70", auth.RoleAdmin)
	env.ownerAID = mkUser("Scoping Owner A", "71", auth.RoleOwner)
	env.ownerBID = mkUser("Scoping Owner B", "72", auth.RoleOwner)
	env.playerID = mkUser("Scoping Player", "73", auth.RolePlayer)

	mkPitch := func(name string, ownerID int) int {
		p, err := model.CreatePitch(ctx, CreatePitchRequest{
			Name: name, Neighborhood: "Khalda", Surface: "artificial_grass",
			Format: "خماسي", PricePerHour: 30, OwnerID: ownerID,
			// Give a valid maps_url so update-path tests satisfy the PR 4.2 mandatory-
			// location gate (these tests exercise scoping, not location).
			MapsURL: "https://maps.app.goo.gl/scopingSeed",
		})
		if err != nil {
			pool.Close()
			t.Fatalf("seed pitch %s: %v", name, err)
		}
		return p.ID
	}
	env.pitchA = mkPitch("Pitch A", env.ownerAID)
	env.pitchB = mkPitch("Pitch B", env.ownerBID)

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		ids := []int{env.pitchA, env.pitchB}
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = ANY($1)`, ids)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, ids)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`,
			[]int{env.adminID, env.ownerAID, env.ownerBID, env.playerID})
		pool.Close()
	})
	return env
}

// seedBooking inserts a confirmed booking on the given pitch, ending at now+offset.
func (e *scopingEnv) seedBooking(t *testing.T, pitchID int, endOffset time.Duration) {
	t.Helper()
	end := time.Now().UTC().Add(endOffset)
	start := end.Add(-time.Hour)
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), 30, 'confirmed', 'player')
	`, pitchID, e.playerID, start, end); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
}

func (e *scopingEnv) ownerA() auth.Actor { return auth.Actor{UserID: e.ownerAID, Role: auth.RoleOwner} }
func (e *scopingEnv) admin() auth.Actor  { return auth.Actor{UserID: e.adminID, Role: auth.RoleAdmin} }

// ── owner cannot read another owner's pitch list ─────────────────────────────

func TestScoping_ListForActor_OwnerSeesOnlyOwn(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	aList, err := e.model.ListForActor(ctx, e.ownerA())
	if err != nil {
		t.Fatalf("ListForActor owner A: %v", err)
	}
	for _, p := range aList {
		if p.ID == e.pitchB {
			t.Fatalf("owner A must not see owner B's pitch %d", e.pitchB)
		}
	}
	if !containsPitch(aList, e.pitchA) {
		t.Fatalf("owner A must see their own pitch %d", e.pitchA)
	}

	adminList, err := e.model.ListForActor(ctx, e.admin())
	if err != nil {
		t.Fatalf("ListForActor admin: %v", err)
	}
	if !containsPitch(adminList, e.pitchA) || !containsPitch(adminList, e.pitchB) {
		t.Fatalf("admin must see all pitches (A and B)")
	}
}

// ── owner cannot edit another owner's pitch → ErrNoRows (→404) ───────────────

func TestScoping_Update_ForeignOwnerGets404(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	// Owner A tries to edit Owner B's pitch.
	_, err := e.model.UpdatePitch(ctx, e.pitchB, e.ownerA(), UpdatePitchRequest{Name: "Hijacked"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for foreign update, got %v", err)
	}

	// Admin can edit any pitch.
	if _, err := e.model.UpdatePitch(ctx, e.pitchB, e.admin(), UpdatePitchRequest{Name: "Admin Renamed"}); err != nil {
		t.Fatalf("admin update should succeed, got %v", err)
	}
}

// ── owner cannot delete another owner's pitch → ErrNoRows (→404) ─────────────

func TestScoping_Delete_ForeignOwnerGets404(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	_, err := e.model.SoftDeletePitch(ctx, e.pitchB, e.ownerA())
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for foreign delete, got %v", err)
	}
	// Pitch B must be untouched.
	if _, err := e.model.GetByID(ctx, e.pitchB); err != nil {
		t.Fatalf("pitch B should still be live, got %v", err)
	}
}

// ── delete blocked by future confirmed booking → 409 with count, no change ───

func TestScoping_Delete_FutureBookingBlocks(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	e.seedBooking(t, e.pitchA, 2*time.Hour) // ends in the future → blocks

	count, err := e.model.SoftDeletePitch(ctx, e.pitchA, e.ownerA())
	if !errors.Is(err, ErrPitchHasFutureBookings) {
		t.Fatalf("expected ErrPitchHasFutureBookings, got %v", err)
	}
	if count != 1 {
		t.Fatalf("expected blocking count 1, got %d", count)
	}
	// Nothing modified: pitch still live.
	if _, err := e.model.GetByID(ctx, e.pitchA); err != nil {
		t.Fatalf("pitch A must remain live after blocked delete, got %v", err)
	}
}

// ── delete with no future booking → soft-deleted, excluded, history + audit ──

func TestScoping_Delete_SoftDeletesAndAudits(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	// A past booking (ended an hour ago) must NOT block deletion, and must
	// survive it (history preserved).
	e.seedBooking(t, e.pitchA, -1*time.Hour)

	count, err := e.model.SoftDeletePitch(ctx, e.pitchA, e.ownerA())
	if err != nil {
		t.Fatalf("delete should succeed, got %v (count=%d)", err, count)
	}

	// Excluded from single-read.
	if _, err := e.model.GetByID(ctx, e.pitchA); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("soft-deleted pitch must be excluded from GetByID, got %v", err)
	}
	// Excluded from owner listing.
	list, _ := e.model.ListForActor(ctx, e.ownerA())
	if containsPitch(list, e.pitchA) {
		t.Fatalf("soft-deleted pitch must be excluded from ListForActor")
	}
	// Excluded from the public GetAll listing.
	all, _ := e.model.GetAll(ctx, PitchFilters{})
	if containsPitch(all, e.pitchA) {
		t.Fatalf("soft-deleted pitch must be excluded from public GetAll")
	}

	// Booking history row intact.
	var bookingRows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE pitch_id = $1`, e.pitchA).Scan(&bookingRows); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	if bookingRows != 1 {
		t.Fatalf("booking history must be preserved, found %d rows", bookingRows)
	}

	// Audit entry written.
	var auditRows int
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM pitch_audit_log WHERE pitch_id = $1 AND action = 'deleted' AND actor_id = $2
	`, e.pitchA, e.ownerAID).Scan(&auditRows); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditRows != 1 {
		t.Fatalf("expected 1 audit entry, found %d", auditRows)
	}
}

// ── is_active toggle: scoping, idempotency, audit ────────────────────────────

func TestActive_ToggleScopingAndIdempotency(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	auditCount := func(pitchID int) int {
		var n int
		if err := e.pool.QueryRow(ctx,
			`SELECT count(*) FROM pitch_audit_log WHERE pitch_id = $1 AND action IN ('activated','deactivated')`,
			pitchID).Scan(&n); err != nil {
			t.Fatalf("audit count: %v", err)
		}
		return n
	}
	isActive := func(pitchID int) bool {
		var a bool
		if err := e.pool.QueryRow(ctx, `SELECT is_active FROM pitches WHERE id = $1`, pitchID).Scan(&a); err != nil {
			t.Fatalf("read is_active: %v", err)
		}
		return a
	}

	// Foreign owner → 404, no change, no audit.
	if err := e.model.SetPitchActive(ctx, e.pitchB, e.ownerA(), false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("owner A deactivating owner B's pitch: err = %v, want pgx.ErrNoRows (→404)", err)
	}
	if !isActive(e.pitchB) || auditCount(e.pitchB) != 0 {
		t.Fatalf("owner B's pitch must be unchanged with no audit after a foreign toggle")
	}

	// Owner deactivates own pitch → persists + one audit row.
	if err := e.model.SetPitchActive(ctx, e.pitchA, e.ownerA(), false); err != nil {
		t.Fatalf("owner deactivate own pitch: %v", err)
	}
	if isActive(e.pitchA) {
		t.Fatalf("pitch A should be inactive")
	}
	if auditCount(e.pitchA) != 1 {
		t.Fatalf("expected 1 audit row after toggle, got %d", auditCount(e.pitchA))
	}

	// Idempotent: setting the same value again → success, NO duplicate audit.
	if err := e.model.SetPitchActive(ctx, e.pitchA, e.ownerA(), false); err != nil {
		t.Fatalf("idempotent toggle should succeed: %v", err)
	}
	if auditCount(e.pitchA) != 1 {
		t.Fatalf("idempotent no-op must not add an audit row, got %d", auditCount(e.pitchA))
	}

	// Admin can reactivate any pitch → audit attributes the admin.
	if err := e.model.SetPitchActive(ctx, e.pitchA, e.admin(), true); err != nil {
		t.Fatalf("admin reactivate: %v", err)
	}
	if !isActive(e.pitchA) {
		t.Fatalf("pitch A should be active again")
	}
	var lastActor int
	var lastRole, lastAction string
	if err := e.pool.QueryRow(ctx, `
		SELECT actor_id, actor_role, action FROM pitch_audit_log
		WHERE pitch_id = $1 ORDER BY id DESC LIMIT 1
	`, e.pitchA).Scan(&lastActor, &lastRole, &lastAction); err != nil {
		t.Fatalf("read last audit: %v", err)
	}
	if lastActor != e.adminID || lastRole != auth.RoleAdmin || lastAction != "activated" {
		t.Fatalf("audit = (actor %d, role %q, action %q), want (admin %d, admin, activated)",
			lastActor, lastRole, lastAction, e.adminID)
	}
}

// ── soft-deleted pitch cannot be toggled ─────────────────────────────────────

func TestActive_ToggleSoftDeletedGets404(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	if _, err := e.model.SoftDeletePitch(ctx, e.pitchA, e.ownerA()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := e.model.SetPitchActive(ctx, e.pitchA, e.ownerA(), false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("toggling a soft-deleted pitch: err = %v, want pgx.ErrNoRows (→404)", err)
	}
}

// ── visibility: inactive excluded from player surfaces, kept for owner/admin ──

func TestActive_ListingVisibility(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	// A confirmed booking on pitch A must survive deactivation untouched.
	e.seedBooking(t, e.pitchA, 48*time.Hour)

	if err := e.model.SetPitchActive(ctx, e.pitchA, e.ownerA(), false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Player-facing: excluded from public listing and single-read.
	all, _ := e.model.GetAll(ctx, PitchFilters{})
	if containsPitch(all, e.pitchA) {
		t.Fatalf("deactivated pitch must be excluded from public GetAll listing")
	}
	if _, err := e.model.GetByID(ctx, e.pitchA); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deactivated pitch must be excluded from public GetByID, got %v", err)
	}

	// Owner/admin: STILL visible (so the badge + toggle render), with isActive=false.
	ownerList, _ := e.model.ListForActor(ctx, e.ownerA())
	var found *Pitch
	for i := range ownerList {
		if ownerList[i].ID == e.pitchA {
			found = &ownerList[i]
		}
	}
	if found == nil {
		t.Fatalf("owner must still see their deactivated pitch in ListForActor")
	}
	if found.IsActive {
		t.Fatalf("owner's view of the pitch must report IsActive=false")
	}

	// Existing booking unchanged.
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM bookings WHERE pitch_id = $1 LIMIT 1`, e.pitchA).Scan(&status); err != nil {
		t.Fatalf("read booking: %v", err)
	}
	if status != "confirmed" {
		t.Fatalf("existing booking status = %q after deactivation, want unchanged 'confirmed'", status)
	}
}

// ── image persist: scoping, replace-returns-old-public_id, clear ─────────────

func TestImage_SetScopingAndCleanup(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	const (
		url1 = "https://res.cloudinary.com/c/image/upload/v1/malaeb/pitches/one.webp"
		pid1 = "malaeb/pitches/one"
		url2 = "https://res.cloudinary.com/c/image/upload/v1/malaeb/pitches/two.webp"
		pid2 = "malaeb/pitches/two"
	)

	// Foreign owner → ErrNoRows (→404), pitch B untouched.
	if _, err := e.model.SetPitchImage(ctx, e.pitchB, e.ownerA(), url1, pid1); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign image set: err = %v, want pgx.ErrNoRows", err)
	}

	// Owner sets image on own pitch → first set has no previous asset.
	old, err := e.model.SetPitchImage(ctx, e.pitchA, e.ownerA(), url1, pid1)
	if err != nil {
		t.Fatalf("owner set own image: %v", err)
	}
	if old != "" {
		t.Fatalf("first set should report no previous public_id, got %q", old)
	}

	// Replacing returns the PREVIOUS public_id (the asset to destroy).
	old, err = e.model.SetPitchImage(ctx, e.pitchA, e.ownerA(), url2, pid2)
	if err != nil {
		t.Fatalf("replace image: %v", err)
	}
	if old != pid1 {
		t.Fatalf("replace should return previous public_id %q, got %q", pid1, old)
	}

	// Persisted columns reflect the latest set.
	var gotURL, gotPID string
	if err := e.pool.QueryRow(ctx,
		`SELECT image_url, image_public_id FROM pitches WHERE id = $1`, e.pitchA,
	).Scan(&gotURL, &gotPID); err != nil {
		t.Fatalf("read image cols: %v", err)
	}
	if gotURL != url2 || gotPID != pid2 {
		t.Fatalf("persisted (%q,%q), want (%q,%q)", gotURL, gotPID, url2, pid2)
	}

	// Clearing (empty strings) returns the previous public_id for cleanup and
	// blanks the columns.
	old, err = e.model.SetPitchImage(ctx, e.pitchA, e.ownerA(), "", "")
	if err != nil {
		t.Fatalf("clear image: %v", err)
	}
	if old != pid2 {
		t.Fatalf("clear should return previous public_id %q, got %q", pid2, old)
	}
	if err := e.pool.QueryRow(ctx,
		`SELECT image_url, image_public_id FROM pitches WHERE id = $1`, e.pitchA,
	).Scan(&gotURL, &gotPID); err != nil {
		t.Fatalf("read image cols after clear: %v", err)
	}
	if gotURL != "" || gotPID != "" {
		t.Fatalf("after clear cols = (%q,%q), want empty", gotURL, gotPID)
	}

	// Admin may set on any pitch.
	if _, err := e.model.SetPitchImage(ctx, e.pitchB, e.admin(), url1, pid1); err != nil {
		t.Fatalf("admin set on any pitch: %v", err)
	}
}

func TestImage_SetSoftDeletedGets404(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	if _, err := e.model.SoftDeletePitch(ctx, e.pitchA, e.ownerA()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := e.model.SetPitchImage(ctx, e.pitchA, e.ownerA(),
		"https://res.cloudinary.com/c/image/upload/v1/malaeb/pitches/x.webp", "malaeb/pitches/x",
	); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("image set on soft-deleted pitch: err = %v, want pgx.ErrNoRows (→404)", err)
	}
}

func containsPitch(ps []Pitch, id int) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

// ── description round-trips create → read; scoped update persists; raw at rest ─
//
// Covers the audit's silently-dropped-description fix: a description set at
// create is persisted and read back by GetByID, GetAll, and ListForActor; the
// existing actor-scoped UpdatePitch changes it (and a foreign owner is still
// 404 with the value untouched); empty clears it. A markup payload is stored
// and read back VERBATIM — proving storage stays raw (the XSS guard is the
// frontend's render-time escaping, not encoding at rest).
func TestDescription_RoundTripAndScopedUpdate(t *testing.T) {
	e := newScopingEnv(t)
	ctx := context.Background()

	const xss = `<script>alert(1)</script><img src=x onerror=alert(1)>`

	// Create a fresh pitch (owned by owner A) carrying the markup description.
	created, err := e.model.CreatePitch(ctx, CreatePitchRequest{
		Name: "Desc Pitch", Neighborhood: "Khalda", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: e.ownerAID, Description: xss,
		MapsURL: "https://maps.app.goo.gl/descSeed", // satisfy the PR 4.2 location gate on update
	})
	if err != nil {
		t.Fatalf("create with description: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, created.ID)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM pitches WHERE id = $1`, created.ID)
	})

	// RETURNING projection on create already carries it.
	if created.Description != xss {
		t.Fatalf("create returned description %q, want raw %q", created.Description, xss)
	}

	// GetByID (detail projection) reads it back verbatim.
	got, err := e.model.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Description != xss {
		t.Fatalf("GetByID description %q, want raw %q (stored raw, not encoded)", got.Description, xss)
	}

	// ListForActor (owner list projection) reads it back too.
	list, err := e.model.ListForActor(ctx, e.ownerA())
	if err != nil {
		t.Fatalf("ListForActor: %v", err)
	}
	var found bool
	for _, p := range list {
		if p.ID == created.ID {
			found = true
			if p.Description != xss {
				t.Fatalf("ListForActor description %q, want raw %q", p.Description, xss)
			}
		}
	}
	if !found {
		t.Fatalf("owner A list missing created pitch %d", created.ID)
	}

	// GetAll (public list projection) also returns it (pitch is active + live).
	all, err := e.model.GetAll(ctx, PitchFilters{})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if !containsPitch(all, created.ID) {
		t.Fatalf("GetAll missing created pitch %d", created.ID)
	}

	// Scoped update changes the description for the owner.
	updated, err := e.model.UpdatePitch(ctx, created.ID, e.ownerA(), UpdatePitchRequest{
		Name: "Desc Pitch", Description: "وصف محدّث",
	})
	if err != nil {
		t.Fatalf("owner update description: %v", err)
	}
	if updated.Description != "وصف محدّث" {
		t.Fatalf("after update description = %q, want %q", updated.Description, "وصف محدّث")
	}

	// A foreign owner cannot update it (404) — and the value is untouched.
	if _, err := e.model.UpdatePitch(ctx, created.ID, auth.Actor{UserID: e.ownerBID, Role: auth.RoleOwner},
		UpdatePitchRequest{Name: "Desc Pitch", Description: "hijacked"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign owner update: err = %v, want pgx.ErrNoRows", err)
	}
	afterForeign, err := e.model.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after foreign attempt: %v", err)
	}
	if afterForeign.Description != "وصف محدّث" {
		t.Fatalf("foreign attempt mutated description to %q", afterForeign.Description)
	}

	// Empty description clears it (the reused form submits full desired state).
	cleared, err := e.model.UpdatePitch(ctx, created.ID, e.ownerA(), UpdatePitchRequest{
		Name: "Desc Pitch", Description: "",
	})
	if err != nil {
		t.Fatalf("owner clear description: %v", err)
	}
	if cleared.Description != "" {
		t.Fatalf("after clear description = %q, want empty", cleared.Description)
	}
}
