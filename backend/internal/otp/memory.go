package otp

// MemoryStore is the in-memory implementation of both Store and RateLimiter used
// for development and unit tests. It keeps one active code per phone, a set of
// verified phones, and per-key sliding-window timestamps for rate limiting. It
// is safe for concurrent use. A persistent (Postgres) implementation replaces it
// behind the same interfaces in a later PART.

import (
	"context"
	"sync"
	"time"
)

// MemoryStore satisfies Store and RateLimiter.
type MemoryStore struct {
	mu       sync.Mutex
	codes    map[string]Code     // phone -> active code
	verified map[string]bool     // phone -> verified
	events   map[string][]time.Time // rate-limit key -> event timestamps
}

// NewMemoryStore constructs an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		codes:    make(map[string]Code),
		verified: make(map[string]bool),
		events:   make(map[string][]time.Time),
	}
}

// Save replaces the active code for code.Phone.
func (m *MemoryStore) Save(_ context.Context, code Code) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[code.Phone] = code
	return nil
}

// Get returns the active code for phone, if any.
func (m *MemoryStore) Get(_ context.Context, phone string) (Code, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[phone]
	return c, ok, nil
}

// Delete removes the active code for phone.
func (m *MemoryStore) Delete(_ context.Context, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.codes, phone)
	return nil
}

// IncrementAttempts bumps and returns the failed-attempt count.
func (m *MemoryStore) IncrementAttempts(_ context.Context, phone string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[phone]
	if !ok {
		return 0, ErrCodeNotFound
	}
	c.Attempts++
	m.codes[phone] = c
	return c.Attempts, nil
}

// MarkPhoneVerified records phone as verified.
func (m *MemoryStore) MarkPhoneVerified(_ context.Context, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verified[phone] = true
	return nil
}

// IsPhoneVerified reports whether phone has been marked verified. Test/inspection
// helper — not part of the Store interface.
func (m *MemoryStore) IsPhoneVerified(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.verified[phone]
}

// Allow implements a sliding-window rate limiter: it drops timestamps older than
// window, and admits (recording the event) only if fewer than max remain.
func (m *MemoryStore) Allow(_ context.Context, key string, max int, window time.Duration, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := now.Add(-window)
	kept := m.events[key][:0:0]
	for _, t := range m.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= max {
		m.events[key] = kept
		return false, nil
	}

	kept = append(kept, now)
	m.events[key] = kept
	return true, nil
}
