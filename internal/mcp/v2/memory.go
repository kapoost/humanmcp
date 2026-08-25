package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── remember ────────────────────────────────────────────────────────────────

func registerRemember(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "remember",
		Description: "Persist a note under the given session code. Session-gated — bootstrap_session first. Callers sharing the code share the memory.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"code":{"type":"string"},"from":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}},` + sessionTokenSchemaProp + `},"required":["text","code"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !sessionActiveOrToken(src, req) {
			return textResult("remember requires an active session. Call bootstrap_session first."), nil
		}
		var a struct {
			Text string   `json:"text"`
			Code string   `json:"code"`
			From string   `json:"from"`
			Tags []string `json:"tags"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		m, err := src.MemoryStore().Save(a.Code, a.From, a.Text, a.Tags)
		if err != nil {
			return textResult("Could not store memory: " + err.Error()), nil
		}
		reply := fmt.Sprintf("Memory stored.\n\nID: %s\nAt: %s\n\nUse recall(code=%q) in a future session to retrieve.",
			m.ID, m.CreatedAt.Format("2 January 2006, 15:04 UTC"), a.Code)
		return textResult(reply), nil
	})
}

// ── recall ──────────────────────────────────────────────────────────────────

func registerRecall(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "recall",
		Description: "List memories saved under the given code, optionally substring-filtered by query. Session-gated for symmetry with remember.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"},` + sessionTokenSchemaProp + `},"required":["code"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !sessionActiveOrToken(src, req) {
			return textResult("recall requires an active session. Call bootstrap_session first."), nil
		}
		var a struct {
			Code  string `json:"code"`
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		mems, err := src.MemoryStore().Recall(a.Code, a.Query, a.Limit)
		if err != nil {
			return textResult("Could not recall: " + err.Error()), nil
		}
		if len(mems) == 0 {
			return textResult("No memories under that code (or no match for the query)."), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d memorie(s):\n\n", len(mems))
		for _, m := range mems {
			fmt.Fprintf(&b, "— %s (%s)\n", m.ID, m.CreatedAt.Format("2 January 2006, 15:04 UTC"))
			if m.From != "" {
				fmt.Fprintf(&b, "  from: %s\n", m.From)
			}
			if len(m.Tags) > 0 {
				fmt.Fprintf(&b, "  tags: %s\n", strings.Join(m.Tags, ", "))
			}
			fmt.Fprintf(&b, "  %s\n\n", m.Body)
		}
		return textResult(b.String()), nil
	})
}

// sessionActive is the shared gate check used by session-scoped tools.
// Extracted so the "always false on stateless v2" semantics live in one place.
func sessionActive(src Source, req *sdk.CallToolRequest) bool {
	if req == nil || req.Extra == nil {
		return false
	}
	return src.IsSessionActiveByHeaders(req.Extra.Header)
}

// sessionActiveOrToken is sessionActive plus an inline-argument fallback.
//
// The Bearer channel assumes the client can set a per-tool-call
// Authorization header. Several MCP clients — Claude Code among them —
// cannot: headers are fixed at server-registration time, so the
// SESSION_TOKEN that bootstrap_session emits has no way back to the
// server. The visible symptom is an agent that bootstraps successfully
// and still gets "Full prompt available after bootstrap_session" from
// get_persona, then re-bootstraps in a loop.
//
// So every session-gated tool also accepts `session_token` as an
// argument. Validation deliberately re-wraps the value as a synthetic
// Authorization header instead of calling ValidateSessionToken directly:
// the two channels then share one code path and cannot drift apart (a
// header-only tightening would otherwise silently skip the arg channel).
func sessionActiveOrToken(src Source, req *sdk.CallToolRequest) bool {
	if sessionActive(src, req) {
		return true
	}
	if req == nil {
		return false
	}
	var a struct {
		SessionToken string `json:"session_token"`
	}
	if len(req.Params.Arguments) > 0 {
		_ = json.Unmarshal(req.Params.Arguments, &a)
	}
	token := strings.TrimSpace(a.SessionToken)
	if token == "" {
		return false
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	return src.IsSessionActiveByHeaders(hdr)
}

// sessionTokenSchemaProp is the shared `session_token` property blob spliced
// into every session-gated tool's InputSchema. Kept as one constant so a
// wording change lands on all of them at once — an agent that learns the
// argument from one tool must find it identical on the rest.
const sessionTokenSchemaProp = `"session_token":{"type":"string","description":"Optional. The SESSION_TOKEN emitted by bootstrap_session. Pass it here when your client cannot set a per-call Authorization: Bearer header."}`
