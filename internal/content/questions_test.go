package content

import (
	"path/filepath"
	"testing"
)

// TestQuestionLifecycle exercises the full Create → Get → Answer →
// MarkFetched flow on a fresh QuestionStore. This is the storage half of
// the ask_human / fetch_answer MCP tool pair — if Create silently fails
// or Answer doesn't survive a reload, agents would see "still awaiting"
// forever.
func TestQuestionLifecycle(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	store := NewQuestionStore(contentDir)

	q, err := store.Create("claude-code", "piece:dziewczyny-nie-warto", "Czy mogę cytować ten wiersz w komercyjnej publikacji?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if q.ID == "" {
		t.Fatal("Create returned an empty ID")
	}
	if q.IsAnswered() {
		t.Error("freshly-created question reports IsAnswered=true")
	}
	if !q.IsAwaiting() {
		t.Error("freshly-created question reports IsAwaiting=false")
	}

	// Read it back through a fresh store — proves disk persistence.
	store2 := NewQuestionStore(contentDir)
	loaded, err := store2.Get(q.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if loaded.Question != q.Question {
		t.Errorf("question text changed: %q vs %q", loaded.Question, q.Question)
	}
	if loaded.From != "claude-code" {
		t.Errorf("from changed: %q vs claude-code", loaded.From)
	}
	if loaded.Context != "piece:dziewczyny-nie-warto" {
		t.Errorf("context changed: %q", loaded.Context)
	}

	// Owner answers
	if err := store2.Answer(q.ID, "Tak, z atrybucją: — kapoost"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	answered, err := store2.Get(q.ID)
	if err != nil {
		t.Fatalf("Get after Answer: %v", err)
	}
	if !answered.IsAnswered() {
		t.Error("answered question reports IsAnswered=false")
	}
	if !answered.IsPicked() {
		t.Error("answered+unfetched should be IsPicked")
	}
	if answered.IsFetched() {
		t.Error("answered question is fetched without MarkFetched")
	}

	// Agent fetches
	if err := store2.MarkFetched(q.ID, "claude-code"); err != nil {
		t.Fatalf("MarkFetched: %v", err)
	}
	fetched, _ := store2.Get(q.ID)
	if !fetched.IsFetched() {
		t.Error("after MarkFetched, IsFetched=false")
	}
	if fetched.FetchedBy != "claude-code" {
		t.Errorf("FetchedBy = %q, want claude-code", fetched.FetchedBy)
	}
}

func TestQuestionCreateRejectsEmpty(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	if _, err := store.Create("agent", "", ""); err == nil {
		t.Error("Create with empty question text should error, did not")
	}
	if _, err := store.Create("agent", "", "   \n\t  "); err == nil {
		t.Error("Create with whitespace-only question should error, did not")
	}
}

func TestQuestionIDIsUnique(t *testing.T) {
	// Two questions asked back-to-back must get distinct IDs even when the
	// timestamp prefix matches. Strict chronological ordering within a
	// minute is not promised — List() sorts by AskedAt timestamp, not by
	// id string.
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	q1, _ := store.Create("a1", "", "Pierwsze pytanie")
	q2, _ := store.Create("a2", "", "Drugie pytanie")
	if q1.ID == q2.ID {
		t.Errorf("IDs collide: %q == %q", q1.ID, q2.ID)
	}
}

// TestQuestionIDCollisionSameText covers the case where the ID prefix and
// the slug both match — same minute, same question text. Without collision
// handling the second Create silently overwrote the first file, and the
// dashboard showed one row for two ask_human calls.
func TestQuestionIDCollisionSameText(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))
	q1, err := store.Create("a1", "", "test")
	if err != nil {
		t.Fatalf("Create #1: %v", err)
	}
	q2, err := store.Create("a2", "", "test")
	if err != nil {
		t.Fatalf("Create #2: %v", err)
	}
	if q1.ID == q2.ID {
		t.Errorf("same-text IDs collide: %q == %q", q1.ID, q2.ID)
	}
	if got := len(store.List()); got != 2 {
		t.Errorf("expected 2 questions on disk, got %d", got)
	}
}
