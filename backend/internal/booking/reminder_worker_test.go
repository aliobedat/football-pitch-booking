package booking

// Unit tests for the PART 7 reminder worker. They drive the worker against an
// in-memory ReminderStore that mirrors the real claim semantics — the eligibility
// window (now, now+horizon], the not-yet-reminded guard, the per-scan limit, and
// the once-only marking — so the worker's orchestration and the envelope it
// builds are exercised deterministically without a database. The real SQL claim
// (including FOR UPDATE SKIP LOCKED) is covered by the Postgres integration test
// in internal/repository.

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/repository"
)

// fakeBooking is a seeded booking in the fake store.
type fakeBooking struct {
	id        int64
	phone     string
	pitch     string
	start     time.Time
	end       time.Time
	confirmed bool
	reminded  bool
}

// fakeReminderStore models the repository's atomic claim+mark+enqueue against an
// in-memory slice of bookings. ClaimDueReminders applies the same eligibility
// filter the SQL does and records every enqueued job for assertions.
type fakeReminderStore struct {
	mu       sync.Mutex
	bookings []*fakeBooking
	enqueued []repository.ReminderJob
}

func (f *fakeReminderStore) ClaimDueReminders(
	_ context.Context,
	now time.Time,
	horizon time.Duration,
	limit int,
	build repository.ReminderBuildFunc,
) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if limit <= 0 {
		return 0, nil
	}

	lower := now.UTC()
	upper := lower.Add(horizon)

	// Stage all mutations and only apply them if the whole batch builds cleanly —
	// mirroring the repository's single-transaction "all or nothing" guarantee.
	type staged struct {
		b   *fakeBooking
		job repository.ReminderJob
	}
	var batch []staged

	for _, b := range f.bookings {
		if len(batch) >= limit {
			break
		}
		if !b.confirmed || b.reminded || b.phone == "" {
			continue
		}
		if !b.start.After(lower) || b.start.After(upper) {
			continue
		}
		job, err := build(repository.DueReminder{
			BookingID: b.id,
			Phone:     b.phone,
			PitchName: b.pitch,
			StartTime: b.start,
			EndTime:   b.end,
		})
		if err != nil {
			return 0, err // abort: nothing staged is applied
		}
		batch = append(batch, staged{b, job})
	}

	for _, s := range batch {
		s.b.reminded = true
		f.enqueued = append(f.enqueued, s.job)
	}
	return len(batch), nil
}

func (f *fakeReminderStore) reminded(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.id == id {
			return b.reminded
		}
	}
	return false
}

func silentReminderWorker(store ReminderStore, cfg ReminderConfig, now time.Time) *ReminderWorker {
	return NewReminderWorker(store, cfg,
		WithReminderLogger(log.New(io.Discard, "", 0)),
		WithReminderClock(func() time.Time { return now }),
	)
}

// TestReminderWorker_PicksOnlyEligible seeds bookings across every time window
// and confirms exactly the eligible ones (confirmed, not reminded, starting in
// the next 24h) are picked up, marked, and enqueued.
func TestReminderWorker_PicksOnlyEligible(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeReminderStore{bookings: []*fakeBooking{
		{id: 1, phone: "+962790000001", pitch: "Pitch A", start: now.Add(2 * time.Hour), end: now.Add(3 * time.Hour), confirmed: true},                 // eligible
		{id: 2, phone: "+962790000002", pitch: "Pitch B", start: now.Add(23 * time.Hour), end: now.Add(24 * time.Hour), confirmed: true},               // eligible (edge, inside)
		{id: 3, phone: "+962790000003", pitch: "Pitch C", start: now.Add(25 * time.Hour), end: now.Add(26 * time.Hour), confirmed: true},               // beyond horizon
		{id: 4, phone: "+962790000004", pitch: "Pitch D", start: now.Add(-1 * time.Hour), end: now.Add(1 * time.Hour), confirmed: true},                // already started
		{id: 5, phone: "+962790000005", pitch: "Pitch E", start: now.Add(4 * time.Hour), end: now.Add(5 * time.Hour), confirmed: false},                // not confirmed (cancelled)
		{id: 6, phone: "+962790000006", pitch: "Pitch F", start: now.Add(5 * time.Hour), end: now.Add(6 * time.Hour), confirmed: true, reminded: true}, // already reminded
	}}

	w := silentReminderWorker(store, ReminderConfig{}, now)

	n, err := w.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if n != 2 {
		t.Fatalf("processed = %d, want 2 (bookings 1 and 2)", n)
	}

	// Exactly bookings 1 and 2 are marked; the others are untouched.
	for _, id := range []int64{1, 2} {
		if !store.reminded(id) {
			t.Errorf("booking %d should be marked reminded", id)
		}
	}
	for _, id := range []int64{3, 4, 5} {
		if store.reminded(id) {
			t.Errorf("booking %d must NOT be marked reminded", id)
		}
	}

	if len(store.enqueued) != 2 {
		t.Fatalf("enqueued = %d, want 2", len(store.enqueued))
	}
}

// TestReminderWorker_EnqueuesReminderEnvelope checks the job the worker builds is
// a well-formed booking_reminder addressed to the player, round-tripping through
// the durable outbox serialization back to the original coordinates.
func TestReminderWorker_EnqueuesReminderEnvelope(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	start := now.Add(6 * time.Hour)
	end := start.Add(time.Hour)
	store := &fakeReminderStore{bookings: []*fakeBooking{
		{id: 42, phone: "+962799999999", pitch: "Olympic Field", start: start, end: end, confirmed: true},
	}}

	w := silentReminderWorker(store, ReminderConfig{MaxAttempts: 8}, now)

	if _, err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(store.enqueued))
	}

	job := store.enqueued[0]
	if job.Recipient != "+962799999999" {
		t.Errorf("recipient = %q, want the player's phone", job.Recipient)
	}
	if job.Kind != string(notification.KindBookingReminder) {
		t.Errorf("kind = %q, want %q", job.Kind, notification.KindBookingReminder)
	}
	if job.MaxAttempts != 8 {
		t.Errorf("max attempts = %d, want 8 (from config)", job.MaxAttempts)
	}

	msg, err := notification.UnmarshalOutbound(job.Envelope)
	if err != nil {
		t.Fatalf("envelope does not round-trip: %v", err)
	}
	p, ok := msg.Payload.(notification.BookingReminderPayload)
	if !ok {
		t.Fatalf("payload type = %T, want BookingReminderPayload", msg.Payload)
	}
	if p.BookingID != 42 || p.PitchName != "Olympic Field" || !p.StartTime.Equal(start) || !p.EndTime.Equal(end) {
		t.Errorf("payload = %+v, does not match the booking", p)
	}
}

// TestReminderWorker_RunsExactlyOnce confirms the once-only guard: a second scan
// over the same data enqueues nothing more, since the first marked the bookings.
func TestReminderWorker_RunsExactlyOnce(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeReminderStore{bookings: []*fakeBooking{
		{id: 1, phone: "+962790000001", pitch: "A", start: now.Add(2 * time.Hour), end: now.Add(3 * time.Hour), confirmed: true},
	}}
	w := silentReminderWorker(store, ReminderConfig{}, now)

	first, err := w.ProcessBatch(context.Background())
	if err != nil || first != 1 {
		t.Fatalf("first scan: n=%d err=%v, want 1/nil", first, err)
	}
	second, err := w.ProcessBatch(context.Background())
	if err != nil || second != 0 {
		t.Fatalf("second scan: n=%d err=%v, want 0/nil", second, err)
	}
	if len(store.enqueued) != 1 {
		t.Errorf("enqueued total = %d, want 1 (exactly once)", len(store.enqueued))
	}
}

// TestReminderWorker_RespectsBatchSize ensures no more than BatchSize bookings
// are claimed per scan.
func TestReminderWorker_RespectsBatchSize(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	var seed []*fakeBooking
	for i := int64(1); i <= 5; i++ {
		seed = append(seed, &fakeBooking{
			id: i, phone: "+96279000000" + string(rune('0'+i)), pitch: "P",
			start: now.Add(time.Duration(i) * time.Hour), end: now.Add(time.Duration(i+1) * time.Hour),
			confirmed: true,
		})
	}
	store := &fakeReminderStore{bookings: seed}
	w := silentReminderWorker(store, ReminderConfig{BatchSize: 2}, now)

	n, err := w.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if n != 2 {
		t.Errorf("processed = %d, want 2 (batch size cap)", n)
	}
}

// TestReminderWorker_BuildErrorAbortsBatch confirms a marshalling failure aborts
// the batch without marking anything reminded — the store's all-or-nothing
// transaction contract, surfaced through the build callback.
func TestReminderWorker_BuildErrorAbortsBatch(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeReminderStore{bookings: []*fakeBooking{
		{id: 1, phone: "+962790000001", pitch: "A", start: now.Add(2 * time.Hour), end: now.Add(3 * time.Hour), confirmed: true},
	}}

	wantErr := errors.New("boom")
	// A worker whose build always fails (simulating an un-marshalable payload).
	w := silentReminderWorker(store, ReminderConfig{}, now)
	failing := func(repository.DueReminder) (repository.ReminderJob, error) { return repository.ReminderJob{}, wantErr }

	_, err := store.ClaimDueReminders(context.Background(), w.now(), w.cfg.Horizon, w.cfg.BatchSize, failing)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if store.reminded(1) {
		t.Error("booking 1 must not be marked when the batch aborted")
	}
	if len(store.enqueued) != 0 {
		t.Errorf("enqueued = %d, want 0 on abort", len(store.enqueued))
	}
}

// TestReminderWorker_RunStopsOnContextCancel confirms Run returns the context
// error promptly on shutdown.
func TestReminderWorker_RunStopsOnContextCancel(t *testing.T) {
	now := time.Now()
	store := &fakeReminderStore{}
	w := silentReminderWorker(store, ReminderConfig{PollInterval: time.Hour}, now)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: Run must do one eager scan then exit

	err := w.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
}
