package outbox

import (
	"testing"
	"time"
)

// alertCapture records each alert the monitor emits.
type alertCapture struct {
	count        int
	lastFailures int
	lastTotal    int
	lastRatio    float64
}

func (a *alertCapture) fn(failures, total int, ratio float64) {
	a.count++
	a.lastFailures, a.lastTotal, a.lastRatio = failures, total, ratio
}

func TestFailureMonitor_AlertsWhenThresholdCrossed(t *testing.T) {
	now := time.Now()
	cap := &alertCapture{}
	m := NewFailureMonitor(time.Hour, 0.5, 4,
		WithMonitorClock(fixedClock(now)),
		WithAlertFunc(cap.fn),
	)

	// 3 samples is below minSamples → no alert yet even though all failed.
	m.Record(true)
	m.Record(true)
	m.Record(true)
	if cap.count != 0 {
		t.Fatalf("alerted after %d samples, want suppressed below minSamples=4", cap.count)
	}

	// 4th failed sample: 4/4 = 100% ≥ 50% and minSamples met → alert.
	m.Record(true)
	if cap.count != 1 {
		t.Fatalf("alerts = %d, want 1 once the threshold is crossed", cap.count)
	}
	if cap.lastFailures != 4 || cap.lastTotal != 4 || cap.lastRatio != 1.0 {
		t.Errorf("alert payload = (%d/%d, %.2f), want (4/4, 1.00)", cap.lastFailures, cap.lastTotal, cap.lastRatio)
	}
}

func TestFailureMonitor_BelowThresholdDoesNotAlert(t *testing.T) {
	now := time.Now()
	cap := &alertCapture{}
	m := NewFailureMonitor(time.Hour, 0.75, 4,
		WithMonitorClock(fixedClock(now)),
		WithAlertFunc(cap.fn),
	)
	// 2 of 4 failed = 50% < 75% threshold.
	m.Record(true)
	m.Record(false)
	m.Record(true)
	m.Record(false)
	if cap.count != 0 {
		t.Errorf("alerts = %d, want 0 (below threshold)", cap.count)
	}
}

func TestFailureMonitor_RateLimitedPerWindow(t *testing.T) {
	now := time.Now()
	cap := &alertCapture{}
	clock := now
	m := NewFailureMonitor(time.Hour, 0.5, 2,
		WithMonitorClock(func() time.Time { return clock }),
		WithAlertFunc(cap.fn),
	)

	m.Record(true)
	m.Record(true) // crosses threshold → first alert
	if cap.count != 1 {
		t.Fatalf("alerts = %d, want 1", cap.count)
	}

	// Still inside the same window: a sustained outage must not re-alert.
	clock = now.Add(30 * time.Minute)
	m.Record(true)
	if cap.count != 1 {
		t.Fatalf("alerts = %d, want 1 (rate-limited within window)", cap.count)
	}

	// Past one full window from the last alert: alerting is allowed again.
	clock = now.Add(2 * time.Hour)
	m.Record(true)
	m.Record(true)
	if cap.count != 2 {
		t.Errorf("alerts = %d, want 2 (window elapsed, re-alert)", cap.count)
	}
}

func TestFailureMonitor_EvictsAgedSamples(t *testing.T) {
	now := time.Now()
	cap := &alertCapture{}
	clock := now
	m := NewFailureMonitor(time.Hour, 0.99, 2,
		WithMonitorClock(func() time.Time { return clock }),
		WithAlertFunc(cap.fn),
	)

	// Two old failures, then they age out of the window before two fresh
	// successes — so the live ratio is 0%, well under threshold, no alert.
	m.Record(true)
	m.Record(true)
	cap.count = 0 // ignore the alert from the initial burst

	clock = now.Add(2 * time.Hour) // both prior samples are now stale
	m.Record(false)
	m.Record(false)
	if cap.count != 0 {
		t.Errorf("alerts = %d, want 0 (aged failures must be evicted from the window)", cap.count)
	}
}
