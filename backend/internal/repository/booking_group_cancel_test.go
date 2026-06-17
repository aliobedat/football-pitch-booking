package repository

// Integration tests for the bulk future-occurrence cancellation (PR 3.5 Phase C)
// against a live DB. They grade: correctness (future cancelled + audited, past
// preserved, already-cancelled skipped), the empty-match → 0 contract, and strict
// owner scoping. SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run GroupCancel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ali/football-pitch-api/internal/auth"
)

// seedGroupRow inserts one manual booking row directly (bypassing the write-path)
// with a given range + status + group id, returning its id. Used to stage past and
// future occurrences precisely.
func (e *blockEnv) seedGroupRow(t *testing.T, group string, start time.Time, dur time.Duration, status string) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source, guest_name, recurrence_group_id)
		VALUES ($1, NULL, tstzrange($2::timestamptz, $3::timestamptz, '[)'), 30, $4::booking_status, 'manual', 'ضيف', $5)
		RETURNING id
	`, e.pitchID, start.UTC(), start.Add(dur).UTC(), status, group).Scan(&id); err != nil {
		t.Fatalf("seedGroupRow: %v", err)
	}
	return id
}

func (e *blockEnv) statusOf(t *testing.T, id int64) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM bookings WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("statusOf: %v", err)
	}
	return s
}

func (e *blockEnv) transitionCount(t *testing.T, bookingID int64, to string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM status_transitions WHERE booking_id = $1 AND to_status = $2`, bookingID, to).Scan(&n); err != nil {
		t.Fatalf("transitionCount: %v", err)
	}
	return n
}

// ── Correctness: future cancelled + audited, past preserved, re-cancel idempotent ─

func TestGroupCancel_CancelsFuturePreservesPast(t *testing.T) {
	e := newBlockEnv(t)
	group := uuid.NewString()
	now := time.Now()

	// Two PAST occurrences (history) and two FUTURE ones, spaced to avoid overlap.
	past1 := e.seedGroupRow(t, group, now.Add(-72*time.Hour), time.Hour, "confirmed")
	past2 := e.seedGroupRow(t, group, now.Add(-48*time.Hour), time.Hour, "confirmed")
	fut1 := e.seedGroupRow(t, group, now.Add(48*time.Hour), time.Hour, "confirmed")
	fut2 := e.seedGroupRow(t, group, now.Add(72*time.Hour), time.Hour, "confirmed")

	ownerID := e.ownerID
	n, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group, Actor: e.ownerActor(), ActorID: ownerID,
	})
	if err != nil {
		t.Fatalf("CancelFutureGroup: %v", err)
	}
	if n != 2 {
		t.Fatalf("cancelled_count = %d, want 2 (only the future occurrences)", n)
	}
	// Future → cancelled + audited.
	for _, id := range []int64{fut1, fut2} {
		if s := e.statusOf(t, id); s != "cancelled" {
			t.Errorf("future row %d status = %q, want cancelled", id, s)
		}
		if c := e.transitionCount(t, id, "cancelled"); c != 1 {
			t.Errorf("future row %d cancelled-transitions = %d, want 1", id, c)
		}
	}
	// Past → untouched, no cancellation transition.
	for _, id := range []int64{past1, past2} {
		if s := e.statusOf(t, id); s != "confirmed" {
			t.Errorf("past row %d status = %q, want confirmed (history preserved)", id, s)
		}
		if c := e.transitionCount(t, id, "cancelled"); c != 0 {
			t.Errorf("past row %d has a cancel transition, want 0", id)
		}
	}

	// Idempotent re-cancel: nothing left to cancel → 0, no new transitions.
	n2, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group, Actor: e.ownerActor(), ActorID: ownerID,
	})
	if err != nil {
		t.Fatalf("re-cancel: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-cancel count = %d, want 0 (already-cancelled rows skipped)", n2)
	}
	if c := e.transitionCount(t, fut1, "cancelled"); c != 1 {
		t.Errorf("future row %d cancelled-transitions after re-cancel = %d, want still 1", fut1, c)
	}
}

// ── Empty match → 0 (NOT an error / 404) ─────────────────────────────────────

func TestGroupCancel_EmptyMatchReturnsZero(t *testing.T) {
	e := newBlockEnv(t)
	ownerID := e.ownerID
	n, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: uuid.NewString(), Actor: e.ownerActor(), ActorID: ownerID,
	})
	if err != nil {
		t.Fatalf("empty-match cancel: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0 for an unknown group", n)
	}
}

// ── Ownership: a foreign owner cancels nothing ───────────────────────────────

func TestGroupCancel_ForeignOwnerCancelsNothing(t *testing.T) {
	e := newBlockEnv(t)
	group := uuid.NewString()
	now := time.Now()
	fut := e.seedGroupRow(t, group, now.Add(50*time.Hour), time.Hour, "confirmed")

	// otherID owns a DIFFERENT pitch — acting on e.pitchID's group must cancel 0.
	otherID := e.otherID
	n, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group,
		Actor: auth.Actor{UserID: int(otherID), Role: auth.RoleOwner}, ActorID: otherID,
	})
	if err != nil {
		t.Fatalf("foreign-owner cancel: %v", err)
	}
	if n != 0 {
		t.Fatalf("foreign owner cancelled %d rows, want 0", n)
	}
	if s := e.statusOf(t, fut); s != "confirmed" {
		t.Fatalf("row %d status = %q after foreign-owner attempt, want confirmed (untouched)", fut, s)
	}

	// Admin, by contrast, is unscoped and CAN cancel it.
	n2, err := e.repo.CancelFutureGroup(context.Background(), CancelGroupParams{
		PitchID: e.pitchID, GroupID: group,
		Actor: auth.Actor{UserID: int(otherID), Role: auth.RoleAdmin}, ActorID: otherID,
	})
	if err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("admin cancelled %d, want 1", n2)
	}
}
