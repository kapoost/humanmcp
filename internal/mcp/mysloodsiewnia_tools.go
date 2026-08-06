package mcp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// Wait timeout for a queued op to complete. Wave 1 is read-only so a long
// wait is safe — the vault's adaptive poller will pick up an op within 2s
// under load. Timeout kicks in when the vault is stale but hasn't crossed
// the liveness TTL yet (a torn network).
const bridgeWaitTimeout = 20 * time.Second

// mysloodsiewniaGate is called first by every bridge tool. Returns the
// serialized offline/degraded response (as JSON in a text block) plus a
// bool indicating whether the tool should short-circuit. When the caller
// gets `stop=true` it must return the returned text as its final result —
// the vault is not reachable so enqueuing an op would just time out.
func (h *Handler) mysloodsiewniaGate() (text string, stop bool) {
	if h.liveness == nil {
		return renderBridgeStatus(mysloodsiewnia.Snapshot{Status: mysloodsiewnia.StatusUnreachable}), true
	}
	snap := h.liveness.Get()
	switch snap.Status {
	case mysloodsiewnia.StatusUnreachable, mysloodsiewnia.StatusDegraded:
		return renderBridgeStatus(snap), true
	}
	return "", false
}

// renderBridgeStatus is the offline/degraded/online status text used by both
// mysloodsiewnia_status and the gate short-circuit in other tools. Kept as
// one canonical formatter so v1↔v2 parity is trivial (single source of truth).
func renderBridgeStatus(snap mysloodsiewnia.Snapshot) string {
	out := map[string]any{
		"status":    string(snap.Status),
		"last_seen": formatBridgeTime(snap.LastSeen),
	}
	if snap.Status == mysloodsiewnia.StatusDegraded {
		out["reason"] = snap.DegradedReason
	}
	if snap.CommitSHA != "" {
		out["commit_sha"] = snap.CommitSHA
	}
	if !snap.PersonasUpdatedAt.IsZero() {
		out["personas_updated_at"] = snap.PersonasUpdatedAt.UTC().Format(time.RFC3339)
	}
	if !snap.SkillsUpdatedAt.IsZero() {
		out["skills_updated_at"] = snap.SkillsUpdatedAt.UTC().Format(time.RFC3339)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

func formatBridgeTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}

// enqueueAndWait pushes an op onto the shared queue and waits for the vault
// to complete it. Returns the text the tool should surface to the agent —
// either the successful result, a timeout note, or a vault-side error.
func (h *Handler) enqueueAndWait(kind mysloodsiewnia.OpKind, args any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return `{"status":"internal_error","error":"failed to marshal args"}`
	}
	if h.bridgeQueue == nil {
		// Should never happen if liveness reports online, but defence in
		// depth: don't panic on a bad wiring.
		return `{"status":"internal_error","error":"bridge queue not configured"}`
	}
	op := h.bridgeQueue.Enqueue(kind, raw)
	completed, ok := h.bridgeQueue.WaitFor(op.ID, bridgeWaitTimeout)
	if !ok {
		out, _ := json.MarshalIndent(map[string]any{
			"status": "vault_timeout",
			"op_id":  op.ID,
			"note":   "Vault didn't complete the op within the wait window; retry safely.",
		}, "", "  ")
		return string(out)
	}
	if completed.State == mysloodsiewnia.OpFailed {
		out, _ := json.MarshalIndent(map[string]any{
			"status": "vault_error",
			"op_id":  completed.ID,
			"error":  completed.Err,
		}, "", "  ")
		return string(out)
	}
	// Vault-side result is already JSON — surface it as-is so the agent
	// gets whatever /query or the get handler produced.
	if len(completed.Result) == 0 {
		return `{"status":"applied","result":null}`
	}
	// Wrap the vault's result so agents can rely on a uniform envelope.
	envelope := map[string]any{"status": "online", "op_id": completed.ID, "result": json.RawMessage(completed.Result)}
	b, _ := json.MarshalIndent(envelope, "", "  ")
	return string(b)
}

// ── v1 tool handlers ────────────────────────────────────────────────────────

func (h *Handler) toolMysloodsiewniaStatus(w http.ResponseWriter, req *Request) {
	if h.liveness == nil {
		writeToolText(w, req.ID, renderBridgeStatus(mysloodsiewnia.Snapshot{Status: mysloodsiewnia.StatusUnreachable}))
		return
	}
	writeToolText(w, req.ID, renderBridgeStatus(h.liveness.Get()))
}

func (h *Handler) toolMysloodsiewniaSearch(w http.ResponseWriter, req *Request, arguments json.RawMessage) {
	var args struct {
		Query   string `json:"query"`
		Limit   int    `json:"limit,omitempty"`
		DocType string `json:"doc_type,omitempty"`
		DocSlug string `json:"doc_slug,omitempty"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			writeToolText(w, req.ID, `{"status":"invalid_args","error":"could not parse arguments"}`)
			return
		}
	}
	if args.Query == "" {
		writeToolText(w, req.ID, `{"status":"invalid_args","error":"query is required"}`)
		return
	}
	// Validate args BEFORE gating on liveness — agents get useful errors
	// even when the vault is offline (bad query is always bad).
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 5
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpSearch, args))
}

func (h *Handler) toolMysloodsiewniaList(w http.ResponseWriter, req *Request, arguments json.RawMessage) {
	var args struct {
		DocType string `json:"doc_type,omitempty"`
		Limit   int    `json:"limit,omitempty"`
		Offset  int    `json:"offset,omitempty"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			writeToolText(w, req.ID, `{"status":"invalid_args","error":"could not parse arguments"}`)
			return
		}
	}
	if args.Limit <= 0 || args.Limit > 200 {
		args.Limit = 50
	}
	if args.Offset < 0 {
		args.Offset = 0
	}
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpList, args))
}

func (h *Handler) toolMysloodsiewniaGet(w http.ResponseWriter, req *Request, arguments json.RawMessage) {
	var args struct {
		DocSlug string `json:"doc_slug"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			writeToolText(w, req.ID, `{"status":"invalid_args","error":"could not parse arguments"}`)
			return
		}
	}
	if args.DocSlug == "" {
		writeToolText(w, req.ID, `{"status":"invalid_args","error":"doc_slug is required"}`)
		return
	}
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpGet, args))
}

// writeToolText wraps writeResult with the standard MCP CallResult envelope.
func writeToolText(w http.ResponseWriter, id interface{}, text string) {
	writeResult(w, id, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
}
