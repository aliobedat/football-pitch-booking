package repository

// §5.3 — The booking-conflict referee under TRUE concurrency. The existing suites
// prove sequential conflict rejection; this proves that N transactions racing the
// SAME pitch+slot at once resolve to EXACTLY ONE committed booking — the
// FOR UPDATE pitch-row lock serialises the contenders and the EXCLUDE USING GIST
// constraint (bookings_pitch_id_booking_range_excl) rejects every loser with
// 23P01 → ErrDoubleBooking. No TOCTOU window, no double-commit.
//
// Reuses the bookableEnv harness. SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run BookingConcurrency

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestBookingConcurrency_SameSlotSingleWinner fires N concurrent CreateBooking
// calls for one identical slot and asserts: exactly one succeeds, every other
// fails with ErrDoubleBooking (never a different error, never a second success),
// and the DB ends with exactly one booking row.
func TestBookingConcurrency_SameSlotSingleWinner(t *testing.T) {
	env := newBookableEnv(t)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)

	errs := make([]error, n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			// Every goroutine requests the SAME pitch + slot (createReq is fixed).
			_, errs[idx] = env.repo.CreateBooking(ctx, env.createReq())
		}(i)
	}
	wg.Wait()

	var success, doubleBooked int
	for i, err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrDoubleBooking):
			doubleBooked++
		default:
			// Any OTHER error (deadlock, serialization failure surfacing raw, a 500-
			// class wrap) is a referee defect — surface it loudly.
			t.Fatalf("goroutine %d: unexpected error %v (want nil or ErrDoubleBooking)", i, err)
		}
	}

	if success != 1 {
		t.Fatalf("CRITICAL: %d concurrent creates succeeded for one slot, want EXACTLY 1", success)
	}
	if doubleBooked != n-1 {
		t.Fatalf("losers = %d, want %d (all non-winners must be ErrDoubleBooking)", doubleBooked, n-1)
	}

	// The authoritative check: the constraint is the referee — the DB must hold
	// exactly one row for this slot regardless of how the race interleaved.
	if committed := env.bookingCount(ctx, t); committed != 1 {
		t.Fatalf("CRITICAL: %d booking rows committed for one slot, want EXACTLY 1 (double-booking!)", committed)
	}
}
