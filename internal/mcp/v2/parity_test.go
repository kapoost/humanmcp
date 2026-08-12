package v2_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	v2 "github.com/kapoost/humanmcp-go/internal/mcp/v2"
	"github.com/kapoost/humanmcp-go/internal/rituals"
)

// TestV2ParityWithLegacy calls every migrated tool on both /mcp (legacy) and
// /mcp/v2 (SDK-based) with identical arguments and asserts the extracted
// text content is byte-identical. Guards against drift during Faza 3
// tool-by-tool migration — if a v2 refactor accidentally reorders a
// strings.Builder, this test catches it before Faza 5 cutover.
//
// Uses a temp content dir with three pieces, one persona, one skill so
// both empty and populated code paths get exercised.
func TestV2ParityWithLegacy(t *testing.T) {
	dir := seedTestContent(t)

	cfg := &config.Config{
		AuthorName: "testuser",
		AuthorBio:  "test bio",
		Domain:     "localhost",
		ContentDir: dir,
	}
	store := content.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("store load: %v", err)
	}
	worker := rituals.New(cfg)
	legacy := mcp.NewHandler(cfg, store, auth.New("testtoken"), worker)

	// Seed a public collection item + a public blob so list_collection,
	// read_collection_item, list_blobs, read_blob exercise populated paths.
	if _, err := legacy.CollectionStore().Save(content.CollectionItem{
		Slug:            "test-drawing",
		Title:           "Test Drawing",
		OriginalCreator: "Some Artist",
		Year:            1970,
		Access:          "public",
		AddedAt:         time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
	if err := legacy.BlobStore().Save(&content.Blob{
		Slug:      "test-contact",
		Title:     "Test Contact",
		BlobType:  content.BlobContact,
		Access:    content.AccessPublic,
		TextData:  `{"email":"test@example.com"}`,
		Published: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	v2h := v2.New(cfg, legacy)

	cases := []struct {
		name    string
		tool    string
		args    map[string]any
		headers map[string]string // extra HTTP headers (e.g. Authorization for owner-mode)
	}{
		{"about_humanmcp", "about_humanmcp", nil, nil},
		{"get_author_profile", "get_author_profile", nil, nil},
		{"list_content_all", "list_content", nil, nil},
		{"list_content_poem", "list_content", map[string]any{"type": "poem"}, nil},
		{"list_content_tag_sea", "list_content", map[string]any{"tag": "sea"}, nil},
		{"list_content_none", "list_content", map[string]any{"type": "nonexistent"}, nil},
		{"list_personas", "list_personas", nil, nil},
		{"list_skills", "list_skills", nil, nil},
		{"list_skills_tag_test", "list_skills", map[string]any{"tag": "test"}, nil},
		{"read_content_public", "read_content", map[string]any{"slug": "public"}, nil},
		{"read_content_locked", "read_content", map[string]any{"slug": "locked"}, nil},
		{"read_content_missing", "read_content", map[string]any{"slug": "nope"}, nil},
		{"verify_content_public", "verify_content", map[string]any{"slug": "public"}, nil},
		{"verify_content_missing", "verify_content", map[string]any{"slug": "nope"}, nil},
		{"get_certificate_public", "get_certificate", map[string]any{"slug": "public"}, nil},
		{"get_certificate_missing", "get_certificate", map[string]any{"slug": "nope"}, nil},
		{"list_provenance_missing", "list_provenance", map[string]any{"slug": "nope"}, nil},
		{"read_provenance_missing", "read_provenance", map[string]any{"slug": "a", "id": "b"}, nil},

		{"list_collection", "list_collection", nil, nil},
		{"read_collection_item_public", "read_collection_item", map[string]any{"slug": "test-drawing"}, nil},
		{"read_collection_item_missing", "read_collection_item", map[string]any{"slug": "nope"}, nil},

		{"list_blobs", "list_blobs", nil, nil},
		{"list_blobs_type_filter", "list_blobs", map[string]any{"blob_type": "contact"}, nil},
		{"list_blobs_type_none", "list_blobs", map[string]any{"blob_type": "vector"}, nil},
		{"read_blob_public", "read_blob", map[string]any{"slug": "test-contact"}, nil},
		{"read_blob_missing", "read_blob", map[string]any{"slug": "nope"}, nil},

		{"list_skill_groups", "list_skill_groups", nil, nil},
		{"load_skill_group_test", "load_skill_group", map[string]any{"name": "test"}, nil},
		{"load_skill_group_empty", "load_skill_group", map[string]any{"name": "nonexistent"}, nil},
		{"load_skill_group_missing_arg", "load_skill_group", map[string]any{}, nil},

		{"suggest_skills_humanmcp", "suggest_skills", map[string]any{
			"files":      []string{"go.mod", "Dockerfile"},
			"languages":  []string{"go"},
			"git_origin": "github.com/kapoost/humanmcp",
		}, nil},
		{"suggest_skills_empty", "suggest_skills", map[string]any{}, nil},

		{"request_access_public", "request_access", map[string]any{"slug": "public"}, nil},
		{"request_access_locked_challenge", "request_access", map[string]any{"slug": "locked"}, nil},
		{"request_access_missing", "request_access", map[string]any{"slug": "nope"}, nil},

		{"submit_answer_wrong", "submit_answer", map[string]any{"slug": "locked", "answer": "five"}, nil},
		{"submit_answer_right", "submit_answer", map[string]any{"slug": "locked", "answer": "four"}, nil},

		{"leave_comment", "leave_comment", map[string]any{"slug": "public", "text": "beautiful", "from": "parity_test"}, nil},
		{"leave_message_with_contact", "leave_message", map[string]any{
			"text": "I would like to reprint one poem.", "context": "requesting reprint permission",
			"contact": "test@example.com", "from": "parity_test",
		}, nil},
		{"leave_message_anonymous", "leave_message", map[string]any{
			"text": "no reply needed.", "context": "one-way note",
		}, nil},

		{"request_license_ccby", "request_license", map[string]any{
			"slug": "public", "intended_use": "share on my blog", "caller_id": "test-agent",
		}, nil},
		{"request_license_commercial_intent", "request_license", map[string]any{
			"slug": "public", "intended_use": "commercial training corpus", "caller_id": "corp-x",
		}, nil},

		// memory — session-gated. Both v1 (no Mcp-Session-Id in tests) and v2
		// (stateless SDK strips it anyway) reject as anonymous. Recall-with-code
		// exists just to lock the "requires session" text; the "found N mems"
		// branch stays untested until bootstrap_session redesign lands.
		{"remember_anonymous", "remember", map[string]any{"text": "note", "code": "abc"}, nil},
		{"recall_anonymous", "recall", map[string]any{"code": "abc"}, nil},

		// editing — owner-gated by Authorization: Bearer testtoken. Also test
		// anonymous rejection path so both success + failure branches parity.
		{"upsert_skill_anonymous", "upsert_skill", map[string]any{
			"slug": "new-skill", "category": "tech", "title": "New", "body": "body",
		}, nil},
		{"upsert_skill_owner", "upsert_skill", map[string]any{
			"slug": "v2-created", "category": "tech", "title": "V2 Created", "body": "hello",
		}, map[string]string{"Authorization": "Bearer testtoken"}},
		{"delete_skill_anonymous", "delete_skill", map[string]any{"slug": "test-skill"}, nil},
		{"delete_skill_missing_owner", "delete_skill", map[string]any{"slug": "nonexistent"},
			map[string]string{"Authorization": "Bearer testtoken"}},

		// team — bootstrap validates code, get_persona/get_skill session-gated.
		// Test config has no POET_POOL and no SESSION_SECRET, so every code
		// fails validation → deterministic "niepoprawne hasło" branch.
		{"bootstrap_session_missing_code", "bootstrap_session", map[string]any{}, nil},
		{"bootstrap_session_wrong_code", "bootstrap_session", map[string]any{"code": "not a real fragment"}, nil},

		{"get_persona_hodor", "get_persona", map[string]any{"slug": "hodor"}, nil},
		{"get_persona_missing_slug", "get_persona", map[string]any{}, nil},
		{"get_persona_not_found", "get_persona", map[string]any{"slug": "nobody"}, nil},

		{"get_skill_test_gated", "get_skill", map[string]any{"slug": "test-skill"}, nil},
		{"get_skill_missing_slug", "get_skill", map[string]any{}, nil},
		{"get_skill_not_found", "get_skill", map[string]any{"slug": "nope"}, nil},

		// dialogue — ask_human creates a Question, fetch_answer polls.
		// Both hit real stores; the reply body is nondeterministic on ID +
		// timestamp so normalizeVolatile takes care of it. fetch_answer with
		// a missing ID exercises the "no question" branch.
		{"ask_human", "ask_human", map[string]any{
			"question": "co byś zrobił w tej sytuacji?", "context": "parity_test", "from": "test-agent",
		}, nil},
		{"fetch_answer_missing_id", "fetch_answer", map[string]any{"id": "nope"}, nil},

		// rituals — narada manifest is optional; v1 and v2 both surface the
		// same "manifest not found" text so this drives the error branch.
		// LLM-dependent tools (record_persona_reflection, synthesise_*) test
		// only the "no API key" branch since the test config leaves ClaudeAPIKey
		// empty.
		{"run_narada_no_context", "run_narada", map[string]any{}, nil},
		{"run_narada_no_manifest", "run_narada", map[string]any{"context": "test context"}, nil},

		{"fetch_narada_missing_id", "fetch_narada_result", map[string]any{}, nil},
		{"fetch_narada_not_found", "fetch_narada_result", map[string]any{"id": "nonexistent"}, nil},

		{"journal_anonymous", "get_persona_journal", map[string]any{"slug": "hodor"}, nil},
		{"journal_owner_empty", "get_persona_journal", map[string]any{"slug": "hodor"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"journal_missing_slug", "get_persona_journal", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},

		{"reflection_anonymous", "record_persona_reflection", map[string]any{
			"narada_id": "x", "persona_slug": "hodor", "error_context": "y",
		}, nil},
		{"reflection_missing_args", "record_persona_reflection", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"reflection_no_llm", "record_persona_reflection", map[string]any{
			"narada_id": "x", "persona_slug": "hodor", "error_context": "y",
		}, map[string]string{"Authorization": "Bearer testtoken"}},

		{"synthesise_anonymous", "synthesise_persona_patterns", map[string]any{"slug": "hodor"}, nil},
		{"synthesise_missing_slug", "synthesise_persona_patterns", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"synthesise_no_llm", "synthesise_persona_patterns", map[string]any{"slug": "hodor"},
			map[string]string{"Authorization": "Bearer testtoken"}},

		// mysłoodsiewnia bridge — parity_test never calls SetBridge, so
		// Liveness() is nil and Status() collapses to StatusUnreachable
		// deterministically. Both v1 and v2 must produce byte-identical
		// offline JSON and identical unauthorized text.
		{"mysloodsiewnia_status_anonymous", "mysloodsiewnia_status", map[string]any{}, nil},
		{"mysloodsiewnia_status_owner_offline", "mysloodsiewnia_status", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_search_anonymous", "mysloodsiewnia_search", map[string]any{"query": "morze"}, nil},
		{"mysloodsiewnia_search_owner_offline", "mysloodsiewnia_search", map[string]any{"query": "morze"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_search_owner_no_query", "mysloodsiewnia_search", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_get_anonymous", "mysloodsiewnia_get", map[string]any{"doc_slug": "x"}, nil},
		{"mysloodsiewnia_get_owner_offline", "mysloodsiewnia_get", map[string]any{"doc_slug": "x"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_get_owner_no_slug", "mysloodsiewnia_get", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},

		// mysloodsiewnia_list (wave 1.5)
		{"mysloodsiewnia_list_anonymous", "mysloodsiewnia_list", map[string]any{}, nil},
		{"mysloodsiewnia_list_owner_offline_no_args", "mysloodsiewnia_list", map[string]any{},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_list_owner_offline_with_filter", "mysloodsiewnia_list",
			map[string]any{"doc_type": "note", "limit": 10, "offset": 5},
			map[string]string{"Authorization": "Bearer testtoken"}},

		// mysloodsiewnia_write (wave 2). Owner-only ingest. Each variant
		// exercises a distinct step in the precedence chain so v1↔v2 drift
		// in any of them surfaces as a specific-named failure.
		{"mysloodsiewnia_write_anonymous", "mysloodsiewnia_write",
			map[string]any{"doc_type": "note", "title": "t", "body": "b"}, nil},
		{"mysloodsiewnia_write_owner_missing_doc_type", "mysloodsiewnia_write",
			map[string]any{"title": "t", "body": "b"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_write_owner_missing_title", "mysloodsiewnia_write",
			map[string]any{"doc_type": "note", "body": "b"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_write_owner_missing_body", "mysloodsiewnia_write",
			map[string]any{"doc_type": "note", "title": "t"},
			map[string]string{"Authorization": "Bearer testtoken"}},
		{"mysloodsiewnia_write_owner_offline", "mysloodsiewnia_write",
			map[string]any{"doc_type": "note", "title": "t", "body": "b"},
			map[string]string{"Authorization": "Bearer testtoken"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v1Text := normalizeVolatile(callLegacy(t, legacy, c.tool, c.args, c.headers))
			v2Text := normalizeVolatile(callV2(t, v2h, c.tool, c.args, c.headers))
			if v1Text != v2Text {
				t.Errorf("parity drift on %s:\n--- v1 ---\n%s\n--- v2 ---\n%s", c.tool, v1Text, v2Text)
			}
		})
	}
}

func callLegacy(t *testing.T, h http.Handler, tool string, args map[string]any, headers map[string]string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return extractText(t, "v1", rec.Body.String())
}

func callV2(t *testing.T, h http.Handler, tool string, args map[string]any, headers map[string]string) string {
	t.Helper()
	params := map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "parity_test", "version": "1"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		},
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/v2", bytes.NewReader(body))
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
	// Streamable HTTP returns text/event-stream by default. Peel the
	// "event: message\ndata: ..." envelope.
	raw := rec.Body.String()
	for _, prefix := range []string{"event: message\n", "data: "} {
		raw = strings.Replace(raw, prefix, "", 1)
	}
	raw = strings.TrimSpace(raw)
	return extractText(t, "v2", raw)
}

// normalizeVolatile strips fields that MUST differ between the v1 and v2
// calls: MessageStore assigns a nanosecond ID and captures time.Now()
// per invocation, so leave_comment/leave_message/request_license bodies
// always drift on those exact bytes. Everything structural is preserved.
var (
	nanoIDRE      = regexp.MustCompile(`\b\d{16,}\b`)
	humanTimeRE   = regexp.MustCompile(`\d{1,2} [A-Z][a-z]+ \d{4}, \d{2}:\d{2} UTC`)
	machineTimeRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)
	// QuestionStore.uniqueID: YYYYMMDD-HHMM-<slug-of-question>. Between the
	// v1 and v2 calls the minute can roll over, giving different HHMM and
	// therefore different IDs — mask so structural drift still fails.
	questionIDRE = regexp.MustCompile(`\d{8}-\d{4}-[a-z0-9-]+`)
	// Bare date "asked=2026-07-30" in ask_human's reply — same day OK, but
	// safest to normalise since a midnight-boundary run would drift.
	bareDateRE = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
)

func normalizeVolatile(s string) string {
	s = nanoIDRE.ReplaceAllString(s, "<ID>")
	s = humanTimeRE.ReplaceAllString(s, "<TIME>")
	s = machineTimeRE.ReplaceAllString(s, "<TIME>")
	s = questionIDRE.ReplaceAllString(s, "<QID>")
	s = bareDateRE.ReplaceAllString(s, "<DATE>")
	return s
}

func extractText(t *testing.T, tag, body string) string {
	t.Helper()
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
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("%s: unmarshal envelope: %v\nbody: %s", tag, err, body)
	}
	if env.Error != nil {
		return "ERR: " + env.Error.Message
	}
	if env.Result == nil || len(env.Result.Content) == 0 {
		return ""
	}
	return env.Result.Content[0].Text
}

func seedTestContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustDir := func(sub string) string {
		p := filepath.Join(dir, sub)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		return p
	}

	must("public.md", `---
slug: public
title: Public Poem
type: poem
access: public
tags: [test, sea]
published: 2024-01-01
---
Hello world.`)

	must("locked.md", `---
slug: locked
title: Locked Poem
type: poem
access: locked
gate: challenge
challenge: What is 2+2?
answer: four
description: A locked piece.
published: 2024-02-15
---
The secret.`)

	personaDir := mustDir("personas")
	must(filepath.Join(personaDir, "hodor.md")[len(dir)+1:], `---
slug: hodor
title: Hodor
role: guardian
tags: [safety]
---
Hodor.`)

	skillDir := mustDir("skills")
	skill := map[string]any{
		"slug":     "test-skill",
		"category": "tech",
		"title":    "Test Skill",
		"body":     "Do the thing.",
		"tags":     []string{"test", "dev"},
	}
	sb, _ := json.Marshal(skill)
	must(filepath.Join(skillDir, "test-skill.json")[len(dir)+1:], string(sb))

	// Ensure MkdirAll runs so Store.Load sees the layout even for content
	// types not exercised by any assertion (blobs, collections, etc).
	for _, sub := range []string{"blobs", "collections", "provenance", "messages", "questions", "memory", "journals", "rituals", "stats"} {
		mustDir(sub)
	}

	return dir
}
