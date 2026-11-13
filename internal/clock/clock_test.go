package clock

import (
	"sync"
	"testing"
	"time"
)

var (
	_ Clock = Real{}
	_ Clock = (*Fake)(nil)
)

func TestRealTracksWallClockInUTCMicroseconds(t *testing.T) {
	for range 100 {
		before := time.Now().Truncate(time.Microsecond)
		got := Real{}.Now()
		after := time.Now()
		if got.Location() != time.UTC {
			t.Fatalf("location = %v, want UTC", got.Location())
		}
		if got.Nanosecond()%1000 != 0 {
			t.Fatalf("nanoseconds %d not truncated to microseconds", got.Nanosecond())
		}
		if got.Before(before) || got.After(after) {
			t.Fatalf("Now() = %v, want within [%v, %v]", got, before, after)
		}
	}
}

func TestNewFakeNormalizesToUTCMicroseconds(t *testing.T) {
	in := time.Date(2026, 1, 1, 15, 0, 0, 123_456_789, time.FixedZone("UTC+3", 3*60*60))
	got := NewFake(in).Now()
	if want := time.Date(2026, 1, 1, 12, 0, 0, 123_456_000, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("Now() = %v, want %v in UTC", got, want)
	}
}

func TestFakeAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := NewFake(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() before Advance = %v, want %v", got, start)
	}
	c.Advance(90 * time.Second)
	c.Advance(-30 * time.Second)
	if got, want := c.Now(), start.Add(60*time.Second); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}

func TestFakeConcurrentUse(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(start)
	const workers, steps = 8, 1000
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range steps {
				c.Advance(time.Second)
			}
		}()
		go func() {
			defer wg.Done()
			for range steps {
				_ = c.Now()
			}
		}()
	}
	wg.Wait()
	if got, want := c.Now(), start.Add(workers*steps*time.Second); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}
