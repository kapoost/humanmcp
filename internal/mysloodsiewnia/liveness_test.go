package mysloodsiewnia

import (
	"testing"
	"time"
)

func TestLivenessStartsUnreachable(t *testing.T) {
	l := New()
	if got := l.Get().Status; got != StatusUnreachable {
		t.Fatalf("fresh liveness should be %q, got %q", StatusUnreachable, got)
	}
	if l.IsOnline() {
		t.Fatal("fresh liveness must not report online")
	}
}

func TestLivenessOnlineWithinTTL(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	l := NewWith(30*time.Second, func() time.Time { return now })
	l.Update("abc123", now, now, true)
	snap := l.Get()
	if snap.Status != StatusOnline {
		t.Fatalf("want online, got %q", snap.Status)
	}
	if snap.CommitSHA != "abc123" {
		t.Fatalf("commit sha lost")
	}
}

func TestLivenessFlipsToUnreachableAfterTTL(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := base
	l := NewWith(30*time.Second, func() time.Time { return now })
	l.Update("abc", base, base, true)
	// Advance clock past TTL — heartbeat is now stale.
	now = base.Add(31 * time.Second)
	if got := l.Get().Status; got != StatusUnreachable {
		t.Fatalf("stale heartbeat should flip to %q, got %q", StatusUnreachable, got)
	}
}

func TestLivenessDegradedWhenFTSUnhealthy(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	l := NewWith(30*time.Second, func() time.Time { return now })
	l.Update("abc", now, now, false)
	snap := l.Get()
	if snap.Status != StatusDegraded {
		t.Fatalf("fts_healthy=false with fresh heartbeat should be %q, got %q", StatusDegraded, snap.Status)
	}
	if snap.DegradedReason != "fts_rebuilding" {
		t.Fatalf("expected degraded reason fts_rebuilding, got %q", snap.DegradedReason)
	}
	if l.IsOnline() {
		t.Fatal("degraded must not report online")
	}
}
