// Package mysloodsiewnia holds shared state for the humanmcp ↔ mysłoodsiewnia bridge:
// a liveness store (heartbeat freshness + FTS health) and an in-memory op queue.
// The vault pulls pending ops via HTTP, executes locally, and posts back — see bridge.go.
package mysloodsiewnia

import (
	"sync"
	"time"
)

// DefaultTTL is how long a heartbeat stays "fresh" before Status() flips to unreachable.
// 30s matches the plan; vault heartbeats every 20s so we have one grace period.
const DefaultTTL = 30 * time.Second

// Status categorises the vault's current reachability from humanmcp's POV.
type Status string

const (
	StatusOnline      Status = "online"
	StatusDegraded    Status = "degraded"
	StatusUnreachable Status = "offline"
)

// Snapshot is what tools and templates read — a point-in-time copy so callers
// never see torn writes and never hold the lock.
type Snapshot struct {
	Status             Status    `json:"status"`
	LastSeen           time.Time `json:"last_seen"`
	CommitSHA          string    `json:"commit_sha"`
	PersonasUpdatedAt  time.Time `json:"personas_updated_at"`
	SkillsUpdatedAt    time.Time `json:"skills_updated_at"`
	FTSHealthy         bool      `json:"fts_healthy"`
	DegradedReason     string    `json:"degraded_reason,omitempty"`
}

// Liveness tracks the last heartbeat received from the vault.
// Safe for concurrent readers + a single writer (the /heartbeat handler).
type Liveness struct {
	mu   sync.RWMutex
	ttl  time.Duration
	now  func() time.Time // injected for tests
	data struct {
		lastSeen          time.Time
		commitSHA         string
		personasUpdatedAt time.Time
		skillsUpdatedAt   time.Time
		ftsHealthy        bool
	}
}

// New builds a Liveness with the default TTL and real clock.
func New() *Liveness {
	return NewWith(DefaultTTL, time.Now)
}

// NewWith is the test seam — inject a custom TTL and clock.
func NewWith(ttl time.Duration, now func() time.Time) *Liveness {
	if now == nil {
		now = time.Now
	}
	return &Liveness{ttl: ttl, now: now}
}

// Update records a fresh heartbeat. Called by the /heartbeat HTTP handler.
func (l *Liveness) Update(commitSHA string, personasUpdatedAt, skillsUpdatedAt time.Time, ftsHealthy bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data.lastSeen = l.now()
	l.data.commitSHA = commitSHA
	l.data.personasUpdatedAt = personasUpdatedAt
	l.data.skillsUpdatedAt = skillsUpdatedAt
	l.data.ftsHealthy = ftsHealthy
}

// Get returns a snapshot with the derived Status field filled in.
// This is what MCP tools call before deciding online/offline/degraded.
func (l *Liveness) Get() Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snap := Snapshot{
		LastSeen:          l.data.lastSeen,
		CommitSHA:         l.data.commitSHA,
		PersonasUpdatedAt: l.data.personasUpdatedAt,
		SkillsUpdatedAt:   l.data.skillsUpdatedAt,
		FTSHealthy:        l.data.ftsHealthy,
	}
	// Never received a heartbeat? Unreachable.
	if l.data.lastSeen.IsZero() {
		snap.Status = StatusUnreachable
		return snap
	}
	// Heartbeat too old → unreachable. (Mira: distinguish this window from
	// online-but-degraded — a freshly-restarted vault has fresh heartbeat but
	// FTS may still be rebuilding, and /query returns empty even though
	// /healthz is 200.)
	if l.now().Sub(l.data.lastSeen) > l.ttl {
		snap.Status = StatusUnreachable
		return snap
	}
	if !l.data.ftsHealthy {
		snap.Status = StatusDegraded
		snap.DegradedReason = "fts_rebuilding"
		return snap
	}
	snap.Status = StatusOnline
	return snap
}

// IsOnline is a convenience for templates — returns true only when Status == online.
// Degraded and unreachable both render as OFFLINE in the MC dashboard because
// they both mean "don't route agent operations here right now".
func (l *Liveness) IsOnline() bool {
	return l.Get().Status == StatusOnline
}
