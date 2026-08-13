package v2_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	v2 "github.com/kapoost/humanmcp-go/internal/mcp/v2"
	"github.com/kapoost/humanmcp-go/internal/rituals"
)

// TestV2ResponseShapes pins the exact reply text for a handful of error
// paths that storyboards can't easily assert byte-for-byte. Replaces the
// v1↔v2 parity_test — same failure class (silent drift in owner-gated
// wording, arg-missing text, and mysloodsiewnia JSON envelope shape),
// no v1 oracle needed. Narada nar-675785d803c2 recommended ~15 cases
// focused on error paths (Axel-brandt).
//
// When a case fails: update the expected string here in the same PR
// that changes the tool body, so the wording change is reviewed as an
// intentional agent-visible message change.
func TestV2ResponseShapes(t *testing.T) {
	dir := t.TempDir()
	// Minimal seed so the server can construct without panicking on
	// missing directories. Every tool tested here fails BEFORE reading
	// content, so seeded values are irrelevant.
	for _, sub := range []string{"personas", "skills", "blobs", "collections", "provenance", "messages", "questions", "memory", "journals", "rituals", "stats"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	cfg := &config.Config{
		AuthorName: "test",
		Domain:     "test.example",
		ContentDir: dir,
		EditToken:  "testtoken", // matches the Bearer used in owner-gated cases below
	}
	worker := rituals.New(cfg)
	backend := mcp.NewBackend(cfg, content.NewStore(dir), auth.New("testtoken"), worker)
	h := v2.New(cfg, backend)

	cases := []struct {
		name    string
		tool    string
		args    map[string]any
		headers map[string]string
		want    string
	}{
		// Owner-gated tools — anonymous rejection wording must stay
		// stable so agents can pattern-match on it.
		{
			name: "upsert_skill_anonymous",
			tool: "upsert_skill",
			args: map[string]any{"slug": "x", "category": "tech", "title": "T", "body": "B"},
			want: "Unauthorized — requires agent token in Authorization: Bearer <token> header.",
		},
		{
			name: "delete_skill_anonymous",
			tool: "delete_skill",
			args: map[string]any{"slug": "x"},
			want: "Unauthorized — requires agent token in Authorization: Bearer <token> header.",
		},
		{
			name: "journal_anonymous",
			tool: "get_persona_journal",
			args: map[string]any{"slug": "hodor"},
			want: "Unauthorized — get_persona_journal requires an owner token.",
		},
		{
			name: "reflection_anonymous",
			tool: "record_persona_reflection",
			args: map[string]any{"narada_id": "x", "persona_slug": "hodor", "error_context": "y"},
			want: "Unauthorized — record_persona_reflection requires an owner token.",
		},
		{
			name: "synthesise_anonymous",
			tool: "synthesise_persona_patterns",
			args: map[string]any{"slug": "hodor"},
			want: "Unauthorized — synthesise_persona_patterns requires an owner token.",
		},
		// LLM-unavailable stubs (owner-authenticated, no API key).
		{
			name:    "reflection_no_llm",
			tool:    "record_persona_reflection",
			args:    map[string]any{"narada_id": "x", "persona_slug": "hodor", "error_context": "y"},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    "LLM unavailable (no API key). Reflection not written.",
		},
		{
			name:    "synthesise_no_llm",
			tool:    "synthesise_persona_patterns",
			args:    map[string]any{"slug": "hodor"},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    "LLM unavailable (no API key). Synthesis skipped.",
		},
		// mysłoodsiewnia envelopes — the JSON shape is what friend
		// tokens key off, so a stray space or reordered key breaks
		// contract with external callers.
		{
			name:    "mysloodsiewnia_write_owner_missing_doc_type",
			tool:    "mysloodsiewnia_write",
			args:    map[string]any{"title": "t", "body": "b"},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    `{"status":"invalid_args","error":"doc_type is required"}`,
		},
		{
			name:    "mysloodsiewnia_write_owner_missing_title",
			tool:    "mysloodsiewnia_write",
			args:    map[string]any{"doc_type": "note", "body": "b"},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    `{"status":"invalid_args","error":"title is required"}`,
		},
		{
			name:    "mysloodsiewnia_write_owner_missing_body",
			tool:    "mysloodsiewnia_write",
			args:    map[string]any{"doc_type": "note", "title": "t"},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    `{"status":"invalid_args","error":"body is required"}`,
		},
		{
			name: "mysloodsiewnia_write_anonymous",
			tool: "mysloodsiewnia_write",
			args: map[string]any{"doc_type": "note", "title": "t", "body": "b"},
			want: "Unauthorized — mysloodsiewnia_* tools require Authorization: Bearer <edit token>.",
		},
		{
			name:    "mysloodsiewnia_search_owner_no_query",
			tool:    "mysloodsiewnia_search",
			args:    map[string]any{},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    `{"status":"invalid_args","error":"query is required"}`,
		},
		{
			name:    "mysloodsiewnia_get_owner_no_slug",
			tool:    "mysloodsiewnia_get",
			args:    map[string]any{},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want:    `{"status":"invalid_args","error":"doc_slug is required"}`,
		},
		// Content tool text — read_content on missing slug returns
		// a specific "not found" tail that agents surface to users.
		{
			name: "get_persona_journal_missing_slug_owner",
			tool: "get_persona_journal",
			args: map[string]any{},
			headers: map[string]string{"Authorization": "Bearer testtoken"},
			want: "slug is required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callV2Tool(t, h, c.tool, c.args, c.headers)
			if got != c.want {
				t.Errorf("response shape drift on %s:\n want: %q\n  got: %q", c.tool, c.want, got)
			}
		})
	}
}

// callV2Tool posts a tools/call envelope, strips the Streamable HTTP
// event-stream framing, and returns the first content block's text.
// Errors reply as "ERR: <msg>" so the caller can pin both branches
// with the same string field.
func callV2Tool(t *testing.T, h http.Handler, tool string, args map[string]any, headers map[string]string) string {
	t.Helper()
	params := map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "shape_test", "version": "1"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		},
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", tool)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	raw := rec.Body.String()
	for _, prefix := range []string{"event: message\n", "data: "} {
		raw = strings.Replace(raw, prefix, "", 1)
	}
	raw = strings.TrimSpace(raw)

	var env struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal: %v — body: %s", err, raw)
	}
	if env.Error != nil {
		return "ERR: " + env.Error.Message
	}
	if env.Result == nil || len(env.Result.Content) == 0 {
		return ""
	}
	return env.Result.Content[0].Text
}
