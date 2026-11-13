package ratelimit

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

func newClock() *clock.Fake { return clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) }

func TestAllowBurstThenExactWait(t *testing.T) {
	c := newClock()
	l := Per(2, time.Minute, c)
	for i := range 2 {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("call %d denied", i+1)
		}
	}
	if ok, wait := l.Allow("ip"); ok || wait != 30*time.Second {
		t.Fatalf("third call: ok=%v wait=%v, want denied with exactly 30s", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys must be independent")
	}
	c.Advance(29 * time.Second)
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("allowed before the next token")
	}
	c.Advance(time.Second)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("denied after refill")
	}
}

func TestAcquireRefundCountsOnlyKeptEvents(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c) // failed logins per phone
	for range 10 {
		refund, ok, _ := l.Acquire("phone")
		if !ok {
			t.Fatal("refunded events must not count")
		}
		refund() // a successful login
	}
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("failure denied before the limit")
		}
	}
	_, ok, wait := l.Acquire("phone")
	if ok || wait != 3*time.Minute {
		t.Fatalf("sixth failure: ok=%v wait=%v, want denied with exactly 3m", ok, wait)
	}
	c.Advance(3 * time.Minute)
	if _, ok, _ := l.Acquire("phone"); !ok {
		t.Fatal("denied after one event refilled")
	}
}

// Regression: refunds made after any time had passed (a real password check) were silently lost.
func TestRefundWorksAfterTimePassed(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	for range 20 {
		refund, ok, _ := l.Acquire("phone")
		if !ok {
			t.Fatal("refunded events must not count")
		}
		c.Advance(150 * time.Millisecond) // the password check
		refund()
		refund() // a second call must not give back more than was taken
	}
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("burst not intact after refunds")
		}
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("refunds raised the bucket above its burst")
	}
}

func TestDoubleRefundDoesNotOverCredit(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	var refund func()
	for range 5 {
		refund, _, _ = l.Acquire("phone")
	}
	c.Advance(time.Millisecond)
	refund()
	refund()
	if _, ok, _ := l.Acquire("phone"); !ok {
		t.Fatal("refunded event not available")
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("a double refund gave back two events")
	}
}

func TestAcquireIsAtomicUnderConcurrency(t *testing.T) {
	l := Per(5, 15*time.Minute, newClock())
	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, _ := l.Acquire("phone"); ok {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 5 {
		t.Fatalf("%d parallel attempts granted, want exactly 5", granted.Load())
	}
}

func TestAllowIsAtomicUnderConcurrency(t *testing.T) {
	l := Per(100, time.Minute, newClock())
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if ok, _ := l.Allow("k"); ok {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 100 {
		t.Fatalf("%d events allowed, want exactly 100", allowed.Load())
	}
}

func TestSweepDropsOnlyFullBuckets(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	for range 5 {
		l.Allow("drained")
	}
	l.Allow("idle")
	c.Advance(4 * time.Minute) // "idle" is full again, "drained" has about 1.3 events
	l.Allow("trigger")         // more than sweepEvery since the last sweep
	if _, ok := l.buckets["idle"]; ok {
		t.Fatal("full bucket not swept")
	}
	if _, ok := l.buckets["drained"]; !ok {
		t.Fatal("sweep reset a bucket that was still limiting")
	}
}

func TestClockGoingBackwardsDoesNotRecredit(t *testing.T) {
	c := newClock()
	l := Per(1, time.Hour, c)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first call denied")
	}
	c.Advance(-2 * time.Hour)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("allowed after the clock jumped back")
	}
	c.Advance(2 * time.Hour)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("allowed after the clock returned")
	}
	c.Advance(time.Hour)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("denied after a real hour")
	}
}

func TestPerRejectsBadArguments(t *testing.T) {
	cases := map[string]func(){
		"zero events":     func() { Per(0, time.Minute, newClock()) },
		"negative events": func() { Per(-1, time.Minute, newClock()) },
		"zero period":     func() { Per(5, 0, newClock()) },
		"negative period": func() { Per(5, -time.Minute, newClock()) },
		"nil clock":       func() { Per(5, time.Minute, nil) },
	}
	for name, f := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

// Many keys, fresh keys and a moving clock exercise the map and the refund path under -race.
func TestConcurrentAcquireRefundManyKeys(t *testing.T) {
	c := newClock()
	l := Per(5, time.Minute, c)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				if refund, ok, _ := l.Acquire(strconv.Itoa((i + j) % 7)); ok {
					refund()
				}
				l.Allow(strconv.Itoa(i*100 + j))
				c.Advance(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}

// A client that sleeps exactly the advertised wait must pass, and a denial never advertises 0.
func TestWaitIsHonest(t *testing.T) {
	periods := []time.Duration{time.Second, time.Minute, 15 * time.Minute, time.Hour + 7*time.Minute + 13*time.Second + 17*time.Millisecond}
	for _, p := range periods {
		for _, n := range []int{1, 3, 7, 12, 14, 17, 22, 60, 1000} {
			for _, frac := range []time.Duration{0, 3, 2} { // refill none, 1/3 or 1/2 of a token first
				c := newClock()
				l := Per(n, p, c)
				for range n {
					l.Allow("k")
				}
				if frac != 0 {
					c.Advance(p / time.Duration(n) / frac)
				}
				ok, wait := l.Allow("k")
				if ok || wait <= 0 {
					t.Fatalf("Per(%d, %v): ok=%v wait=%v", n, p, ok, wait)
				}
				c.Advance(wait)
				if ok, w := l.Allow("k"); !ok {
					t.Fatalf("Per(%d, %v): denied (wait %v) after sleeping the advertised %v", n, p, w, wait)
				}
			}
		}
	}
}

// Rates where float rounding lands just below a whole token; epsilon must absorb it.
func TestAwkwardRates(t *testing.T) {
	for _, r := range []struct {
		n int
		p time.Duration
	}{
		{394, 3544818 * time.Microsecond},
		{850, 3332850 * time.Microsecond},
		{841, 5230179 * time.Microsecond},
		{1854, 774972 * time.Millisecond},
		{1753, 14225595 * time.Microsecond},
	} {
		c := newClock()
		l := Per(r.n, r.p, c)
		for range r.n {
			l.Allow("k")
		}
		_, wait := l.Allow("k")
		c.Advance(wait)
		if ok, _ := l.Allow("k"); !ok {
			t.Errorf("Per(%d, %v): denied after the advertised wait %v", r.n, r.p, wait)
		}
	}
}

func TestRefillIsCappedAtBurst(t *testing.T) {
	c := newClock()
	l := Per(5, time.Second, c)
	for range 5 {
		l.Allow("k")
	}
	c.Advance(30 * time.Second) // shorter than sweepEvery, so a sweep can't hide a missing cap
	granted := 0
	for range 10 {
		if ok, _ := l.Allow("k"); ok {
			granted++
		}
	}
	if granted != 5 {
		t.Fatalf("%d events after a long idle period, want the burst of 5", granted)
	}
}

func TestLateRefundIsCappedAtBurst(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	refund, _, _ := l.Acquire("phone")
	c.Advance(15 * time.Minute) // time alone has restored the event
	refund()
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("burst not available")
		}
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("a late refund raised the bucket above its burst")
	}
}

func TestSweepResumesAfterClockStepsBack(t *testing.T) {
	c := newClock()
	l := Per(1, time.Minute, c)
	l.Allow("a")
	c.Advance(-24 * time.Hour)
	l.Allow("e")
	c.Advance(2 * time.Minute)
	l.Allow("f") // "e" is full again, so this call must sweep it
	if _, ok := l.buckets["e"]; ok {
		t.Fatal("sweeping stalled after the clock stepped back")
	}
}
