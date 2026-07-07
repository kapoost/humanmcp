package content

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestRitualCreateGet(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	job, err := store.Create("narada", "deploy z sekretami do prod", []string{"ghost", "hodor"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID == "" || job.Status != RitualPending {
		t.Fatalf("bad job: %+v", job)
	}
	got, err := store.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Context != "deploy z sekretami do prod" {
		t.Errorf("context lost: %q", got.Context)
	}
	if len(got.Personas) != 2 || got.Personas[0] != "ghost" {
		t.Errorf("personas lost: %v", got.Personas)
	}
}

func TestRitualLifecycle(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	job, err := store.Create("narada", "kontekst", []string{"mira"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkRunning(job.ID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	after, _ := store.Get(job.ID)
	if after.Status != RitualRunning || after.StartedAt.IsZero() {
		t.Errorf("not running: %+v", after)
	}
	if err := store.Complete(job.ID, []PersonaVoice{{Slug: "mira", Recommendation: "async"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done, _ := store.Get(job.ID)
	if done.Status != RitualDone || len(done.Voices) != 1 {
		t.Errorf("not done: %+v", done)
	}
	if done.Voices[0].Recommendation != "async" {
		t.Errorf("voice lost: %+v", done.Voices)
	}
}

func TestRitualFail(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	job, _ := store.Create("narada", "x", nil)
	_ = store.MarkRunning(job.ID)
	if err := store.Fail(job.ID, "LLM timeout"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	got, _ := store.Get(job.ID)
	if got.Status != RitualFailed || got.Error != "LLM timeout" {
		t.Errorf("not failed: %+v", got)
	}
}

func TestRitualMarkRunningRejectsNonPending(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	job, _ := store.Create("narada", "x", nil)
	_ = store.MarkRunning(job.ID)
	if err := store.MarkRunning(job.ID); err == nil {
		t.Error("expected error when marking already-running job")
	}
}

func TestRitualListPendingRecent(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	j1, _ := store.Create("narada", "a", nil)
	_, _ = store.Create("narada", "b", nil)
	j3, _ := store.Create("narada", "c", nil)
	_ = store.MarkRunning(j1.ID)
	_ = store.Complete(j1.ID, nil)

	pending, _ := store.ListPending()
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}
	recent, _ := store.ListRecent(10)
	if len(recent) != 3 {
		t.Errorf("expected 3 recent, got %d", len(recent))
	}
	if recent[0].ID != j3.ID {
		t.Errorf("expected newest first, got %s", recent[0].ID)
	}
}

func TestRitualIDsUnique(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		j, err := store.Create("narada", "kontekst", nil)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if seen[j.ID] {
			t.Fatalf("ID collision: %s", j.ID)
		}
		seen[j.ID] = true
	}
}

func TestRitualConcurrentCreate(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	var wg sync.WaitGroup
	ids := make(chan string, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := store.Create("narada", "kontekst", nil)
			if err == nil {
				ids <- j.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("concurrent collision: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 30 {
		t.Errorf("expected 30 unique ids, got %d", len(seen))
	}
}

func TestRitualRejectsBadID(t *testing.T) {
	store := NewRitualStore(filepath.Join(t.TempDir(), "content"))
	if _, err := store.Get("../etc"); err == nil {
		t.Error("expected error for path-traversal id")
	}
	if _, err := store.Get("BadID"); err == nil {
		t.Error("expected error for uppercase id")
	}
}
