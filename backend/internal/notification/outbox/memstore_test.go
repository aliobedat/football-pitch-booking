package outbox

// In-memory test doubles for the outbox seams. memStore mirrors the semantics of
// PostgresStore closely enough to drive the Worker deterministically without a
// database — most importantly, ClaimDue increments attempts and flips status to
// 'processing' in one step, exactly as the SQL claim does, so the worker's
// attempt-budget logic is exercised faithfully.

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/ali/football-pitch-api/internal/notification"
)

// memStore is an in-memory Store.
type memStore struct {
	mu     sync.Mutex
	jobs   map[int64]*Job
	nextID int64
	now    func() time.Time
}

// memStoreOption configures a memStore.
type memStoreOption func(*memStore)

// withMemClock injects the time source memStore uses to default a job's
// AvailableAt at enqueue time. A store and the worker draining it must share one
// clock, otherwise a job enqueued with the real wall clock can land just after a
// fixed test "now" and never satisfy ClaimDue's next_attempt_at <= now predicate
// — a timing-dependent flake. Defaults to time.Now when unset.
func withMemClock(now func() time.Time) memStoreOption {
	return func(m *memStore) {
		if now != nil {
			m.now = now
		}
	}
}

func newMemStore(opts ...memStoreOption) *memStore {
	m := &memStore{jobs: make(map[int64]*Job), now: time.Now}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *memStore) Enqueue(_ context.Context, j NewJob) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	available := j.AvailableAt
	if available.IsZero() {
		available = m.now()
	}
	maxAttempts := j.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	m.nextID++
	id := m.nextID
	m.jobs[id] = &Job{
		ID:            id,
		Recipient:     j.Recipient,
		Kind:          j.Kind,
		Envelope:      j.Envelope,
		Status:        StatusPending,
		Attempts:      0,
		MaxAttempts:   maxAttempts,
		NextAttemptAt: available,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	return id, nil
}

func (m *memStore) ClaimDue(_ context.Context, now time.Time, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var due []*Job
	for _, j := range m.jobs {
		if j.Status == StatusPending && !j.NextAttemptAt.After(now) {
			due = append(due, j)
		}
	}
	// Mirror "ORDER BY next_attempt_at" so claiming is deterministic.
	sort.Slice(due, func(i, k int) bool { return due[i].NextAttemptAt.Before(due[k].NextAttemptAt) })
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}

	claimed := make([]Job, 0, len(due))
	for _, j := range due {
		j.Status = StatusProcessing
		j.Attempts++
		j.UpdatedAt = now
		claimed = append(claimed, *j) // return a copy with the bumped attempt count
	}
	return claimed, nil
}

func (m *memStore) MarkSucceeded(_ context.Context, id int64, providerMessageID string) error {
	return m.mutate(id, func(j *Job) {
		j.Status = StatusSucceeded
		j.ProviderMessageID = providerMessageID
		j.LastError = ""
	})
}

func (m *memStore) Reschedule(_ context.Context, id int64, nextAttemptAt time.Time, lastErr string) error {
	return m.mutate(id, func(j *Job) {
		j.Status = StatusPending
		j.NextAttemptAt = nextAttemptAt
		j.LastError = lastErr
	})
}

func (m *memStore) MarkDeadLetter(_ context.Context, id int64, lastErr string) error {
	return m.mutate(id, func(j *Job) {
		j.Status = StatusDeadLetter
		j.LastError = lastErr
	})
}

func (m *memStore) MarkBlocked(_ context.Context, id int64, reason string) error {
	return m.mutate(id, func(j *Job) {
		j.Status = StatusBlocked
		j.LastError = reason
	})
}

func (m *memStore) mutate(id int64, fn func(*Job)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return ErrJobNotFound
	}
	fn(j)
	j.UpdatedAt = time.Now()
	return nil
}

// get returns a snapshot of a job for assertions.
func (m *memStore) get(id int64) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// ── DeliveryStore double ─────────────────────────────────────────────────────

type sentRecord struct {
	providerMessageID string
	jobID             *int64
	recipient         string
}

type memDeliveries struct {
	mu       sync.Mutex
	sent     []sentRecord
	statuses []DeliveryUpdate
}

func (d *memDeliveries) RecordSent(_ context.Context, providerMessageID string, jobID *int64, recipient string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, sentRecord{providerMessageID, jobID, recipient})
	return nil
}

func (d *memDeliveries) ApplyStatus(_ context.Context, u DeliveryUpdate) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statuses = append(d.statuses, u)
	return nil
}

func (d *memDeliveries) sentCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

// ── Sender double ────────────────────────────────────────────────────────────

// stubSender returns a fixed result/error and counts its invocations.
type stubSender struct {
	result notification.DeliveryResult
	err    error
	calls  int
}

func (s *stubSender) Send(context.Context, notification.OutboundMessage) (notification.DeliveryResult, error) {
	s.calls++
	return s.result, s.err
}

// ── Shared fixtures ──────────────────────────────────────────────────────────

const testRecipient = "+962790000000"

// validEnvelope marshals a known-good booking_confirmed message for enqueueing.
func validEnvelope(t interface{ Fatalf(string, ...any) }) []byte {
	start := time.Date(2026, 6, 2, 18, 0, 0, 0, time.UTC)
	msg := notification.OutboundMessage{
		Recipient: testRecipient,
		Kind:      notification.KindBookingConfirmed,
		Payload: notification.BookingConfirmedPayload{
			BookingID: 1, PitchName: "Pitch A", StartTime: start, EndTime: start.Add(time.Hour),
		},
	}
	b, err := notification.MarshalOutbound(msg)
	if err != nil {
		t.Fatalf("marshal fixture envelope: %v", err)
	}
	return b
}
