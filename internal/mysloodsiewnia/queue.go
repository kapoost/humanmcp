package mysloodsiewnia

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// OpKind is what the vault should execute. Kept as a small enum so the
// vault-side worker can switch cleanly without version drift.
type OpKind string

const (
	OpStatus OpKind = "status"
	OpSearch OpKind = "search"
	OpGet    OpKind = "get"
	OpList   OpKind = "list"
)

// OpState tracks a queued operation's lifecycle. Ghost's blind-spot in the
// narada was collapsing "accepted" and "applied" into one HTTP 200 —
// distinguishing them here means agents (and tests) can tell the difference
// between "the vault took the job" and "the vault actually finished it".
type OpState string

const (
	OpAccepted OpState = "accepted" // enqueued, vault hasn't picked it up yet
	OpPicked   OpState = "picked"   // vault fetched via /pending-ops, working
	OpApplied  OpState = "applied"  // vault posted /complete with success
	OpFailed   OpState = "failed"   // vault posted /complete with error
)

// Op is one queued operation. The result channel is closed exactly once by
// Complete(), which unblocks whoever's waiting in WaitFor().
type Op struct {
	ID         string          `json:"op_id"`
	Kind       OpKind          `json:"kind"`
	Args       json.RawMessage `json:"args"`
	EnqueuedAt time.Time       `json:"enqueued_at"`

	// Wave 3 sharing / friend tokens. When TokenID is "" (or "owner"),
	// vault treats the op as unlimited-scope (wave 1 semantics). When
	// TokenID names a friend slug, vault MUST filter its SELECTs with
	// `AND doc_type IN (Scopes...) AND access != 'private'` before any
	// COUNT / LIMIT and MUST record the returned doc_id hashes in the
	// per-response audit sink. See ADR-0001 sekcja "Wave 3".
	TokenID string   `json:"token_id,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`

	// Set by Complete; readable only after result chan closes.
	State  OpState         `json:"-"`
	Result json.RawMessage `json:"-"`
	Err    string          `json:"-"`
	result chan struct{}
}

// Queue is the in-memory bridge queue. Not persistent: on process restart
// pending ops are lost — the design accepts this because (a) the agent has
// a 20s wait timeout and will report vault_timeout to the user, and (b) any
// pending op is by definition read-only in wave 1 so the user just re-asks.
type Queue struct {
	mu      sync.Mutex
	pending []*Op          // FIFO of accepted ops
	byID    map[string]*Op // lookup for /complete and WaitFor
}

func NewQueue() *Queue {
	return &Queue{byID: make(map[string]*Op)}
}

// Enqueue registers a new op with a fresh UUID and returns it. The caller
// blocks in WaitFor until the vault posts /complete or timeout elapses.
// Owner path — no scope propagation. Wave 1 semantics.
func (q *Queue) Enqueue(kind OpKind, args json.RawMessage) *Op {
	return q.enqueue(kind, args, "", nil)
}

// EnqueueScoped is the wave-3 friend-token variant: TokenID and Scopes are
// carried on the op so the vault worker can apply the SQL filter and audit
// write before returning. Race-safe — fields set before the op is visible
// to /pending-ops pickers.
func (q *Queue) EnqueueScoped(kind OpKind, args json.RawMessage, tokenID string, scopes []string) *Op {
	return q.enqueue(kind, args, tokenID, scopes)
}

func (q *Queue) enqueue(kind OpKind, args json.RawMessage, tokenID string, scopes []string) *Op {
	op := &Op{
		ID:         newOpID(),
		Kind:       kind,
		Args:       args,
		TokenID:    tokenID,
		Scopes:     scopes,
		EnqueuedAt: time.Now(),
		State:      OpAccepted,
		result:     make(chan struct{}),
	}
	q.mu.Lock()
	q.pending = append(q.pending, op)
	q.byID[op.ID] = op
	q.mu.Unlock()
	return op
}

// Pending returns up to max oldest accepted ops and marks them as picked.
// Called by the vault's /pending-ops poller.
func (q *Queue) Pending(max int) []*Op {
	if max <= 0 {
		max = 10
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.pending)
	if n > max {
		n = max
	}
	out := make([]*Op, 0, n)
	for i := 0; i < n; i++ {
		op := q.pending[i]
		op.State = OpPicked
		out = append(out, op)
	}
	q.pending = q.pending[n:]
	return out
}

// Complete records the vault's response. Idempotent for the same op_id —
// double-complete returns ErrAlreadyCompleted so a network retry from the
// vault can't corrupt state or wake a second waiter.
func (q *Queue) Complete(opID string, state OpState, result json.RawMessage, errMsg string) error {
	q.mu.Lock()
	op, ok := q.byID[opID]
	if !ok {
		q.mu.Unlock()
		return ErrUnknownOp
	}
	if op.State == OpApplied || op.State == OpFailed {
		q.mu.Unlock()
		return ErrAlreadyCompleted
	}
	op.State = state
	op.Result = result
	op.Err = errMsg
	q.mu.Unlock()
	close(op.result)
	return nil
}

// WaitFor blocks until Complete is called for opID, or timeout fires.
// Returns the op (with State/Result/Err populated) and a boolean signalling
// whether the wait completed (true) or timed out (false).
func (q *Queue) WaitFor(opID string, timeout time.Duration) (*Op, bool) {
	q.mu.Lock()
	op, ok := q.byID[opID]
	q.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case <-op.result:
		return op, true
	case <-time.After(timeout):
		return op, false
	}
}

// Prune drops completed ops older than maxAge from the byID map so it
// doesn't grow forever. Call periodically from a background goroutine.
func (q *Queue) Prune(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)
	q.mu.Lock()
	defer q.mu.Unlock()
	removed := 0
	for id, op := range q.byID {
		if (op.State == OpApplied || op.State == OpFailed) && op.EnqueuedAt.Before(cutoff) {
			delete(q.byID, id)
			removed++
		}
	}
	return removed
}

var (
	ErrUnknownOp        = errors.New("mysloodsiewnia: unknown op id")
	ErrAlreadyCompleted = errors.New("mysloodsiewnia: op already completed")
)

// newOpID returns a 128-bit hex id (32 chars). Using crypto/rand keeps
// collision probability negligible without pulling in a UUID dependency.
func newOpID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic on this platform; fall back
		// to a timestamp-based id so the server still boots. Not
		// collision-safe under load — but neither is a dead server.
		return "ts-" + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
