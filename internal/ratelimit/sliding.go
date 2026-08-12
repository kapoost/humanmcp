// Package ratelimit provides an in-memory sliding-window rate limiter keyed
// by an arbitrary string. Consolidates seven near-identical implementations
// that used to live inline in internal/mcp/handler.go and internal/web/
// handler.go (checkRateLimit, checkBucketRate, checkAskHumanRateLimit,
// checkFetchAnswerRateLimit, CheckNaradaRateLimit, CheckNaradaFetchRateLimit,
// CheckFriendTokenRateLimit, checkContactRateLimit).
//
// Semantics: at check time, prune entries older than `now - window`, then
// deny if the surviving count is >= limit; else record `now` and allow.
// retry-after is the number of seconds until the oldest surviving entry
// falls off the window edge — always >= 1 second on deny.
package ratelimit

import (
	"sync"
	"time"
)

// Clock is the wall-clock source. Defaults to time.Now; tests inject a
// fake clock to advance time deterministically.
type Clock func() time.Time

// Bucket is an in-memory sliding-window rate limiter keyed by an arbitrary
// string (IP, tokenID, session ID, etc). Safe for concurrent use.
type Bucket struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	now    Clock
	log    map[string][]time.Time
}

// New builds a Bucket. `clock` may be nil (defaults to time.Now).
func New(window time.Duration, limit int, clock Clock) *Bucket {
	if clock == nil {
		clock = time.Now
	}
	return &Bucket{
		window: window,
		limit:  limit,
		now:    clock,
		log:    make(map[string][]time.Time),
	}
}

// Allow checks whether `key` has budget in the current window. On allow,
// records the hit. Returns (true, 0) on allow; (false, retryAfterSecs) on
// deny where retryAfterSecs is when the oldest hit falls off the window.
func (b *Bucket) Allow(key string) (allowed bool, retryAfterSecs int) {
	return b.allow(key, b.limit)
}

// AllowWithLimit is Allow with a per-call limit override — used by friend
// tokens where each token has its own rate_limit_per_hour. `limit <= 0`
// falls back to the bucket's default limit.
func (b *Bucket) AllowWithLimit(key string, limit int) (allowed bool, retryAfterSecs int) {
	if limit <= 0 {
		limit = b.limit
	}
	return b.allow(key, limit)
}

func (b *Bucket) allow(key string, limit int) (bool, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	cutoff := now.Add(-b.window)
	kept := b.log[key][:0]
	for _, t := range b.log[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		b.log[key] = kept
		oldest := kept[0]
		retry := int(oldest.Add(b.window).Sub(now).Seconds()) + 1
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	kept = append(kept, now)
	b.log[key] = kept
	return true, 0
}

// Prune drops entries older than the window for keys that haven't been
// touched recently. Called by the parent handler's cleanup loop to bound
// memory when a bucket has seen many one-shot keys (e.g. IPs). Optional —
// buckets that see the same keys repeatedly self-prune via Allow.
func (b *Bucket) Prune() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	cutoff := now.Add(-b.window)
	removed := 0
	for key, times := range b.log {
		kept := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(b.log, key)
			removed++
		} else if len(kept) != len(times) {
			b.log[key] = kept
		}
	}
	return removed
}
