package repository

// Integration tests for CreateBookingIdempotent against a live database. They
// exercise the REAL claim/replay/conflict SQL (the user-scoped unique key, the
// completed-response replay, the fingerprint-mismatch 422, and the pending 409),
// reusing the bookable test harness. SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL
// is set, so the default `go test ./...` run stays green.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Idempotency

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/models"
)

func (e *bookableEnv) cleanupIdem(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM booking_idempotency_keys WHERE user_id = $1`, e.playerID)
	})
}

// TestIdempotency_ReplayReturnsSameBooking: a repeated key+body creates the
// booking once and replays the ORIGINAL on the second call — no second row.
func TestIdempotency_ReplayReturnsSameBooking(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	req := env.createReq()
	idem := models.IdempotencyParams{Key: "key-replay", Endpoint: "POST /bookings", Fingerprint: "fp-A"}

	first, replayed, err := env.repo.CreateBookingIdempotent(ctx, req, idem)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if replayed {
		t.Fatal("first create reported replayed=true, want false")
	}

	second, replayed2, err := env.repo.CreateBookingIdempotent(ctx, req, idem)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if !replayed2 {
		t.Error("second create reported replayed=false, want true")
	}
	if second.ID != first.ID {
		t.Errorf("replay returned booking %d, want original %d", second.ID, first.ID)
	}
	if n := env.bookingCount(ctx, t); n != 1 {
		t.Errorf("booking count = %d, want 1 (no duplicate from replay)", n)
	}
}

// TestIdempotency_FingerprintMismatch: the same key with a DIFFERENT body is a
// client bug → ErrIdempotencyKeyConflict (422), and creates no second booking.
func TestIdempotency_FingerprintMismatch(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	req := env.createReq()
	if _, _, err := env.repo.CreateBookingIdempotent(ctx, req,
		models.IdempotencyParams{Key: "key-mismatch", Fingerprint: "fp-A"}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, _, err := env.repo.CreateBookingIdempotent(ctx, req,
		models.IdempotencyParams{Key: "key-mismatch", Fingerprint: "fp-DIFFERENT"})
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyConflict", err)
	}
	if n := env.bookingCount(ctx, t); n != 1 {
		t.Errorf("booking count = %d, want 1 (mismatch must not book again)", n)
	}
}

// TestIdempotency_PendingInProgress: a committed 'pending' claim (a prior attempt
// that crashed mid-flight) makes a same-key retry return ErrIdempotencyInProgress
// (409), without creating a booking.
func TestIdempotency_PendingInProgress(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	// Simulate an in-flight/orphaned claim: a committed pending row.
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO booking_idempotency_keys (idem_key, user_id, endpoint, fingerprint, status, expires_at)
		VALUES ('key-pending', $1, 'POST /bookings', 'fp-A', 'pending', now() + interval '1 hour')
	`, env.playerID); err != nil {
		t.Fatalf("seed pending claim: %v", err)
	}

	_, _, err := env.repo.CreateBookingIdempotent(ctx, env.createReq(),
		models.IdempotencyParams{Key: "key-pending", Fingerprint: "fp-A"})
	if !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("err = %v, want ErrIdempotencyInProgress", err)
	}
	if n := env.bookingCount(ctx, t); n != 0 {
		t.Errorf("booking count = %d, want 0 (pending claim blocks new booking)", n)
	}
}

// TestIdempotency_FailedBookingDoesNotBurnKey: when the booking insert fails
// (deactivated pitch), the whole tx rolls back INCLUDING the claim, so the key is
// reusable — a later valid attempt with the same key succeeds.
func TestIdempotency_FailedBookingDoesNotBurnKey(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	if _, err := env.pool.Exec(ctx, `UPDATE pitches SET is_active = false WHERE id = $1`, env.pitchID); err != nil {
		t.Fatalf("deactivate pitch: %v", err)
	}
	idem := models.IdempotencyParams{Key: "key-retry", Fingerprint: "fp-A"}
	if _, _, err := env.repo.CreateBookingIdempotent(ctx, env.createReq(), idem); !errors.Is(err, ErrPitchNotBookable) {
		t.Fatalf("err = %v, want ErrPitchNotBookable", err)
	}

	// Reactivate and retry with the SAME key — the rolled-back claim left it free.
	if _, err := env.pool.Exec(ctx, `UPDATE pitches SET is_active = true WHERE id = $1`, env.pitchID); err != nil {
		t.Fatalf("reactivate pitch: %v", err)
	}
	b, replayed, err := env.repo.CreateBookingIdempotent(ctx, env.createReq(), idem)
	if err != nil {
		t.Fatalf("retry create: %v", err)
	}
	if replayed || b == nil {
		t.Errorf("retry replayed=%v booking=%v, want fresh booking", replayed, b)
	}
}

// TestDeleteExpiredIdempotencyKeys prunes only rows past their TTL.
func TestDeleteExpiredIdempotencyKeys(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	if _, err := env.pool.Exec(ctx, `
		INSERT INTO booking_idempotency_keys (idem_key, user_id, endpoint, fingerprint, status, expires_at) VALUES
			('expired', $1, 'e', 'f', 'completed', now() - interval '1 hour'),
			('fresh',   $1, 'e', 'f', 'completed', now() + interval '1 hour')
	`, env.playerID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := env.repo.DeleteExpiredIdempotencyKeys(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n < 1 {
		t.Errorf("deleted %d, want >= 1 (the expired row)", n)
	}
	var remaining int
	if err := env.pool.QueryRow(ctx,
		`SELECT count(*) FROM booking_idempotency_keys WHERE user_id = $1 AND idem_key = 'fresh'`,
		env.playerID).Scan(&remaining); err != nil {
		t.Fatalf("count fresh: %v", err)
	}
	if remaining != 1 {
		t.Errorf("fresh row count = %d, want 1 (not pruned)", remaining)
	}
}
