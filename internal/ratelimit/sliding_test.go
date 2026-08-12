package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a controllable clock for deterministic tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func TestBucketBasicAllowThenDeny(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Hour, 3, fc.Now)

	for i := 0; i < 3; i++ {
		allow, retry := b.Allow("key1")
		if !allow || retry != 0 {
			t.Fatalf("hit %d: want (true, 0), got (%v, %d)", i+1, allow, retry)
		}
	}
	allow, retry := b.Allow("key1")
	if allow {
		t.Fatalf("hit 4: want deny, got allow")
	}
	if retry < 1 || retry > 3601 {
		t.Fatalf("hit 4: retryAfter out of range: %d", retry)
	}
}

func TestBucketPerKeyIsolation(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Hour, 2, fc.Now)

	for i := 0; i < 2; i++ {
		if allow, _ := b.Allow("burn"); !allow {
			t.Fatalf("burn hit %d: want allow", i+1)
		}
	}
	if allow, _ := b.Allow("burn"); allow {
		t.Fatal("burn hit 3: want deny")
	}
	if allow, _ := b.Allow("other"); !allow {
		t.Fatal("other should NOT be affected by burn — per-key isolation")
	}
}

func TestBucketWindowSlides(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Minute, 2, fc.Now)

	b.Allow("k")
	b.Allow("k")
	if allow, _ := b.Allow("k"); allow {
		t.Fatal("hit 3 in same minute: want deny")
	}
	fc.Advance(61 * time.Second)
	if allow, _ := b.Allow("k"); !allow {
		t.Fatal("after window slid: want allow again")
	}
}

func TestBucketRetryAfterShrinks(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Minute, 1, fc.Now)

	b.Allow("k")
	_, retry1 := b.Allow("k")
	if retry1 < 59 || retry1 > 61 {
		t.Fatalf("first retry expected ~60s, got %d", retry1)
	}
	fc.Advance(30 * time.Second)
	_, retry2 := b.Allow("k")
	if retry2 < 29 || retry2 > 32 {
		t.Fatalf("after 30s advance retry expected ~30s, got %d", retry2)
	}
}

func TestBucketAllowWithLimitOverride(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Hour, 100, fc.Now)

	// Override to 2 — third call denied even though default limit is 100.
	b.AllowWithLimit("k", 2)
	b.AllowWithLimit("k", 2)
	if allow, _ := b.AllowWithLimit("k", 2); allow {
		t.Fatal("third call with limit=2: want deny")
	}
	// limit <= 0 falls back to default 100 — same key with default still denies
	// because we already burned 2 hits and default limit is 100 → 98 remaining.
	if allow, _ := b.AllowWithLimit("k", 0); !allow {
		t.Fatal("fallback to default limit=100: want allow")
	}
}

func TestBucketPrune(t *testing.T) {
	fc := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	b := New(time.Minute, 5, fc.Now)

	b.Allow("a")
	b.Allow("b")
	b.Allow("c")
	fc.Advance(2 * time.Minute)
	removed := b.Prune()
	if removed != 3 {
		t.Fatalf("Prune after window: want 3 removed, got %d", removed)
	}
	if allow, _ := b.Allow("a"); !allow {
		t.Fatal("post-prune Allow('a'): want allow")
	}
}

func TestBucketConcurrentAccess(t *testing.T) {
	b := New(time.Hour, 1000, nil)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				b.Allow("shared")
			}
		}()
	}
	wg.Wait()
	// 1000 exact hits → next Allow should deny.
	if allow, _ := b.Allow("shared"); allow {
		t.Fatal("after 1000 concurrent hits: want deny on next")
	}
}
