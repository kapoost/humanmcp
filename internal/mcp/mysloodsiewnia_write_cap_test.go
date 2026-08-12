package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMysloodsiewniaWriteBodyCap covers the payload arithmetic that
// storyboards can't (YAML can't inline a 100 KiB body legibly).
// Under-cap boundary (=100 KiB) MUST pass the cap check; over-cap
// (100 KiB + 1) MUST return payload_too_large with limit and got.
func TestMysloodsiewniaWriteBodyCap(t *testing.T) {
	h, _ := newTestHandler(t)
	// Owner-only tool requires cfg.EditToken to recognize Bearer token.
	// newTestHandler doesn't populate it because most tests target
	// anonymous paths.
	h.cfg.EditToken = "testtoken"

	call := func(body string) string {
		t.Helper()
		env := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "mysloodsiewnia_write",
				"arguments": map[string]any{
					"doc_type": "note",
					"title":    "cap-test",
					"body":     body,
				},
			},
		}
		raw, _ := json.Marshal(env)
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer testtoken")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
		}
		result, ok := resp["result"].(map[string]any)
		if !ok {
			t.Fatalf("no result in response: %v", resp)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("no content: %v", result)
		}
		return content[0].(map[string]any)["text"].(string)
	}

	// At-cap: no bridge configured → gate short-circuits with offline.
	// Proves the cap check let this body through.
	atCap := call(strings.Repeat("a", writeBodyCap))
	if !strings.Contains(atCap, `"status": "offline"`) {
		t.Errorf("at-cap: expected offline gate response, got: %s", atCap)
	}
	if strings.Contains(atCap, "payload_too_large") {
		t.Errorf("at-cap: unexpectedly triggered payload_too_large: %s", atCap)
	}

	// Over-cap by 1 byte: MUST return payload_too_large with limit + got.
	over := call(strings.Repeat("x", writeBodyCap+1))
	if !strings.Contains(over, `"status":"payload_too_large"`) {
		t.Errorf("over-cap: expected payload_too_large, got: %s", over)
	}
	if !strings.Contains(over, `"limit":102400`) {
		t.Errorf("over-cap: expected limit=102400, got: %s", over)
	}
	if !strings.Contains(over, `"got":102401`) {
		t.Errorf("over-cap: expected got=102401, got: %s", over)
	}
	if strings.Contains(over, "offline") {
		t.Errorf("over-cap: fell through to gate instead of failing at cap: %s", over)
	}
}

// TestMysloodsiewniaWriteFriendDenied ensures owner-only gate runs
// BEFORE rate limit — so a scoped friend token cannot exhaust its
// read budget by hammering /write. Precedence pinned by
// storyboards/mysloodsiewnia/write_friend_forbidden.yaml.
func TestMysloodsiewniaWriteOwnerGateBeforeRateLimit(t *testing.T) {
	// Note: v1 handler_test.go doesn't wire friend tokens by default.
	// This test only checks that the owner-only response shape is
	// stable; a full friend-token scenario lives in the storyboard
	// tree (write_friend_forbidden.yaml) where the storyboard runner
	// configures friend tokens on the config.
	h, _ := newTestHandler(t)

	env := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "mysloodsiewnia_write",
			"arguments": map[string]any{
				"doc_type": "note",
				"title":    "a",
				"body":     "b",
			},
		},
	}
	raw, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header → anonymous.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Unauthorized") {
		t.Errorf("anonymous write: expected Unauthorized, got: %s", body)
	}
	if strings.Contains(body, "write_denied") {
		t.Errorf("anonymous write: MUST NOT reveal write_denied shape (would leak that write tool exists in a distinct way): %s", body)
	}
}
