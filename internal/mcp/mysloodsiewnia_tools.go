package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// Wait timeout for a queued op to complete. Wave 1 is read-only so a long
// wait is safe — the vault's adaptive poller will pick up an op within 2s
// under load. Timeout kicks in when the vault is stale but hasn't crossed
// the liveness TTL yet (a torn network).
const bridgeWaitTimeout = 20 * time.Second

// unauthorizedText is what mysloodsiewnia_* tools return without any
// recognized token. IDENTICAL for owner-absent AND friend-unknown/expired/
// malformed (wave 3 W4 + Z3 pin — see ADR-0001). Same string as v2 so the
// parity test between v1 and v2 stays green.
const unauthorizedText = "Unauthorized — mysloodsiewnia_* tools require Authorization: Bearer <edit token>."

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
// to complete it. Owner path (tokenID=="" or "owner") uses unscoped Enqueue;
// friend path uses EnqueueScoped so the vault worker applies the SQL filter
// + writes the transactional audit row for the returned doc_ids.
func (h *Handler) enqueueAndWait(kind mysloodsiewnia.OpKind, args any, tokenID string, scopes []string) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return `{"status":"internal_error","error":"failed to marshal args"}`
	}
	if h.bridgeQueue == nil {
		return `{"status":"internal_error","error":"bridge queue not configured"}`
	}
	var op *mysloodsiewnia.Op
	if tokenID == "" || tokenID == ownerTokenID {
		op = h.bridgeQueue.Enqueue(kind, raw)
	} else {
		op = h.bridgeQueue.EnqueueScoped(kind, raw, tokenID, scopes)
	}
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
	if len(completed.Result) == 0 {
		return `{"status":"applied","result":null}`
	}
	envelope := map[string]any{"status": "online", "op_id": completed.ID, "result": json.RawMessage(completed.Result)}
	b, _ := json.MarshalIndent(envelope, "", "  ")
	return string(b)
}

// friendRateLimit returns (responseBody, allowed). Inline JSON (no indent)
// so wave3_rate_limit_per_token.yaml assertion matches — body_contains
// checks for `"status":"rate_limited"` with no space after colon.
func (h *Handler) friendRateLimit(tokenID string) (string, bool) {
	if tokenID == "" || tokenID == ownerTokenID {
		return "", true
	}
	limit := 30 // Z4 fallback
	if h.cfg != nil {
		if spec, exists := h.cfg.FriendTokens[tokenID]; exists && spec != nil && spec.RateLimitPerHour > 0 {
			limit = spec.RateLimitPerHour
		}
	}
	allow, retry := h.CheckFriendTokenRateLimit(tokenID, limit)
	if !allow {
		return fmt.Sprintf(`{"status":"rate_limited","retry_after":%d}`, retry), false
	}
	return "", true
}

// friendScope enforces doc_type ∈ scopes for scoped callers. JSON-indented
// so wave3_scoped_out_of_scope_403.yaml assertion matches — body_contains
// checks for `"status": "out_of_scope"` WITH space after colon.
//
// Empty docType ⇒ vault-side handles scope (SELECT ... WHERE doc_type IN
// (scopes)). Only an explicit out-of-scope doc_type short-circuits here.
//
// Asymmetric with access:private (silent skip on vault) — see ADR-0001
// W3/Z2 pinned invariant. Do NOT unify.
func friendScope(tokenID string, scopes []string, docType string) (string, bool) {
	if tokenID == "" || tokenID == ownerTokenID || scopes == nil {
		return "", true
	}
	if docType == "" {
		return "", true
	}
	for _, s := range scopes {
		if s == docType || s == "*" {
			return "", true
		}
	}
	body := map[string]any{
		"status":  "out_of_scope",
		"allowed": scopes,
	}
	b, _ := json.MarshalIndent(body, "", "  ")
	return string(b), false
}

// ── v1 tool handlers ────────────────────────────────────────────────────────
//
// Auth precedence for all four tools:
//   (1) AuthorizeRequestByHeaders → unauthorized text if not recognized
//   (2) Parse args → invalid_args on decode failure or required-arg missing
//   (3) friendRateLimit (owner bypasses)
//   (4) friendScope on args.DocType where applicable (owner bypasses)
//   (5) liveness gate → offline / degraded shape
//   (6) enqueueAndWait — passes tokenID + scopes to vault via queue

func (h *Handler) toolMysloodsiewniaStatus(w http.ResponseWriter, r *http.Request, req *Request) {
	tokenID, _, ok := h.AuthorizeRequestByHeaders(r.Header)
	if !ok {
		writeToolText(w, req.ID, unauthorizedText)
		return
	}
	if resp, allowed := h.friendRateLimit(tokenID); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	if h.liveness == nil {
		writeToolText(w, req.ID, renderBridgeStatus(mysloodsiewnia.Snapshot{Status: mysloodsiewnia.StatusUnreachable}))
		return
	}
	writeToolText(w, req.ID, renderBridgeStatus(h.liveness.Get()))
}

func (h *Handler) toolMysloodsiewniaSearch(w http.ResponseWriter, r *http.Request, req *Request, arguments json.RawMessage) {
	tokenID, scopes, ok := h.AuthorizeRequestByHeaders(r.Header)
	if !ok {
		writeToolText(w, req.ID, unauthorizedText)
		return
	}
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
	if resp, allowed := h.friendRateLimit(tokenID); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	if resp, allowed := friendScope(tokenID, scopes, args.DocType); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 5
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpSearch, args, tokenID, scopes))
}

func (h *Handler) toolMysloodsiewniaList(w http.ResponseWriter, r *http.Request, req *Request, arguments json.RawMessage) {
	tokenID, scopes, ok := h.AuthorizeRequestByHeaders(r.Header)
	if !ok {
		writeToolText(w, req.ID, unauthorizedText)
		return
	}
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
	if resp, allowed := h.friendRateLimit(tokenID); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	if resp, allowed := friendScope(tokenID, scopes, args.DocType); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpList, args, tokenID, scopes))
}

func (h *Handler) toolMysloodsiewniaGet(w http.ResponseWriter, r *http.Request, req *Request, arguments json.RawMessage) {
	tokenID, scopes, ok := h.AuthorizeRequestByHeaders(r.Header)
	if !ok {
		writeToolText(w, req.ID, unauthorizedText)
		return
	}
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
	if resp, allowed := h.friendRateLimit(tokenID); !allowed {
		writeToolText(w, req.ID, resp)
		return
	}
	// _get has no doc_type in args; scope enforcement is deferred to the
	// vault SQL layer, which filters by scope + access before returning
	// the row (out-of-scope / private ⇒ not_found).
	if text, stop := h.mysloodsiewniaGate(); stop {
		writeToolText(w, req.ID, text)
		return
	}
	writeToolText(w, req.ID, h.enqueueAndWait(mysloodsiewnia.OpGet, args, tokenID, scopes))
}

// writeToolText wraps writeResult with the standard MCP CallResult envelope.
func writeToolText(w http.ResponseWriter, id interface{}, text string) {
	writeResult(w, id, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
}
