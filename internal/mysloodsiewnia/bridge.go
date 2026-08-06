package mysloodsiewnia

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Bridge wires HTTP endpoints the vault polls: /heartbeat, /pending-ops,
// /complete. All three require the shared VaultBridgeToken as a Bearer
// token — the token is a Fly secret, the vault reads it from
// ~/mysloodsiewnia/config/bridge.env (chmod 600).
type Bridge struct {
	Liveness *Liveness
	Queue    *Queue
	Token    string // shared secret; empty ⇒ bridge disabled (503 on all endpoints)
}

func NewBridge(liveness *Liveness, queue *Queue, token string) *Bridge {
	return &Bridge{Liveness: liveness, Queue: queue, Token: token}
}

// Register mounts the three bridge routes on mux. Path prefix is fixed at
// /mysloodsiewnia/ so it's obvious what these are for when reading /server
// logs.
func (b *Bridge) Register(mux *http.ServeMux) {
	mux.HandleFunc("/mysloodsiewnia/heartbeat", b.handleHeartbeat)
	mux.HandleFunc("/mysloodsiewnia/pending-ops", b.handlePendingOps)
	mux.HandleFunc("/mysloodsiewnia/complete", b.handleComplete)
}

// authOK checks the Authorization: Bearer <token> header in constant time.
// Empty configured token disables the whole bridge — the vault gets 503
// so it back-offs politely instead of retrying a broken deploy.
func (b *Bridge) authOK(r *http.Request) (ok bool, disabled bool) {
	if b.Token == "" {
		return false, true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false, false
	}
	got := strings.TrimPrefix(h, "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(b.Token)) != 1 {
		return false, false
	}
	return true, false
}

type heartbeatBody struct {
	CommitSHA         string    `json:"commit_sha"`
	PersonasUpdatedAt time.Time `json:"personas_updated_at"`
	SkillsUpdatedAt   time.Time `json:"skills_updated_at"`
	FTSHealthy        bool      `json:"fts_healthy"`
}

func (b *Bridge) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ok, disabled := b.authOK(r); !ok {
		if disabled {
			http.Error(w, "bridge disabled: VAULT_BRIDGE_TOKEN not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body heartbeatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid heartbeat body", http.StatusBadRequest)
		return
	}
	b.Liveness.Update(body.CommitSHA, body.PersonasUpdatedAt, body.SkillsUpdatedAt, body.FTSHealthy)
	writeJSON(w, http.StatusOK, map[string]any{
		"ack":         true,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

func (b *Bridge) handlePendingOps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ok, disabled := b.authOK(r); !ok {
		if disabled {
			http.Error(w, "bridge disabled: VAULT_BRIDGE_TOKEN not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ops := b.Queue.Pending(10)
	// Marshal explicit fields — the private `result` chan on Op is not
	// exported but we still want to filter to the pull contract.
	type wireOp struct {
		ID         string          `json:"op_id"`
		Kind       OpKind          `json:"kind"`
		Args       json.RawMessage `json:"args"`
		EnqueuedAt time.Time       `json:"enqueued_at"`
	}
	out := make([]wireOp, 0, len(ops))
	for _, op := range ops {
		out = append(out, wireOp{ID: op.ID, Kind: op.Kind, Args: op.Args, EnqueuedAt: op.EnqueuedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ops": out})
}

type completeBody struct {
	OpID   string          `json:"op_id"`
	Status OpState         `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (b *Bridge) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ok, disabled := b.authOK(r); !ok {
		if disabled {
			http.Error(w, "bridge disabled: VAULT_BRIDGE_TOKEN not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body completeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid complete body", http.StatusBadRequest)
		return
	}
	if body.Status != OpApplied && body.Status != OpFailed {
		http.Error(w, "status must be applied or failed", http.StatusBadRequest)
		return
	}
	if err := b.Queue.Complete(body.OpID, body.Status, body.Result, body.Error); err != nil {
		// Unknown op or double-complete — return 409 so the vault can
		// swallow the retry without escalating. Bridge state is
		// self-healing under duplicate delivery.
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "op_id": body.OpID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ack": true, "op_id": body.OpID})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
