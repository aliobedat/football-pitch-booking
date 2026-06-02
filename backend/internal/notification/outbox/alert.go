package outbox

import (
	"log"
	"sync"
	"time"
)

// FailureMonitor watches the dispatch outcome stream and emits a structured
// alert when the failure rate over a sliding window crosses a threshold. It is
// the "alerting for elevated failure rates" half of PART 6: cheap, in-process,
// and dependency-free (no external metrics backend is introduced).
//
// It is intentionally a detector, not a metrics store — it samples recent
// outcomes, computes a ratio, and logs once per window when the ratio is
// elevated. Blocked (consent/validation) outcomes are NOT recorded here: they
// are policy decisions, not delivery failures, and must not trip the alarm.
type FailureMonitor struct {
	window     time.Duration
	threshold  float64 // failure ratio in [0,1] that triggers an alert
	minSamples int     // suppress alerts until at least this many samples exist

	mu          sync.Mutex
	outcomes    []sample // recent outcomes within the window
	now         func() time.Time
	lastAlertAt time.Time
	alertFn     func(failures, total int, ratio float64)
}

type sample struct {
	at     time.Time
	failed bool
}

// MonitorOption configures a FailureMonitor.
type MonitorOption func(*FailureMonitor)

// WithMonitorClock overrides the time source (tests inject a fake clock).
func WithMonitorClock(now func() time.Time) MonitorOption {
	return func(m *FailureMonitor) {
		if now != nil {
			m.now = now
		}
	}
}

// WithAlertFunc overrides the alert sink. The default logs a structured warning;
// tests capture the call to assert the alarm fired.
func WithAlertFunc(fn func(failures, total int, ratio float64)) MonitorOption {
	return func(m *FailureMonitor) {
		if fn != nil {
			m.alertFn = fn
		}
	}
}

// NewFailureMonitor builds a monitor that alerts when, within window and given
// at least minSamples observations, the failure ratio reaches threshold.
func NewFailureMonitor(window time.Duration, threshold float64, minSamples int, opts ...MonitorOption) *FailureMonitor {
	m := &FailureMonitor{
		window:     window,
		threshold:  threshold,
		minSamples: minSamples,
		now:        time.Now,
	}
	m.alertFn = m.defaultAlert
	for _, o := range opts {
		o(m)
	}
	return m
}

// Record observes one dispatch outcome and, if the windowed failure ratio is now
// elevated, fires the alert (at most once per window).
func (m *FailureMonitor) Record(failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	cutoff := now.Add(-m.window)

	// Drop samples that have aged out of the window.
	kept := m.outcomes[:0]
	for _, s := range m.outcomes {
		if s.at.After(cutoff) {
			kept = append(kept, s)
		}
	}
	m.outcomes = kept
	m.outcomes = append(m.outcomes, sample{at: now, failed: failed})

	total := len(m.outcomes)
	if total < m.minSamples {
		return
	}
	failures := 0
	for _, s := range m.outcomes {
		if s.failed {
			failures++
		}
	}
	ratio := float64(failures) / float64(total)
	if ratio < m.threshold {
		return
	}
	// Rate-limit alerts to one per window so a sustained outage does not spam.
	if !m.lastAlertAt.IsZero() && now.Sub(m.lastAlertAt) < m.window {
		return
	}
	m.lastAlertAt = now
	m.alertFn(failures, total, ratio)
}

func (m *FailureMonitor) defaultAlert(failures, total int, ratio float64) {
	log.Printf("[ALERT] notification delivery failure rate elevated: %d/%d failed (%.0f%%) over the last %s",
		failures, total, ratio*100, m.window)
}
