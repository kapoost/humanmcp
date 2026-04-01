package content

import (
	"testing"
	"time"
)

func TestCacheGetEmpty(t *testing.T) {
	c := NewCache[string](time.Second)
	_, ok := c.Get()
	if ok {
		t.Error("empty cache should return ok=false")
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := NewCache[string](time.Second)
	c.Set("hello")
	v, ok := c.Get()
	if !ok { t.Error("should hit after Set") }
	if v != "hello" { t.Errorf("got %q", v) }
}

func TestCacheExpires(t *testing.T) {
	c := NewCache[int](10 * time.Millisecond)
	c.Set(42)
	time.Sleep(15 * time.Millisecond)
	_, ok := c.Get()
	if ok { t.Error("should miss after TTL") }
}

func TestCacheInvalidate(t *testing.T) {
	c := NewCache[string](time.Minute)
	c.Set("data")
	c.Invalidate()
	_, ok := c.Get()
	if ok { t.Error("should miss after Invalidate") }
}

func TestCacheResetAfterInvalidate(t *testing.T) {
	c := NewCache[int](time.Minute)
	c.Set(1)
	c.Invalidate()
	c.Set(2)
	v, ok := c.Get()
	if !ok { t.Error("should hit after re-Set") }
	if v != 2 { t.Errorf("got %d", v) }
}

func TestCacheSlice(t *testing.T) {
	c := NewCache[[]*Piece](time.Second)
	pieces := []*Piece{{Slug: "a"}, {Slug: "b"}}
	c.Set(pieces)
	got, ok := c.Get()
	if !ok { t.Error("should hit") }
	if len(got) != 2 { t.Errorf("got %d pieces", len(got)) }
}
