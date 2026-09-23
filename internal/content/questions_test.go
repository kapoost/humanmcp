package content

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestFindLatestByAsker pins the re-ask delivery path.
//
// Until September 2026 an answer only reached an agent that had kept its
// question ID across sessions and polled fetch_answer. Measured on the live
// queue, that delivered 3 of 16 answers; nobody had collected one since June.
// Re-asking is the signal stateless agents actually produce, so the store has
// to be able to recognise a twin.
func TestFindLatestByAsker(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))

	first, err := store.Create("literary-analysis-agent", "", "How do you connect divisors to love?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Same asker, same question, sloppier whitespace and casing — still a twin.
	got, ok := store.FindLatestByAsker("Literary-Analysis-Agent", "  How do you  connect DIVISORS to love?  ", "")
	if !ok {
		t.Fatal("twin not recognised through whitespace/case differences")
	}
	if got.ID != first.ID {
		t.Errorf("matched %s, wanted %s", got.ID, first.ID)
	}

	// A different asker with identical wording must NOT match: the answer may
	// have been written for the first one.
	if _, ok := store.FindLatestByAsker("someone-else", "How do you connect divisors to love?", ""); ok {
		t.Error("matched across askers — an answer could leak to the wrong agent")
	}

	// Anonymous callers cannot be matched at all, for the same reason.
	if _, ok := store.FindLatestByAsker("", "How do you connect divisors to love?", ""); ok {
		t.Error("matched with an empty from")
	}

	// A different question from the same asker is not a twin.
	if _, ok := store.FindLatestByAsker("literary-analysis-agent", "What is the sea?", ""); ok {
		t.Error("matched a different question")
	}
}

// TestFindLatestByAskerPicksNewest guards the tie-break: duplicates already
// exist in the live queue (uniqueID deliberately keeps both copies rather than
// overwriting), so a lookup must land on the most recent one.
func TestFindLatestByAskerPicksNewest(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))

	older, err := store.Create("researcher", "", "What is the exact title of your poem with slug 'private-parts'?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newer, err := store.Create("researcher", "", "What is the exact title of your poem with slug 'private-parts'?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if older.ID == newer.ID {
		t.Fatal("duplicate submission reused the ID — the second would overwrite the first")
	}

	got, ok := store.FindLatestByAsker("researcher", "What is the exact title of your poem with slug 'private-parts'?", "")
	if !ok {
		t.Fatal("twin not found")
	}
	if !got.AskedAt.Before(newer.AskedAt) && got.ID != newer.ID && got.ID != older.ID {
		t.Errorf("unexpected match %s", got.ID)
	}
}

// ID było wyłącznie znacznikiem czasu plus slugiem pytania — dawało się
// odtworzyć, znając utwór i przybliżoną godzinę. A fetch_answer(id) nie ma
// innej bramki: kto zna identyfikator, ten czyta odpowiedź.
func TestQuestionIDsAreNotGuessable(t *testing.T) {
	store := NewQuestionStore(filepath.Join(t.TempDir(), "content"))

	const text = "Jaki jest tytuł tego utworu?"
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		q, err := store.Create("badacz", "", text)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seen[q.ID] {
			t.Fatalf("powtórzony identyfikator: %s", q.ID)
		}
		seen[q.ID] = true
	}

	// Sam znacznik czasu plus slug nie może wystarczyć do trafienia w ID.
	guess := generateQuestionID(time.Now().UTC(), text)
	for id := range seen {
		if id == guess {
			t.Errorf("identyfikator odtwarzalny z samej treści i czasu: %s", id)
		}
	}
	if _, err := store.Get(strings.TrimSuffix(guess, guess[len(guess)-5:])); err == nil {
		t.Error("pytanie da się pobrać po samym przedrostku bez losowej końcówki")
	}
}
