package v2

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// bridgeWaitTimeout mirrors the v1 constant — v1↔v2 parity depends on both
// paths waiting the same amount before returning vault_timeout.
const bridgeWaitTimeout = 20 * time.Second

// unauthorizedText is what bridge tools return without a valid owner token.
// Same string as v1 so parity_test passes.
const unauthorizedText = "Unauthorized — mysloodsiewnia_* tools require Authorization: Bearer <edit token>."

// ── mysloodsiewnia_status ───────────────────────────────────────────────────

func registerMysloodsiewniaStatus(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_status",
		Description: "Read-only liveness probe on kapoost's local vault (mysłoodsiewnia). Returns {status: online|degraded|offline, last_seen, commit_sha, personas_updated_at, skills_updated_at}. Offline is a stable state — retry later, don't escalate. Owner-only via Authorization: Bearer.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult(unauthorizedText), nil
		}
		snap := currentSnapshot(src)
		return textResult(renderBridgeStatus(snap)), nil
	})
}

// ── mysloodsiewnia_search ───────────────────────────────────────────────────

func registerMysloodsiewniaSearch(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_search",
		Description: "Full-text search (BM25 over SQLite FTS5) on kapoost's local vault corpus. Owner-only. Returns {status, results:[{source, type, body, doc_slug, title, page, citation}], summary}. Vault offline ⇒ {status:offline} — do not retry immediately.",
		InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"integer"},"doc_type":{"type":"string"},"doc_slug":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
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
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		if args.Limit <= 0 || args.Limit > 20 {
			args.Limit = 5
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpSearch, args)), nil
	})
}

// ── mysloodsiewnia_list ─────────────────────────────────────────────────────

func registerMysloodsiewniaList(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_list",
		Description: "Enumerate vault documents without FTS — for browsing by type or paginating. Owner-only. Args: {doc_type?: string filter (note/pdf/literatura/calendar_event/...), limit?: int 1-200 default 50, offset?: int default 0}. Returns [{slug, title, doc_type, created_at, chunk_count}]. Vault offline ⇒ {status:offline}.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"doc_type":{"type":"string"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
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
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpList, args)), nil
	})
}

// ── mysloodsiewnia_get ──────────────────────────────────────────────────────

func registerMysloodsiewniaGet(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "mysloodsiewnia_get",
		Description: "Fetch one document from kapoost's vault by slug. Owner-only. Returns the full body + metadata; vault offline ⇒ {status:offline}.",
		InputSchema: json.RawMessage(`{"type":"object","required":["doc_slug"],"properties":{"doc_slug":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
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
		if text, stop := gate(src); stop {
			return textResult(text), nil
		}
		return textResult(enqueueAndWait(src, mysloodsiewnia.OpGet, args)), nil
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

func enqueueAndWait(src Source, kind mysloodsiewnia.OpKind, args any) string {
	q := src.BridgeQueue()
	if q == nil {
		return `{"status":"internal_error","error":"bridge queue not configured"}`
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return `{"status":"internal_error","error":"failed to marshal args"}`
	}
	op := q.Enqueue(kind, raw)
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
