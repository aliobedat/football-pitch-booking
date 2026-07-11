package repository

// Integration tests for the bookability guard on booking CREATION. They exercise
// the REAL resolve+lock SQL in CreateBooking (the FOR UPDATE pitch row lock and
// the is_active / deleted_at predicate) against a live database, asserting that a
// handcrafted POST /bookings can NOT create a booking on a deactivated or
// soft-deleted pitch — and that a rejected create writes NO booking row (so no
// slot is held). SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set, so the
// default `go test ./...` run stays green.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Bookable

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/testutil"
)

type bookableEnv struct {
	pool     *pgxpool.Pool
	repo     BookingRepository
	ownerID  int64
	playerID int64
	pitchID  int64
}

func newBookableEnv(t *testing.T) *bookableEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping bookability integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	suffix := testutil.UniqueSuffix() % 1_000_000
	mkUser := func(name, prefix, role string) int64 {
		var id int64
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	owner := mkUser("BK Owner", "84", "owner")
	player := mkUser("BK Player", "85", "player")

	pitchModel := &data.PitchModel{DB: pool}
	p, err := pitchModel.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "BK Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(owner),
	})
	if err != nil {
		t.Fatalf("seed pitch: %v", err)
	}
	pitch := int64(p.ID)

	env := &bookableEnv{pool: pool, repo: NewBookingRepository(pool), ownerID: owner, playerID: player, pitchID: pitch}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM status_transitions WHERE booking_id IN (SELECT id FROM bookings WHERE pitch_id = $1)`, pitch)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pitch)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, pitch)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitch)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{owner, player})
		pool.Close()
	})
	return env
}

func (e *bookableEnv) createReq() models.CreateBookingRequest {
	start := time.Now().UTC().Add(72 * time.Hour)
	return models.CreateBookingRequest{
		PitchID:   e.pitchID,
		PlayerID:  e.playerID,
		StartTime: start,
		EndTime:   start.Add(time.Hour),
	}
}

func (e *bookableEnv) bookingCount(ctx context.Context, t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE pitch_id = $1`, e.pitchID).Scan(&n); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	return n
}

// A booking against a DEACTIVATED pitch is rejected with ErrPitchNotBookable and
// writes NO booking row (so no slot is held, no audit row, no notification — the
// service never sees a success to dispatch on).
func TestBookable_DeactivatedPitchRejected(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	if _, err := env.pool.Exec(ctx, `UPDATE pitches SET is_active = false WHERE id = $1`, env.pitchID); err != nil {
		t.Fatalf("deactivate pitch: %v", err)
	}

	_, err := env.repo.CreateBooking(ctx, env.createReq())
	if !errors.Is(err, ErrPitchNotBookable) {
		t.Fatalf("CreateBooking on deactivated pitch: err = %v, want ErrPitchNotBookable", err)
	}
	if n := env.bookingCount(ctx, t); n != 0 {
		t.Fatalf("deactivated-pitch create wrote %d booking rows, want 0 (no slot held)", n)
	}
}

// A booking against a SOFT-DELETED pitch is rejected with the same zero-side-effect
// guarantee.
func TestBookable_SoftDeletedPitchRejected(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	if _, err := env.pool.Exec(ctx, `UPDATE pitches SET deleted_at = now() WHERE id = $1`, env.pitchID); err != nil {
		t.Fatalf("soft-delete pitch: %v", err)
	}

	_, err := env.repo.CreateBooking(ctx, env.createReq())
	if !errors.Is(err, ErrPitchNotBookable) {
		t.Fatalf("CreateBooking on soft-deleted pitch: err = %v, want ErrPitchNotBookable", err)
	}
	if n := env.bookingCount(ctx, t); n != 0 {
		t.Fatalf("soft-deleted-pitch create wrote %d booking rows, want 0 (no slot held)", n)
	}
}

// A genuinely missing pitch id keeps the existing 404 behavior (ErrPitchNotFound),
// distinct from the 409 bookability rejection.
func TestBookable_MissingPitchStillNotFound(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	req := env.createReq()
	req.PitchID = 999_000_001 // unused id
	_, err := env.repo.CreateBooking(ctx, req)
	if !errors.Is(err, ErrPitchNotFound) {
		t.Fatalf("CreateBooking on missing pitch: err = %v, want ErrPitchNotFound", err)
	}
}

// An ACTIVE, non-deleted pitch books exactly as before (no regression): a confirmed
// booking row is written with its audit transition.
func TestBookable_ActivePitchSucceeds(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	b, err := env.repo.CreateBooking(ctx, env.createReq())
	if err != nil {
		t.Fatalf("CreateBooking on active pitch: %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("booking status = %q, want confirmed", b.Status)
	}
	if n := env.bookingCount(ctx, t); n != 1 {
		t.Fatalf("active-pitch create wrote %d booking rows, want 1", n)
	}
}

// Concurrency: a booking-create and a deactivate issued against the same pitch are
// serialized by the FOR UPDATE row lock. The outcome is never a committed booking
// on a deactivated pitch — either the booking commits (pitch still active when the
// lock was taken) or it is rejected with ErrPitchNotBookable. We assert that
// invariant rather than a fixed winner, since the race ordering is nondeterministic.
func TestBookable_ConcurrentCreateAndDeactivateSerialized(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	var createErr error
	go func() {
		defer wg.Done()
		_, createErr = env.repo.CreateBooking(ctx, env.createReq())
	}()
	go func() {
		defer wg.Done()
		_, _ = env.pool.Exec(ctx, `UPDATE pitches SET is_active = false WHERE id = $1`, env.pitchID)
	}()
	wg.Wait()

	var isActive bool
	if err := env.pool.QueryRow(ctx, `SELECT is_active FROM pitches WHERE id = $1`, env.pitchID).Scan(&isActive); err != nil {
		t.Fatalf("read final is_active: %v", err)
	}
	committed := env.bookingCount(ctx, t)

	switch {
	case createErr == nil:
		// Booking committed → it must have done so on an active pitch.
		if committed != 1 {
			t.Fatalf("create succeeded but %d booking rows exist, want 1", committed)
		}
	case errors.Is(createErr, ErrPitchNotBookable):
		// Rejected after deactivation won the lock → no booking row.
		if committed != 0 {
			t.Fatalf("create rejected as not-bookable but %d booking rows exist, want 0", committed)
		}
		if isActive {
			t.Fatalf("create rejected as not-bookable yet pitch ended up active — inconsistent")
		}
	default:
		t.Fatalf("unexpected create error: %v", createErr)
	}

	// The invariant under test: a deactivated pitch must never carry a committed booking.
	if !isActive && committed != 0 {
		t.Fatalf("INVARIANT VIOLATED: pitch is deactivated but %d booking row(s) committed", committed)
	}
}
