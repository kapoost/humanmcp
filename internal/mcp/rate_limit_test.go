package mcp

import (
	"testing"
	"time"
)

// TestCheckBucketRate covers the sliding-window rate limiter used by
// ask_human (5/hr/IP) and fetch_answer (30/hr/IP). Per-IP buckets stay
// isolated; entries older than the window get pruned silently.
func TestCheckBucketRate(t *testing.T) {
	h := &Handler{
		askHumanLog: map[string][]time.Time{},
	}

	ip := "203.0.113.1"
	for i := 1; i <= 5; i++ {
		if !h.checkBucketRate(ip, h.askHumanLog, time.Hour, 5) {
			t.Fatalf("call %d should be allowed, was blocked", i)
		}
	}
	if h.checkBucketRate(ip, h.askHumanLog, time.Hour, 5) {
		t.Error("6th call should be blocked, was allowed")
	}

	// A different IP keeps its own bucket.
	other := "203.0.113.2"
	if !h.checkBucketRate(other, h.askHumanLog, time.Hour, 5) {
		t.Error("different IP should not share the bucket")
	}
}

// TestCheckBucketRate_PrunesOldEntries injects stale timestamps and confirms
// they don't count against the current window.
func TestCheckBucketRate_PrunesOldEntries(t *testing.T) {
	h := &Handler{askHumanLog: map[string][]time.Time{}}
	ip := "198.51.100.1"
	// Pre-load with 5 stale entries (well outside a 1-hour window).
	stale := time.Now().Add(-2 * time.Hour)
	h.askHumanLog[ip] = []time.Time{stale, stale, stale, stale, stale}

	// Next call should still be allowed because the stale entries get pruned.
	if !h.checkBucketRate(ip, h.askHumanLog, time.Hour, 5) {
		t.Error("call with stale entries should be allowed after prune")
	}
}
