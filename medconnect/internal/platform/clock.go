// Package platform holds small cross-cutting primitives that the rest of the
// service depends on through interfaces, so behaviour is deterministic in tests.
package platform

import (
	"sync"
	"time"
)

// Clock reports the current time. Production uses SystemClock; tests use
// FakeClock so time-dependent logic (expiry, point-in-time) is deterministic.
type Clock interface {
	Now() time.Time
}

// SystemClock returns the real wall-clock time.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// FakeClock is a controllable Clock for tests.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFakeClock returns a FakeClock fixed at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t} }

// Now returns the clock's current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Set moves the clock to t.
func (f *FakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}
