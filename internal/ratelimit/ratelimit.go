// Package ratelimit provides in-memory per-key token buckets.
// State is lost on restart and not shared between instances (spec section 8).
package ratelimit

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

const (
	sweepEvery = time.Minute
	// epsilon absorbs float rounding in refills, so a token that is due is never missed.
	epsilon = 1e-9
)

// Limiter holds one token bucket per key (client IP, phone number). It is safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	n         float64       // events per period, also the burst size
	period    time.Duration // time to refill a whole bucket
	clock     clock.Clock
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time // refills are counted from here; a clock moving backwards adds nothing
}

// Per allows n events per period for each key, with bursts of up to n. It panics on n < 1,
// a non-positive period or a nil clock: a zero window would silently allow or block everything.
func Per(n int, period time.Duration, c clock.Clock) *Limiter {
	if n < 1 || period <= 0 || c == nil {
		panic(fmt.Sprintf("ratelimit: invalid Per(%d, %v)", n, period))
	}
	return &Limiter{n: float64(n), period: period, clock: c, buckets: make(map[string]*bucket)}
}

// Allow consumes one event for key. A denied call consumes nothing and returns the time
// until the next event becomes available.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.take(key)
}

// Acquire is Allow with an undo. The event is taken up front, so parallel requests can't all
// pass a check made before their outcome is known; refund gives it back, which lets a caller
// count only failures (for example, refund after a successful login). refund may be called
// any number of times, including after a denial; only the first call of a granted event counts.
func (l *Limiter) Acquire(key string) (refund func(), ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok, wait = l.take(key); !ok {
		return func() {}, false, wait
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			// Capped at the burst: if the request outlived period/n, time may have refilled it already.
			b := l.bucket(key)
			b.tokens = min(l.n, b.tokens+1)
		})
	}, true, 0
}

// take consumes one token or reports how long until one is available. l.mu must be held.
func (l *Limiter) take(key string) (bool, time.Duration) {
	b := l.bucket(key)
	if b.tokens >= 1-epsilon {
		b.tokens = max(0, b.tokens-1)
		return true, 0
	}
	// Ceil: a client that waits exactly this long must find the token there.
	return false, time.Duration(math.Ceil((1 - b.tokens) * float64(l.period) / l.n))
}

// bucket returns the refilled bucket for key, creating a full one if needed. l.mu must be held.
func (l *Limiter) bucket(key string) *bucket {
	now := l.clock.Now()
	if now.Sub(l.lastSweep) > sweepEvery || now.Before(l.lastSweep) {
		// A full bucket behaves exactly like a fresh one, so dropping it changes nothing.
		for k, b := range l.buckets {
			if l.refill(b, now); b.tokens >= l.n-epsilon {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.n, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	return b
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	if now.After(b.last) {
		b.tokens = min(l.n, b.tokens+float64(now.Sub(b.last))/float64(l.period)*l.n)
		b.last = now
	}
}
