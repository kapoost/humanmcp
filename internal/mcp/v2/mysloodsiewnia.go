package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// bridgeWaitTimeout mirrors the v1 constant — v1↔v2 parity depends on both
// paths waiting the same amount before returning vault_timeout.
const bridgeWaitTimeout = 20 * time.Second

// unauthorizedText is what bridge tools return without any recognized token.
// IDENTICAL for owner-not-present AND friend-token-unknown/expired/malformed
// (wave 3 W4 + Z3). Same string as v1 so parity_test passes.
const unauthorizedText = "Unauthorized — mysloodsiewnia_* tools require Authorization: Bearer <edit token>."

// ownerSlug is the tokenID stamped on requests that carry the edit / agent /
// session token. Kept in sync with internal/mcp/friend_auth.go:ownerTokenID.
const ownerSlug = "owner"

// ── mysloodsiewnia_status ───────────────────────────────────────────────────

func registerMysloodsiewniaStatus(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_status",
		Description: "Read-only liveness probe on kapoost's local vault (mysłoodsiewnia). Returns {status: online|degraded|offline, last_seen, commit_sha, personas_updated_at, skills_updated_at}. Offline is a stable state — retry later, don't escalate. Requires Authorization: Bearer <edit token or friend token>.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		tokenID, _, ok := src.AuthorizeRequestByHeaders(req.Extra.Header)
		if !ok {
			return textResult(unauthorizedText), nil
		}
		if resp, allowed := enforceRateLimit(src, tokenID); !allowed {
			return textResult(resp), nil
		}
		snap := currentSnapshot(src)
		return textResult(renderBridgeStatus(snap)), nil
	})
}

// ── mysloodsiewnia_search ───────────────────────────────────────────────────

func registerMysloodsiewniaSearch(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_search",
		Description: "Full-text search (BM25 over SQLite FTS5) on kapoost's local vault corpus. Requires Authorization: Bearer <edit token or friend token>. Friend tokens see only their scoped doc_types; access:private is invisible. Returns {status, results:[{source, type, body, doc_slug, title, page, citation}], summary}. Vault offline ⇒ {status:offline}.",
		InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"integer"},"doc_type":{"type":"string"},"doc_slug":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		tokenID, scopes, ok := src.AuthorizeRequestByHeaders(req.Extra.Header)
		if !ok {
			return textResult(unauthorizedText), nil
		}
		var args struct {
			Query   string `json:"query"`
			Limit   int    `json:"limit,omitempty"`
			DocType string `json:"doc_type,omitempty"`
			DocSlug string `json:"doc_slug,omitempty"`
		}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return textResult(`{"status":"invalid_args","error":"could not parse arguments"}`), nil
			}
		}
		if args.Query == "" {
			return textResult(`{"status":"invalid_args","error":"query is required"}`), nil
		}
		if resp, allowed := enforceRateLimit(src, tokenID); !allowed {
			return textResult(resp), nil
		}
		if resp, allowed := enforceScope(tokenID, scopes, args.DocType); !allowed {
			return textResult(resp), nil
		}
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		if args.Limit <= 0 || args.Limit > 20 {
			args.Limit = 5
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpSearch, args, tokenID, scopes)), nil
	})
}

// ── mysloodsiewnia_list ─────────────────────────────────────────────────────

func registerMysloodsiewniaList(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_list",
		Description: "Enumerate vault documents without FTS — for browsing by type or paginating. Requires Authorization: Bearer <edit token or friend token>. Friend tokens see only their scoped doc_types; access:private is invisible. Args: {doc_type?: string filter (note/pdf/literatura/calendar_event/...), limit?: int 1-200 default 50, offset?: int default 0}. Returns [{slug, title, doc_type, created_at, chunk_count}]. Vault offline ⇒ {status:offline}.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"doc_type":{"type":"string"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		tokenID, scopes, ok := src.AuthorizeRequestByHeaders(req.Extra.Header)
		if !ok {
			return textResult(unauthorizedText), nil
		}
		var args struct {
			DocType string `json:"doc_type,omitempty"`
			Limit   int    `json:"limit,omitempty"`
			Offset  int    `json:"offset,omitempty"`
		}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return textResult(`{"status":"invalid_args","error":"could not parse arguments"}`), nil
			}
		}
		if args.Limit <= 0 || args.Limit > 200 {
			args.Limit = 50
		}
		if args.Offset < 0 {
			args.Offset = 0
		}
		if resp, allowed := enforceRateLimit(src, tokenID); !allowed {
			return textResult(resp), nil
		}
		if resp, allowed := enforceScope(tokenID, scopes, args.DocType); !allowed {
			return textResult(resp), nil
		}
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpList, args, tokenID, scopes)), nil
	})
}

// ── mysloodsiewnia_get ──────────────────────────────────────────────────────

func registerMysloodsiewniaGet(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_get",
		Description: "Fetch one document from kapoost's vault by slug. Requires Authorization: Bearer <edit token or friend token>. Friend tokens: vault-side filter enforces scope + access:private invisibility — an out-of-scope or private slug returns not_found. Vault offline ⇒ {status:offline}.",
		InputSchema: json.RawMessage(`{"type":"object","required":["doc_slug"],"properties":{"doc_slug":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		tokenID, scopes, ok := src.AuthorizeRequestByHeaders(req.Extra.Header)
		if !ok {
			return textResult(unauthorizedText), nil
		}
		var args struct {
			DocSlug string `json:"doc_slug"`
		}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return textResult(`{"status":"invalid_args","error":"could not parse arguments"}`), nil
			}
		}
		if args.DocSlug == "" {
			return textResult(`{"status":"invalid_args","error":"doc_slug is required"}`), nil
		}
		if resp, allowed := enforceRateLimit(src, tokenID); !allowed {
			return textResult(resp), nil
		}
		// _get has no doc_type in args — scope enforcement is deferred to
		// the vault SQL layer, which filters by scope + access before
		// returning the row (out-of-scope / private ⇒ not_found).
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpGet, args, tokenID, scopes)), nil
	})
}

// ── shared helpers (mirror internal/mcp/mysloodsiewnia_tools.go) ─────────────

func currentSnapshot(src Source) mysloodsiewnia.Snapshot {
	l := src.Liveness()
	if l == nil {
		return mysloodsiewnia.Snapshot{Status: mysloodsiewnia.StatusUnreachable}
	}
	return l.Get()
}

func gate(src Source) (text string, stop bool) {
	snap := currentSnapshot(src)
	if snap.Status == mysloodsiewnia.StatusOnline {
		return "", false
	}
	return renderBridgeStatus(snap), true
}

// enforceRateLimit returns (responseBody, allowed). Owner bypasses. On deny
// the body shape is inline (no indent) `{"status":"rate_limited","retry_after":N}`
// — pinned by wave3_rate_limit_per_token.yaml (asserts without space after
// colon). See ADR-0001 Prerequisite + Z4.
func enforceRateLimit(src Source, tokenID string) (string, bool) {
	if tokenID == "" || tokenID == ownerSlug {
		return "", true
	}
	limit := 30 // Z4 fallback
	if cfg := src.Config(); cfg != nil {
		if spec, exists := cfg.FriendTokens[tokenID]; exists && spec != nil && spec.RateLimitPerHour > 0 {
			limit = spec.RateLimitPerHour
		}
	}
	allow, retry := src.CheckFriendTokenRateLimit(tokenID, limit)
	if !allow {
		return fmt.Sprintf(`{"status":"rate_limited","retry_after":%d}`, retry), false
	}
	return "", true
}

// enforceScope returns (responseBody, allowed). Owner/nil-scopes bypass. Empty
// docType means the caller didn't filter — the vault will apply the scope
// filter on its side (SELECT ... WHERE doc_type IN (Scopes...)). Only an
// explicit out-of-scope doc_type argument short-circuits here.
//
// Body shape is JSON-indented `{"status": "out_of_scope","allowed": [...]}`
// — pinned by wave3_scoped_out_of_scope_403.yaml (asserts WITH space after
// colon). Deliberately asymmetric with access:private (silent skip on vault
// side) — see ADR-0001 W3/Z2 pinned invariant. Do NOT unify.
func enforceScope(tokenID string, scopes []string, docType string) (string, bool) {
	if tokenID == "" || tokenID == ownerSlug || scopes == nil {
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

// enqueueAndWait — tokenID + scopes propagate to the vault via the queue
// wire form. Owner path (tokenID=="" or "owner") uses the wave-1 unscoped
// Enqueue; friend path uses EnqueueScoped so the vault worker applies the
// SQL filter + writes the transactional audit row.
func enqueueAndWait(src Source, kind mysloodsiewnia.OpKind, args any, tokenID string, scopes []string) string {
	q := src.BridgeQueue()
	if q == nil {
		return `{"status":"internal_error","error":"bridge queue not configured"}`
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return `{"status":"internal_error","error":"failed to marshal args"}`
	}
	var op *mysloodsiewnia.Op
	if tokenID == "" || tokenID == ownerSlug {
		op = q.Enqueue(kind, raw)
	} else {
		op = q.EnqueueScoped(kind, raw, tokenID, scopes)
	}
	completed, ok := q.WaitFor(op.ID, bridgeWaitTimeout)
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
