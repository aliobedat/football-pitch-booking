package repository

// Integration tests for Admin/Owner/Player ownership scoping of CancelBooking —
// the IDOR guard on booking cancellation. They exercise the REAL resolve+lock
// SQL (the pitches-join ownership predicate, the 404-not-403 semantics, the
// confirmed-only state guard, and the audit attribution) against a live
// database, and are SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set — so
// the default `go test ./...` run stays green.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run CancelScoping

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/testutil"
)

type cancelScopeEnv struct {
	pool     *pgxpool.Pool
	repo     BookingRepository
	ownerAID int64
	ownerBID int64
	adminID  int64
	playerID int64
	pitchA   int64 // owned by ownerA
	pitchB   int64 // owned by ownerB
}

func newCancelScopeEnv(t *testing.T) *cancelScopeEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping cancel scoping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}

	suffix := testutil.UniqueSuffix() % 1_000_000
	mkUser := func(name, prefix, role string) int64 {
		var id int64
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	e := &cancelScopeEnv{pool: pool, repo: NewBookingRepository(pool)}
	e.ownerAID = mkUser("CS Owner A", "80", auth.RoleOwner)
	e.ownerBID = mkUser("CS Owner B", "81", auth.RoleOwner)
	e.adminID = mkUser("CS Admin", "82", auth.RoleAdmin)
	e.playerID = mkUser("CS Player", "83", auth.RolePlayer)

	model := &data.PitchModel{DB: pool}
	mkPitch := func(name string, owner int64) int64 {
		p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
			Name: name, Neighborhood: "Amman", Surface: "artificial_grass",
			Format: "خماسي", PricePerHour: 30, OwnerID: int(owner),
		})
		if err != nil {
			pool.Close()
			t.Fatalf("seed pitch %s: %v", name, err)
		}
		return int64(p.ID)
	}
	e.pitchA = mkPitch("CS Pitch A", e.ownerAID)
	e.pitchB = mkPitch("CS Pitch B", e.ownerBID)

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		pitches := []int64{e.pitchA, e.pitchB}
		// status_transitions cascade on bookings delete (FK ON DELETE CASCADE).
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, pitches)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, pitches)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`,
			[]int64{e.ownerAID, e.ownerBID, e.adminID, e.playerID})
		pool.Close()
	})
	return e
}

// seedConfirmed inserts a confirmed booking on the pitch for the env player and
// returns its id.
func (e *cancelScopeEnv) seedConfirmed(t *testing.T, pitchID int64) int64 {
	t.Helper()
	start := time.Now().UTC().Add(72 * time.Hour)
	var id int64
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), 30, 'confirmed', 'player')
		RETURNING id
	`, pitchID, e.playerID, start, start.Add(time.Hour)).Scan(&id); err != nil {
		t.Fatalf("seed confirmed booking: %v", err)
	}
	return id
}

func (e *cancelScopeEnv) statusOf(t *testing.T, bookingID int64) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status FROM bookings WHERE id = $1`, bookingID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

func (e *cancelScopeEnv) cancelTransitionCount(t *testing.T, bookingID int64) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM status_transitions WHERE booking_id = $1 AND to_status = 'cancelled'
	`, bookingID).Scan(&n); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	return n
}

func actor(id int64, role string) CancelBookingParams {
	return CancelBookingParams{ActorID: &id, ActorRole: role}
}

// ── core security test: foreign owner → 404 and ZERO side effects ────────────

func TestCancelScoping_ForeignOwnerGets404NoSideEffects(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	bID := e.seedConfirmed(t, e.pitchB) // booking on owner B's pitch

	p := actor(e.ownerAID, ActorOwner)
	p.BookingID = bID
	p.Reason = "should not happen"

	_, err := e.repo.CancelBooking(ctx, p)
	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("owner A cancelling owner B's booking: err = %v, want ErrBookingNotFound (→404)", err)
	}
	// Zero side effects: slot NOT released (still confirmed) and NO audit row.
	if got := e.statusOf(t, bID); got != "confirmed" {
		t.Fatalf("booking status = %q after rejected cancel, want it untouched ('confirmed')", got)
	}
	if n := e.cancelTransitionCount(t, bID); n != 0 {
		t.Fatalf("found %d cancel audit rows after a rejected cancel, want 0", n)
	}
}

// ── owner cancelling on their OWN pitch succeeds, audit attributes the owner ──

func TestCancelScoping_OwnerCancelsOwnPitch(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	bID := e.seedConfirmed(t, e.pitchA)
	p := actor(e.ownerAID, ActorOwner)
	p.BookingID = bID

	b, err := e.repo.CancelBooking(ctx, p)
	if err != nil {
		t.Fatalf("owner cancelling own pitch's booking: %v", err)
	}
	if b.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", b.Status)
	}
	assertAudit(t, e, bID, e.ownerAID, ActorOwner)
}

// ── admin may cancel ANY booking, audit attributes the admin ─────────────────

func TestCancelScoping_AdminCancelsAnyBooking(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	bID := e.seedConfirmed(t, e.pitchB) // admin cancels a booking on owner B's pitch
	p := actor(e.adminID, ActorAdmin)
	p.BookingID = bID

	if _, err := e.repo.CancelBooking(ctx, p); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if got := e.statusOf(t, bID); got != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got)
	}
	assertAudit(t, e, bID, e.adminID, ActorAdmin)
}

// ── player may cancel their OWN booking (route allows player) ────────────────

func TestCancelScoping_PlayerCancelsOwnBooking(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	bID := e.seedConfirmed(t, e.pitchA)
	p := actor(e.playerID, ActorPlayer)
	p.BookingID = bID

	if _, err := e.repo.CancelBooking(ctx, p); err != nil {
		t.Fatalf("player cancelling own booking: %v", err)
	}
	if got := e.statusOf(t, bID); got != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got)
	}
	assertAudit(t, e, bID, e.playerID, ActorPlayer)
}

// ── already-cancelled → 409, no duplicate side effects ───────────────────────

func TestCancelScoping_AlreadyCancelledIs409(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	bID := e.seedConfirmed(t, e.pitchA)
	p := actor(e.ownerAID, ActorOwner)
	p.BookingID = bID

	if _, err := e.repo.CancelBooking(ctx, p); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	// Second cancel of the same booking.
	_, err := e.repo.CancelBooking(ctx, p)
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("re-cancel: err = %v, want ErrInvalidStatusTransition (→409)", err)
	}
	// No duplicate audit row — exactly one cancel transition exists.
	if n := e.cancelTransitionCount(t, bID); n != 1 {
		t.Fatalf("cancel audit rows = %d after a re-cancel, want exactly 1", n)
	}
}

// ── deactivated pitch exposes no availability (→ ErrPitchNotFound → 404) ──────

func TestCancelScoping_AvailabilityHiddenWhenInactive(t *testing.T) {
	e := newCancelScopeEnv(t)
	ctx := context.Background()

	// Active pitch returns availability fine.
	if _, err := e.repo.GetBookedSlots(ctx, int(e.pitchA), time.Now().UTC()); err != nil {
		t.Fatalf("availability for active pitch should succeed: %v", err)
	}

	// Deactivate pitch A via the pitch model, then availability must 404.
	model := &data.PitchModel{DB: e.pool}
	if err := model.SetPitchActive(ctx, int(e.pitchA), auth.Actor{UserID: int(e.ownerAID), Role: auth.RoleOwner}, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := e.repo.GetBookedSlots(ctx, int(e.pitchA), time.Now().UTC()); !errors.Is(err, ErrPitchNotFound) {
		t.Fatalf("availability for inactive pitch: err = %v, want ErrPitchNotFound (→404)", err)
	}
}

func assertAudit(t *testing.T, e *cancelScopeEnv, bookingID, actorID int64, role string) {
	t.Helper()
	var gotActor int64
	var gotRole string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT actor_id, actor_role FROM status_transitions
		WHERE booking_id = $1 AND to_status = 'cancelled'
		ORDER BY id DESC LIMIT 1
	`, bookingID).Scan(&gotActor, &gotRole); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if gotActor != actorID || gotRole != role {
		t.Fatalf("audit attributed to actor=%d role=%q, want actor=%d role=%q",
			gotActor, gotRole, actorID, role)
	}
}
