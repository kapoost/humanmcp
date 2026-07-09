package content

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPersonaJournalAppendAndList(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))

	e1 := PersonaJournalEntry{
		At:             time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC),
		NaradaID:       "nar-abc",
		Context:        "deploy z sekretami",
		Recommendation: "dodać pre-commit hook",
		ErrorSignal:    "commit fbc123 rollback po 2h",
		Reflection:     "Pre-commit bez CI jest opcjonalny. Następnym razem: hook + CI gate.",
	}
	if err := store.Append("ghost", e1); err != nil {
		t.Fatalf("Append #1: %v", err)
	}

	e2 := PersonaJournalEntry{
		At:             time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC),
		NaradaID:       "nar-def",
		Recommendation: "użyj HMAC",
		Reflection:     "HMAC bez rotacji kluczy się starzeje.\nDodać rotation policy.",
	}
	if err := store.Append("ghost", e2); err != nil {
		t.Fatalf("Append #2: %v", err)
	}

	entries, err := store.List("ghost")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest first.
	if entries[0].NaradaID != "nar-def" {
		t.Errorf("expected newest first, got %q", entries[0].NaradaID)
	}
	if entries[1].NaradaID != "nar-abc" {
		t.Errorf("expected second oldest, got %q", entries[1].NaradaID)
	}
	if !strings.Contains(entries[1].Reflection, "Pre-commit bez CI") {
		t.Errorf("reflection body lost: %q", entries[1].Reflection)
	}
	if !strings.Contains(entries[0].Reflection, "rotation policy") {
		t.Errorf("multi-line reflection lost: %q", entries[0].Reflection)
	}
}

func TestPersonaJournalEmpty(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	entries, err := store.List("nobody")
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on empty journal, got %d", len(entries))
	}
	raw, err := store.Render("nobody")
	if err != nil || raw != "" {
		t.Errorf("expected empty render, got %q err=%v", raw, err)
	}
}

func TestPersonaJournalRejectsBadSlug(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	// Path traversal attempt.
	err := store.Append("../etc", PersonaJournalEntry{NaradaID: "x", Reflection: "y"})
	if err == nil {
		t.Error("expected error for path-traversal slug")
	}
	err = store.Append("Ghost!", PersonaJournalEntry{NaradaID: "x", Reflection: "y"})
	if err == nil {
		t.Error("expected error for uppercase/special slug")
	}
}

func TestPersonaJournalRequiresFields(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	err := store.Append("ghost", PersonaJournalEntry{Reflection: "x"})
	if err == nil {
		t.Error("expected error for missing narada_id")
	}
	err = store.Append("ghost", PersonaJournalEntry{NaradaID: "nar-1"})
	if err == nil {
		t.Error("expected error for missing reflection")
	}
}

func TestPersonaPatternsWriteRead(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	err := store.WritePatterns("ghost", PersonaPatterns{
		SynthesisedAt:      time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC),
		EntriesAtSynthesis: 12,
		Patterns:           "1. Over-trust preventive\n2. Miss deployment coordination",
		Model:              "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("WritePatterns: %v", err)
	}
	got, err := store.ReadPatterns("ghost")
	if err != nil {
		t.Fatalf("ReadPatterns: %v", err)
	}
	if got.EntriesAtSynthesis != 12 || !strings.Contains(got.Patterns, "Over-trust") {
		t.Errorf("bad round-trip: %+v", got)
	}
}

func TestPersonaPatternsMissingReturnsZero(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	got, err := store.ReadPatterns("nobody")
	if err != nil {
		t.Fatalf("missing patterns should not error: %v", err)
	}
	if got.Patterns != "" || got.EntriesAtSynthesis != 0 {
		t.Errorf("expected zero-value, got %+v", got)
	}
}

func TestNeedsSynthesisThreshold(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	// No entries → no synthesis needed.
	need, _, _ := store.NeedsSynthesis("mira")
	if need {
		t.Error("empty persona should not need synthesis")
	}
	// Add 4 entries (below threshold=5).
	for i := 0; i < 4; i++ {
		_ = store.Append("mira", PersonaJournalEntry{
			At:         time.Date(2026, 7, i+1, 10, 0, 0, 0, time.UTC),
			NaradaID:   fmt.Sprintf("nar-%d", i),
			Reflection: "x",
		})
	}
	need, count, _ := store.NeedsSynthesis("mira")
	if need {
		t.Errorf("4 entries below threshold should not trigger, got need=%v count=%d", need, count)
	}
	// 5th entry crosses the threshold.
	_ = store.Append("mira", PersonaJournalEntry{
		At:         time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
		NaradaID:   "nar-5",
		Reflection: "x",
	})
	need, count, _ = store.NeedsSynthesis("mira")
	if !need || count != 5 {
		t.Errorf("5 entries should trigger, got need=%v count=%d", need, count)
	}
	// After synthesis at 5 entries, need drops until 5 more arrive.
	_ = store.WritePatterns("mira", PersonaPatterns{
		SynthesisedAt:      time.Now().UTC(),
		EntriesAtSynthesis: 5,
		Patterns:           "1. Test pattern",
	})
	need, count, _ = store.NeedsSynthesis("mira")
	if need || count != 0 {
		t.Errorf("post-synthesis should not trigger, got need=%v count=%d", need, count)
	}
}

func TestListSlugsWithJournal(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	_ = store.Append("ghost", PersonaJournalEntry{NaradaID: "n1", Reflection: "r"})
	_ = store.Append("mira", PersonaJournalEntry{NaradaID: "n2", Reflection: "r"})
	// Write patterns for one — verifies the sidecar isn't listed as a journal.
	_ = store.WritePatterns("ghost", PersonaPatterns{
		EntriesAtSynthesis: 1, Patterns: "1. x",
	})
	got, err := store.ListSlugsWithJournal()
	if err != nil {
		t.Fatalf("ListSlugsWithJournal: %v", err)
	}
	if len(got) != 2 || got[0] != "ghost" || got[1] != "mira" {
		t.Errorf("expected [ghost mira], got %v", got)
	}
}

func TestPersonaJournalConcurrentAppend(t *testing.T) {
	store := NewPersonaJournalStore(filepath.Join(t.TempDir(), "content"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.Append("mira", PersonaJournalEntry{
				At:         time.Date(2026, 7, 7, 10, i, 0, 0, time.UTC),
				NaradaID:   "nar-" + string(rune('a'+i)),
				Reflection: "wpis",
			})
		}(i)
	}
	wg.Wait()
	entries, err := store.List("mira")
	if err != nil {
		t.Fatalf("List after concurrent: %v", err)
	}
	if len(entries) != 20 {
		t.Errorf("expected 20 entries after concurrent append, got %d (loss under mutex)", len(entries))
	}
}
