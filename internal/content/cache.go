package content

import (
	"sync"
	"time"
)

// cache is a simple TTL in-memory cache for expensive disk reads.
// Used by Store, BlobStore, StatStore to avoid re-reading files on every request.

type cacheEntry[T any] struct {
	value   T
	loadedAt time.Time
}

type Cache[T any] struct {
	mu    sync.RWMutex
	entry *cacheEntry[T]
	ttl   time.Duration
}

func NewCache[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl}
}

func (c *Cache[T]) Get() (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entry == nil {
		var zero T
		return zero, false
	}
	if time.Since(c.entry.loadedAt) > c.ttl {
		var zero T
		return zero, false
	}
	return c.entry.value, true
}

func (c *Cache[T]) Set(v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = &cacheEntry[T]{value: v, loadedAt: time.Now()}
}

func (c *Cache[T]) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = nil
}
