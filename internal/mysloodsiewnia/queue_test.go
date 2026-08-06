package mysloodsiewnia

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestQueueRoundTrip(t *testing.T) {
	q := NewQueue()
	op := q.Enqueue(OpSearch, json.RawMessage(`{"query":"morze"}`))
	if op.State != OpAccepted {
		t.Fatalf("new op state = %q, want %q", op.State, OpAccepted)
	}
	if len(op.ID) != 32 {
		t.Fatalf("op id length = %d, want 32 (128-bit hex)", len(op.ID))
	}

	pending := q.Pending(10)
	if len(pending) != 1 || pending[0].ID != op.ID {
		t.Fatalf("Pending returned %d ops, want the one we enqueued", len(pending))
	}
	if pending[0].State != OpPicked {
		t.Fatalf("picked op state = %q, want %q", pending[0].State, OpPicked)
	}
	if again := q.Pending(10); len(again) != 0 {
		t.Fatalf("Pending returned %d, want 0 (already picked)", len(again))
	}
}

func TestQueueWaitForUnblocksOnComplete(t *testing.T) {
	q := NewQueue()
	op := q.Enqueue(OpGet, json.RawMessage(`{"doc_slug":"x"}`))

	// Complete from another goroutine after a short delay.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = q.Complete(op.ID, OpApplied, json.RawMessage(`{"ok":true}`), "")
	}()

	got, ok := q.WaitFor(op.ID, 1*time.Second)
	if !ok {
		t.Fatal("WaitFor timed out but Complete was called")
	}
	if got.State != OpApplied {
		t.Fatalf("state after complete = %q, want %q", got.State, OpApplied)
	}
	if string(got.Result) != `{"ok":true}` {
		t.Fatalf("result lost: %s", got.Result)
	}
}

func TestQueueWaitForTimeout(t *testing.T) {
	q := NewQueue()
	op := q.Enqueue(OpStatus, nil)
	_, ok := q.WaitFor(op.ID, 30*time.Millisecond)
	if ok {
		t.Fatal("WaitFor should have timed out — vault never completed the op")
	}
}

func TestQueueDoubleCompleteRejected(t *testing.T) {
	q := NewQueue()
	op := q.Enqueue(OpStatus, nil)
	if err := q.Complete(op.ID, OpApplied, nil, ""); err != nil {
		t.Fatalf("first complete: %v", err)
	}
	err := q.Complete(op.ID, OpApplied, nil, "")
	if !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("second complete: got %v, want ErrAlreadyCompleted", err)
	}
}

func TestQueueCompleteUnknownRejected(t *testing.T) {
	q := NewQueue()
	err := q.Complete("nope", OpApplied, nil, "")
	if !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("unknown op: got %v, want ErrUnknownOp", err)
	}
}

func TestQueueConcurrentEnqueueUnique(t *testing.T) {
	q := NewQueue()
	const N = 200
	var wg sync.WaitGroup
	ids := make(chan string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op := q.Enqueue(OpStatus, nil)
			ids <- op.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]bool, N)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate op id %q under concurrent enqueue", id)
		}
		seen[id] = true
	}
	if len(seen) != N {
		t.Fatalf("got %d unique ids, want %d", len(seen), N)
	}
}
