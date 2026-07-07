package content

import (
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
