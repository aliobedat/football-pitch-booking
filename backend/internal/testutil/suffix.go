// Package testutil holds small helpers shared by DB-backed test fixtures.
package testutil

import (
	"sync/atomic"
	"time"
)

// suffixCounter is seeded once per test process from the wall clock, then
// strictly incremented. Two fixtures in the same process can therefore never
// draw the same value — unlike the previous per-call
// time.Now().UnixNano()%1_000_000 pattern, where parallel test environments
// constructed within the same microsecond collided on the users phone
// unique index (23505). Cross-process (per-package test binary) risk is
// unchanged: still wall-clock seeded, but sequences diverge immediately.
var suffixCounter atomic.Int64

func init() {
	suffixCounter.Store(time.Now().UnixNano())
}

// UniqueSuffix returns a process-unique, monotonically increasing value for
// building unique fixture identifiers (phones, slugs). Callers apply their
// own modulus to fit their format (e.g. % 1_000_000 for a %06d phone tail);
// consecutive values stay distinct under any modulus ≥ the number of calls.
func UniqueSuffix() int64 {
	return suffixCounter.Add(1)
}
