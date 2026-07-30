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
	legacy := mcp.NewHandler(cfg, store, auth.New("testtoken"))
	v2h := v2.New(cfg, legacy)

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"about_humanmcp", "about_humanmcp", nil},
		{"get_author_profile", "get_author_profile", nil},
		{"list_content_all", "list_content", nil},
		{"list_content_poem", "list_content", map[string]any{"type": "poem"}},
		{"list_content_tag_sea", "list_content", map[string]any{"tag": "sea"}},
		{"list_content_none", "list_content", map[string]any{"type": "nonexistent"}},
		{"list_personas", "list_personas", nil},
		{"list_skills", "list_skills", nil},
		{"list_skills_tag_test", "list_skills", map[string]any{"tag": "test"}},
		{"read_content_public", "read_content", map[string]any{"slug": "public"}},
		{"read_content_locked", "read_content", map[string]any{"slug": "locked"}},
		{"read_content_missing", "read_content", map[string]any{"slug": "nope"}},
		{"verify_content_public", "verify_content", map[string]any{"slug": "public"}},
		{"verify_content_missing", "verify_content", map[string]any{"slug": "nope"}},
		{"get_certificate_public", "get_certificate", map[string]any{"slug": "public"}},
		{"get_certificate_missing", "get_certificate", map[string]any{"slug": "nope"}},
		{"list_provenance_missing", "list_provenance", map[string]any{"slug": "nope"}},
		{"read_provenance_missing", "read_provenance", map[string]any{"slug": "a", "id": "b"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v1Text := callLegacy(t, legacy, c.tool, c.args)
			v2Text := callV2(t, v2h, c.tool, c.args)
			if v1Text != v2Text {
				t.Errorf("parity drift on %s:\n--- v1 ---\n%s\n--- v2 ---\n%s", c.tool, v1Text, v2Text)
			}
		})
	}
}

func callLegacy(t *testing.T, h http.Handler, tool string, args map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return extractText(t, "v1", rec.Body.String())
}

func callV2(t *testing.T, h http.Handler, tool string, args map[string]any) string {
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
