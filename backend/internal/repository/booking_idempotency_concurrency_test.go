package repository

// §5.9 — idempotency under TRUE concurrency. The existing booking_idempotency_test
// suite proves SEQUENTIAL behavior (replay, fingerprint-mismatch, pending, key
// reuse). The gap is the concurrent same-key race, which this fills:
//
//   Two simultaneous CreateBookingIdempotent calls with the SAME key for the SAME
//   slot → EXACTLY ONE booking row. One goroutine wins with a fresh insert
//   (replayed=false); the other must NOT insert a second booking — it either
//   replays the stored result (replayed=true, same id) OR gets
//   ErrIdempotencyInProgress (the documented concurrent outcome, since the claim
//   uses ON CONFLICT DO NOTHING and the loser may read the winner's still-pending
//   row). Never two fresh inserts, never a double-book.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run IdempotencyConcurrency -v

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ali/football-pitch-api/internal/models"
)

func TestIdempotencyConcurrency_SameKeySingleBooking(t *testing.T) {
	env := newBookableEnv(t)
	env.cleanupIdem(t)
	ctx := context.Background()

	idem := models.IdempotencyParams{Key: "concurrent-key", Endpoint: "POST /bookings", Fingerprint: "fp-A"}

	const n = 2
	var wg sync.WaitGroup
	wg.Add(n)
	type result struct {
		booking  *models.Booking
		replayed bool
		err      error
	}
	results := make([]result, n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			b, replayed, err := env.repo.CreateBookingIdempotent(ctx, env.createReq(), idem)
			results[idx] = result{b, replayed, err}
		}(i)
	}
	wg.Wait()

	var fresh, replays, inProgress int
	var freshID int64
	for i, r := range results {
		switch {
		case r.err == nil && !r.replayed:
			fresh++
			if r.booking == nil {
				t.Fatalf("goroutine %d: fresh success but nil booking", i)
			}
			freshID = r.booking.ID
		case r.err == nil && r.replayed:
			replays++
		case errors.Is(r.err, ErrIdempotencyInProgress):
			inProgress++
		case errors.Is(r.err, ErrDoubleBooking):
			t.Fatalf("goroutine %d: ErrDoubleBooking — idempotency failed to dedupe the concurrent claim", i)
		default:
			t.Fatalf("goroutine %d: unexpected error %v", i, r.err)
		}
	}

	// Exactly one fresh insert; the other replayed or saw in-progress — both are
	// correct "no double-book" outcomes.
	if fresh != 1 {
		t.Fatalf("CRITICAL: %d fresh inserts for one idempotency key, want EXACTLY 1", fresh)
	}
	if replays+inProgress != n-1 {
		t.Fatalf("loser outcomes replay=%d inProgress=%d, want exactly %d combined", replays, inProgress, n-1)
	}
	// If the loser replayed, it must echo the winner's booking id (not a new one).
	for _, r := range results {
		if r.err == nil && r.replayed && r.booking != nil && r.booking.ID != freshID {
			t.Fatalf("replay returned booking %d, want winner's %d", r.booking.ID, freshID)
		}
	}

	// The authoritative invariant: exactly one booking row exists for the slot.
	if got := env.bookingCount(ctx, t); got != 1 {
		t.Fatalf("CRITICAL double-book: %d booking rows for one idempotency key, want EXACTLY 1", got)
	}
}
