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

const gateWitnessBody = "arg-channel gate witness body"

// TestSessionTokenArgumentChannel pins the fallback that lets clients
// without per-call header control reach session-gated tools.
//
// The Bearer channel assumes the caller can set Authorization per tool
// call. Claude Code cannot — its MCP headers are fixed at registration —
// so an agent could bootstrap successfully and still be told "Full prompt
// available after bootstrap_session" by every gated tool, with
// re-bootstrapping as its only (useless) recourse. The `session_token`
// argument closes that loop.
//
// The negative cases matter as much as the positive one: the argument must
// be a second *channel* to the same validation, never a second, weaker
// gate. If any of these start passing content to an unauthenticated
// caller, the arg channel has become a bypass.
func TestSessionTokenArgumentChannel(t *testing.T) {
	h, cfg := gateFixture(t)
	token := mcp.GenerateSessionToken(cfg.SessionSecret)
	if token == "" {
		t.Fatal("fixture produced no session token")
	}

	cases := []struct {
		name       string
		args       map[string]any
		headers    map[string]string
		wantBody   bool
		wantInText string
	}{
		{
			name:       "anonymous stays gated",
			args:       map[string]any{"slug": "gate-witness"},
			wantBody:   false,
			wantInText: "Full prompt available after bootstrap_session",
		},
		{
			name:     "valid token as argument unlocks",
			args:     map[string]any{"slug": "gate-witness", "session_token": token},
			wantBody: true,
		},
		{
			name:     "valid token as Bearer header still unlocks",
			args:     map[string]any{"slug": "gate-witness"},
			headers:  map[string]string{"Authorization": "Bearer " + token},
			wantBody: true,
		},
		{
			name:     "argument channel works even when the header is garbage",
			args:     map[string]any{"slug": "gate-witness", "session_token": token},
			headers:  map[string]string{"Authorization": "Bearer not-a-token"},
			wantBody: true,
		},
		{
			name:       "forged token argument is rejected",
			args:       map[string]any{"slug": "gate-witness", "session_token": "9999999999.deadbeef"},
			wantBody:   false,
			wantInText: "Full prompt available after bootstrap_session",
		},
		{
			name:       "token signed with a different secret is rejected",
			args:       map[string]any{"slug": "gate-witness", "session_token": mcp.GenerateSessionToken("some-other-secret")},
			wantBody:   false,
			wantInText: "Full prompt available after bootstrap_session",
		},
		{
			name:       "empty token argument is not a free pass",
			args:       map[string]any{"slug": "gate-witness", "session_token": ""},
			wantBody:   false,
			wantInText: "Full prompt available after bootstrap_session",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callV2Tool(t, h, "get_persona", c.args, c.headers)
			hasBody := strings.Contains(got, gateWitnessBody)
			if hasBody != c.wantBody {
				t.Errorf("body visible = %v, want %v\ngot: %q", hasBody, c.wantBody, got)
			}
			if c.wantInText != "" && !strings.Contains(got, c.wantInText) {
				t.Errorf("missing %q in: %q", c.wantInText, got)
			}
		})
	}
}

// The argument is advertised on every gated tool, not just get_persona —
// an agent that learns it from one tool will reach for it on the rest, and
// a tool that silently ignores it looks like an expired session.
// The schema is only half the contract: both the server instructions and the
// bootstrap preamble enumerate the tools that take the argument, and an agent
// following the enumeration literally calls anything missing from it without a
// token. prepare_narada was gated for a whole release while absent from both
// lists. This pins the enumerations against the schemas, so the next gated
// tool cannot be added to one and forgotten in the others.
func TestSessionTokenEnumerationsMatchSchemas(t *testing.T) {
	h, cfg := gateFixture(t)
	listed := callV2ToolsList(t, h)

	// Both texts mention plenty of tool names elsewhere, so the assertion has
	// to look inside the enumeration itself — otherwise it passes on any
	// unrelated mention and pins nothing.
	instructions := between(t,
		mcp.RenderServerInstructions("example.test", 42, 20, 30),
		"ARGUMENT to any session-gated tool", "Never re-run bootstrap_session")
	preamble := between(t,
		callV2Tool(t, h, "bootstrap_session", map[string]any{"code": cfg.SessionSecret}, nil),
		"ARGUMENT instead", "Do NOT re-bootstrap")

	for tool, schema := range listed {
		if !strings.Contains(schema, "session_token") {
			continue
		}
		if !strings.Contains(instructions, tool) {
			t.Errorf("%s takes session_token but is missing from the enumeration in RenderServerInstructions:\n%s", tool, instructions)
		}
		if !strings.Contains(preamble, tool) {
			t.Errorf("%s takes session_token but is missing from the enumeration in the bootstrap preamble:\n%s", tool, preamble)
		}
	}
}

// between returns the slice of text between two markers, failing the test if
// either marker moved — a silently empty slice would make the assertion above
// vacuously true, which is the failure mode it exists to prevent.
func between(t *testing.T, text, start, end string) string {
	t.Helper()
	i := strings.Index(text, start)
	if i < 0 {
		t.Fatalf("start marker %q not found — the enumeration was reworded", start)
	}
	rest := text[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("end marker %q not found — the enumeration was reworded", end)
	}
	return rest[:j]
}

func TestSessionTokenArgumentAdvertisedOnGatedTools(t *testing.T) {
	h, _ := gateFixture(t)
	listed := callV2ToolsList(t, h)
	for _, tool := range []string{
		"get_persona", "get_skill", "load_skill_group",
		"remember", "recall", "list_collection", "read_collection_item",
		"prepare_narada",
	} {
		schema, ok := listed[tool]
		if !ok {
			t.Errorf("tool %s missing from tools/list", tool)
			continue
		}
		if !strings.Contains(schema, "session_token") {
			t.Errorf("tool %s does not advertise session_token: %s", tool, schema)
		}
	}
	// Owner-gated tools stay header-only on purpose: their credential is
	// the long-lived EDIT_TOKEN, and accepting it as an argument would put
	// it in agent transcripts and tool-call logs.
	for _, tool := range []string{"upsert_skill", "delete_skill", "get_persona_journal"} {
		if schema, ok := listed[tool]; ok && strings.Contains(schema, "session_token") {
			t.Errorf("owner-gated tool %s must not accept session_token: %s", tool, schema)
		}
	}
}

// run_narada advertises the explicit persona override, and the reply names
// which selection mode fired. Agents act on this text — one that cannot
// tell "the router chose these" from "you chose these" has no way to know
// the override exists when the router picks badly.
func TestRunNaradaAdvertisesPersonaOverride(t *testing.T) {
	h, _ := gateFixture(t)
	listed := callV2ToolsList(t, h)
	schema, ok := listed["run_narada"]
	if !ok {
		t.Fatal("run_narada missing from tools/list")
	}
	if !strings.Contains(schema, `"personas"`) {
		t.Errorf("run_narada does not advertise personas: %s", schema)
	}

	routed := callV2Tool(t, h, "run_narada", map[string]any{"context": "accessibility review"}, nil)
	if !strings.Contains(routed, "chosen by the keyword manifest") {
		t.Errorf("routed narada does not name its selection mode: %q", routed)
	}
	if !strings.Contains(routed, "personas=") {
		t.Errorf("routed narada does not point at the override: %q", routed)
	}

	explicit := callV2Tool(t, h, "run_narada", map[string]any{
		"context": "accessibility review", "personas": []string{"gate-witness"},
	}, nil)
	if !strings.Contains(explicit, "taken from your `personas` argument") {
		t.Errorf("explicit narada does not name its selection mode: %q", explicit)
	}
	if !strings.Contains(explicit, "1 personas selected: gate-witness") {
		t.Errorf("explicit narada did not seat the requested persona: %q", explicit)
	}
}

// callV2ToolsList posts a tools/list envelope and returns tool name →
// serialized inputSchema. Asserting on the advertised schema (rather than
// on handler internals) is the point: the schema is what the agent reads,
// and an argument the server honours but never advertises is invisible.
func callV2ToolsList(t *testing.T, h http.Handler) map[string]string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "schema_test", "version": "1"},
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/list")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	raw := rec.Body.String()
	for _, prefix := range []string{"event: message\n", "data: "} {
		raw = strings.Replace(raw, prefix, "", 1)
	}
	var env struct {
		Result *struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &env); err != nil {
		t.Fatalf("unmarshal tools/list: %v — body: %s", err, raw)
	}
	if env.Error != nil {
		t.Fatalf("tools/list error: %s", env.Error.Message)
	}
	if env.Result == nil {
		t.Fatal("tools/list returned no result")
	}
	out := map[string]string{}
	for _, tool := range env.Result.Tools {
		out[tool.Name] = string(tool.InputSchema)
	}
	return out
}

func gateFixture(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"personas", "skills", "blobs", "collections", "provenance", "messages", "questions", "memory", "journals", "rituals", "stats"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	persona := "---\nslug: gate-witness\ntitle: Gate Witness\nrole: test fixture\ntags: [test]\n---\n" + gateWitnessBody
	if err := os.WriteFile(filepath.Join(dir, "personas", "gate-witness.md"), []byte(persona), 0o644); err != nil {
		t.Fatalf("write persona: %v", err)
	}
	manifest := `{"type":"narada","default_personas":["gate-witness"],"min_personas":1,"max_personas":5,
	  "keyword_routes":[{"keywords":["accessibility"],"personas":["gate-witness"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "rituals", "narada.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	cfg := &config.Config{
		AuthorName:    "test",
		Domain:        "test.example",
		ContentDir:    dir,
		EditToken:     "testtoken",
		SessionSecret: "gate-fixture-secret",
	}
	worker := rituals.New(cfg)
	backend := mcp.NewBackend(cfg, content.NewStore(dir), auth.New("testtoken"), worker)
	return v2.New(cfg, backend), cfg
}
