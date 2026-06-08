package content

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewMemoryStore(dir)

	code := "jeszcze polska nie zginęła"
	m, err := store.Save(code, "claude-code", "Łukasz prefers terse responses", []string{"prefs"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if m.ID == "" || !strings.HasPrefix(m.ID, "mem-") {
		t.Errorf("ID shape unexpected: %q", m.ID)
	}

	// Fresh store reads the same file
	store2 := NewMemoryStore(dir)
	mems, err := store2.Recall(code, "", 0)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(mems))
	}
	if mems[0].Body != "Łukasz prefers terse responses" {
		t.Errorf("body lost: %q", mems[0].Body)
	}
	if mems[0].From != "claude-code" {
		t.Errorf("from lost: %q", mems[0].From)
	}
	if len(mems[0].Tags) != 1 || mems[0].Tags[0] != "prefs" {
		t.Errorf("tags lost: %v", mems[0].Tags)
	}
}

func TestMemoryIsolatedPerCode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewMemoryStore(dir)

	_, _ = store.Save("code-A", "", "secret A", nil)
	_, _ = store.Save("code-B", "", "secret B", nil)

	a, _ := store.Recall("code-A", "", 0)
	b, _ := store.Recall("code-B", "", 0)
	if len(a) != 1 || a[0].Body != "secret A" {
		t.Errorf("code-A recall returned wrong content: %v", a)
	}
	if len(b) != 1 || b[0].Body != "secret B" {
		t.Errorf("code-B recall returned wrong content: %v", b)
	}
	// Cross-code isolation
	cross, _ := store.Recall("code-A", "secret B", 0)
	if len(cross) != 0 {
		t.Errorf("substring query leaked across codes: %v", cross)
	}
}

func TestMemoryQuerySubstring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewMemoryStore(dir)
	code := "test-code"
	_, _ = store.Save(code, "", "Łukasz sails an MX-5", []string{"car"})
	_, _ = store.Save(code, "", "Mira recommended edge-first design", nil)
	_, _ = store.Save(code, "", "Likes Polish poetry", []string{"prefs", "polish"})

	hits, _ := store.Recall(code, "edge", 0)
	if len(hits) != 1 || !strings.Contains(hits[0].Body, "Mira") {
		t.Errorf("substring query failed: %+v", hits)
	}
	tagHits, _ := store.Recall(code, "polish", 0)
	if len(tagHits) != 1 {
		t.Errorf("tag-substring query failed: %+v", tagHits)
	}
}

func TestMemoryRejectsOversizeAndEmpty(t *testing.T) {
	store := NewMemoryStore(filepath.Join(t.TempDir(), "content"))
	if _, err := store.Save("c", "", "", nil); err == nil {
		t.Error("empty body should error")
	}
	if _, err := store.Save("", "", "non-empty", nil); err == nil {
		t.Error("empty code should error")
	}
	big := strings.Repeat("x", MaxMemoryBytes+1)
	if _, err := store.Save("c", "", big, nil); err == nil {
		t.Error("oversized body should error")
	}
}
