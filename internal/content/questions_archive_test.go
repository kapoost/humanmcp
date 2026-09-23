package content

import (
	"path/filepath"
	"testing"
)

// Do września 2026 magazyn pytań miał wyłącznie Create/Answer/MarkFetched —
// nie było jak niczego zdjąć z kolejki. Dlatego „test" i „hello" z lipca
// wisiały miesiącami w sekcji „pending — need your answer", rozcieńczając to,
// co naprawdę czekało na odpowiedź.
//
// Archiwizacja jest PRZENIESIENIEM, nie usunięciem: kolejka bywa jedynym
// śladem, że ktoś zapytał, więc sprzątanie musi dać się cofnąć.
func TestArchiveRemovesFromQueueButKeepsQuestion(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))

	keep, err := store.Create("researcher", "", "Realne pytanie")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	noise, err := store.Create("", "", "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(store.List()) != 2 {
		t.Fatalf("setup: %d pytań, oczekiwano 2", len(store.List()))
	}

	if err := store.Archive(noise.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	left := store.List()
	if len(left) != 1 || left[0].ID != keep.ID {
		t.Errorf("po archiwizacji w kolejce: %v, oczekiwano tylko %s", ids(left), keep.ID)
	}
	arch := store.ListArchived()
	if len(arch) != 1 || arch[0].ID != noise.ID {
		t.Fatalf("archiwum: %v, oczekiwano %s", ids(arch), noise.ID)
	}
	if arch[0].Question != "test" {
		t.Errorf("treść zgubiona przy przenosinach: %q", arch[0].Question)
	}
}

func TestUnarchiveRestoresToQueue(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	q, err := store.Create("explorer-agent", "", "Czy da się cofnąć?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Archive(q.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := store.Unarchive(q.ID); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if len(store.List()) != 1 {
		t.Errorf("po cofnięciu w kolejce %d pytań, oczekiwano 1", len(store.List()))
	}
	if len(store.ListArchived()) != 0 {
		t.Errorf("po cofnięciu w archiwum zostało %d", len(store.ListArchived()))
	}
}

func TestArchiveUnknownIDIsAnError(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	if err := store.Archive("nie-ma-takiego"); err == nil {
		t.Error("Archive nieistniejącego ID przeszło bez błędu — cicha porażka sprzątania")
	}
	if err := store.Unarchive("nie-ma-takiego"); err == nil {
		t.Error("Unarchive nieistniejącego ID przeszło bez błędu")
	}
}

// Zarchiwizowane pytanie nie może wracać przez re-ask: agent dostałby
// z powrotem coś, co właściciel świadomie zdjął z kolejki.
func TestArchivedQuestionIsNotMatchedByReAsk(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	q, err := store.Create("noisy-agent", "", "hello")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Archive(q.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// Zmiana wobec pierwotnego założenia: zarchiwizowana ODPOWIEDŹ ma wracać.
	// Archiwum zdejmuje pytanie z kolejki właściciela, ale nie unieważnia
	// odpowiedzi, którą już napisał. Inaczej zarchiwizowanie odpowiedzianego
	// pytania cicho psuje jego dostarczenie — agent powtarza pytanie
	// i dostaje duplikat zamiast gotowej odpowiedzi.
	if _, ok := store.FindLatestByAsker("noisy-agent", "hello", ""); !ok {
		t.Error("zarchiwizowane pytanie nie jest już odnajdywane — odpowiedź przepada")
	}
}

func ids(qs []Question) []string {
	out := make([]string, 0, len(qs))
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}
