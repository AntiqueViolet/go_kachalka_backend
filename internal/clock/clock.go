// Package clock abstracts the current time so tests can control it.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time in UTC with microsecond precision.
type Clock interface {
	Now() time.Time
}

// Real is the wall clock. Times are UTC with microsecond precision to match Postgres timestamptz.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Fake is a manually advanced clock for tests. It is safe for concurrent use; create it with NewFake.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a clock stopped at t, normalized the same way as Real.
func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC().Truncate(time.Microsecond)} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock by d, which may be negative.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
