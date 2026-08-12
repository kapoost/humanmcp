package storyboard

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	_ "github.com/kapoost/humanmcp-go/internal/mcp/v2" // trialed Tier C mount, see comment below
	"github.com/kapoost/humanmcp-go/internal/web"
)

const testEditToken = "storyboard-test-token"

// testFriendTokens is the deterministic fixture used by wave-3 storyboards
// (storyboards/mysloodsiewnia/wave3_*.yaml). Slug names are anonymous
// (slug-a, slug-b) per repo-public hygiene — see prompts/wave3-sharing.md
// Landminy sekcja. Scopes are toy values that match the YAMLs, not real
// vault doc_types.
//
// slug-a: normal scope + generous cap — used by out-of-scope + offline gate.
// slug-b: aggressive cap (3/hr) — used by rate-limit burn test.
func testFriendTokens() map[string]*config.FriendTokenSpec {
	future := time.Now().Add(90 * 24 * time.Hour)
	now := time.Now()
	return map[string]*config.FriendTokenSpec{
		"slug-a": {
			Token:            "storyboard-friend-slug-a",
			Scopes:           []string{"literatura", "note"},
			RateLimitPerHour: 50,
			ExpiresAt:        future,
			CreatedAt:        now,
		},
		"slug-b": {
			Token:            "storyboard-friend-slug-b",
			Scopes:           []string{"literatura"},
			RateLimitPerHour: 3,
			ExpiresAt:        future,
			CreatedAt:        now,
		},
	}
}

// runHTTP boots a fresh server stack against a temp data dir, applies the
// declared setup (listings, pieces), then replays each scripted assertion.
func runHTTP(t *testing.T, sb Storyboard) {
	t.Helper()

	contentDir := t.TempDir() + "/content"
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := &config.Config{
		Host:         "127.0.0.1",
		Port:         "0",
		ContentDir:   contentDir,
		AuthorName:   "Storyboard",
		EditToken:    testEditToken,
		FriendTokens: testFriendTokens(),
		// Provide deterministic PoetPool + PoetSecret so PickActivePoem
		// returns a non-empty session code in tests. Without this, the
		// owner dashboards skip the "session" section entirely and
		// storyboards targeting that surface render nothing.
		PoetPool:   []string{"jeszcze polska nie zginęła"},
		PoetSecret: "storyboard-poet-secret",
	}
	// Fresh ed25519 keypair per storyboard so signature-verification
	// scenarios have a real root of trust. Setup pieces are auto-signed
	// when `signed: true` appears in their YAML map.
	testKey, kerr := content.GenerateKeyPair()
	if kerr != nil {
		t.Fatalf("generate keypair: %v", kerr)
	}
	cfg.SigningPrivateKey = testKey.PrivateKeyBase64()
	cfg.SigningPublicKey = testKey.PublicKeyHex()
	store := content.NewStore(contentDir)
	_ = store.Load()
	listingStore := content.NewListingStore(contentDir)
	a := auth.New(cfg.EditToken)
	h := web.NewHandler(cfg, store, a)
	mux := http.NewServeMux()
	// Mount MCP at /mcp + /mcp/ so storyboards can POST JSON-RPC
	// against tools/call without spinning up a parallel server.
	// TODO(Tier C v1→v2): storyboards currently target v1 mcpHandler.
	// A trial mount of v2 (mcpv2.New(cfg, mcpHandler)) here surfaces
	// 10 real failures: (a) 4× -32602 error-code drift (SDK wraps tool
	// errors in CallToolResult content, no JSON-RPC error object with
	// code); (b) 2× list_collection/read_collection_item missing
	// "Members-only drawing"/"Original creator" fields — v2 impl parity
	// gap vs v1; (c) 4× initialize response missing HOW USERS ADD /
	// PERSIST FOR NEXT SESSION / server instructions block — v2 SDK
	// doesn't surface them via initialize the same way v1 did. Fix in
	// three PRs (assertion updates + v2 collection port + v2 initialize
	// instructions wire-up) before flipping this mount.
	mcpHandler := mcp.NewHandler(cfg, store, a)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	// Wire the MCP tool count into the web handler so /llms.txt can
	// render the same number as prod.
	h.SetMCPToolCount(len(mcpHandler.ToolNames()))
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for _, l := range sb.Setup.Listings {
		if err := saveListing(listingStore, l); err != nil {
			t.Fatalf("setup listing %v: %v", l, err)
		}
	}
	for _, p := range sb.Setup.Pieces {
		if err := savePiece(store, p, testKey); err != nil {
			t.Fatalf("setup piece %v: %v", p, err)
		}
	}
	for _, p := range sb.Setup.Personas {
		if err := savePersona(contentDir, p); err != nil {
			t.Fatalf("setup persona %v: %v", p, err)
		}
	}
	if len(sb.Setup.Events) > 0 {
		statStore := content.NewStatStore(contentDir)
		for _, e := range sb.Setup.Events {
			ev := content.Event{
				Type:   content.EventType(toStr(e["type"])),
				Caller: content.CallerType(toStr(e["caller"])),
				Slug:   toStr(e["slug"]),
				From:   toStr(e["from"]),
				Query:  toStr(e["query"]),
			}
			statStore.Record(ev)
		}
	}

	for _, asn := range sb.HTTPCases {
		name := asn.Name
		if name == "" {
			name = asn.Request.Method + " " + asn.Request.Path
		}
		t.Run(name, func(t *testing.T) {
			runHTTPAssertion(t, srv, asn)
		})
	}
}

func runHTTPAssertion(t *testing.T, srv *httptest.Server, a HTTPAssertion) {
	t.Helper()
	client := &http.Client{
		// Don't follow redirects — we want to assert on Location.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var body io.Reader
	contentType := ""
	switch {
	case len(a.Request.Files) > 0:
		// multipart/form-data: bundle Form fields + Files into one envelope
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range a.Request.Form {
			_ = mw.WriteField(k, v)
		}
		for _, f := range a.Request.Files {
			data, derr := base64.StdEncoding.DecodeString(f.ContentB64)
			if derr != nil {
				t.Fatalf("decode file %s: %v", f.Filename, derr)
			}
			part, perr := mw.CreateFormFile(f.Field, f.Filename)
			if perr != nil {
				t.Fatalf("create form file: %v", perr)
			}
			part.Write(data)
		}
		mw.Close()
		body = &buf
		contentType = mw.FormDataContentType()
	case len(a.Request.Form) > 0:
		vals := url.Values{}
		for k, v := range a.Request.Form {
			vals.Set(k, v)
		}
		body = strings.NewReader(vals.Encode())
		contentType = "application/x-www-form-urlencoded"
	case a.Request.Body != "":
		body = strings.NewReader(a.Request.Body)
	}

	method := a.Request.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, srv.URL+a.Request.Path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// MCP 2026-07-28 Streamable HTTP (via go-sdk) rejects requests without
	// Accept: application/json + text/event-stream. Auto-set for /mcp
	// paths so YAMLs stay concise (storyboards target JSON-RPC semantics,
	// not transport headers). YAML `headers` can still override.
	if strings.HasPrefix(a.Request.Path, "/mcp") {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	for k, v := range a.Request.Headers {
		req.Header.Set(k, v)
	}
	if a.Request.Owner {
		req.Header.Set("X-Edit-Token", testEditToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if a.Expect.Status != 0 && resp.StatusCode != a.Expect.Status {
		t.Errorf("status: got %d, want %d (body: %s)", resp.StatusCode, a.Expect.Status, truncate(string(respBody), 200))
	}
	if a.Expect.LocationContains != "" {
		loc := resp.Header.Get("Location")
		if !strings.Contains(loc, a.Expect.LocationContains) {
			t.Errorf("location: got %q, want substring %q", loc, a.Expect.LocationContains)
		}
	}
	bodyStr := string(respBody)
	for _, need := range a.Expect.BodyContains {
		if !strings.Contains(bodyStr, need) {
			t.Errorf("body missing %q\nbody: %s", need, truncate(bodyStr, 400))
		}
	}
	for _, forbid := range a.Expect.BodyDoesNotContain {
		if strings.Contains(bodyStr, forbid) {
			t.Errorf("body unexpectedly contains %q", forbid)
		}
	}
}

// saveListing bypasses POST /listings/new so the storyboard YAML can pin
// exact slugs (handleListingCreate appends a unix timestamp suffix, which
// is unhelpful for deterministic tests).
func saveListing(store *content.ListingStore, fields map[string]interface{}) error {
	l := content.Listing{
		Slug:   toStr(fields["slug"]),
		Type:   toStr(fields["type"]),
		Title:  toStr(fields["title"]),
		Body:   toStr(fields["body"]),
		Price:  toStr(fields["price"]),
		Status: toStr(fields["status"]),
		Access: toStr(fields["access"]),
		Lang:   toStr(fields["lang"]),
	}
	if l.Status == "" {
		l.Status = "open"
	}
	if l.Access == "" {
		l.Access = "public"
	}
	return store.Save(l)
}

func savePiece(store *content.Store, fields map[string]interface{}, kp *content.KeyPair) error {
	p := &content.Piece{
		Slug:      toStr(fields["slug"]),
		Title:     toStr(fields["title"]),
		Type:      toStr(fields["type"]),
		Body:      toStr(fields["body"]),
		Signature: toStr(fields["signature"]),
	}
	if p.Type == "" {
		p.Type = "poem"
	}
	if access := toStr(fields["access"]); access != "" {
		p.Access = content.AccessLevel(access)
	} else {
		p.Access = content.AccessPublic
	}
	// `signed: true` in the YAML auto-signs with the runner's test key
	// (so signatureState renders "valid"). Leaving Signature blank yields
	// the absent state; setting an explicit `signature: "..."` value gives
	// the invalid-signature state.
	if signedFlag, _ := fields["signed"].(bool); signedFlag && kp != nil && p.Signature == "" {
		sig, err := content.SignPiece(p, kp)
		if err != nil {
			return err
		}
		p.Signature = sig
	}
	return store.Save(p)
}

// savePersona writes a persona MD file matching the shape read by
// loadPersonasList (web) and parsePersonaFile (mcp): YAML frontmatter
// between --- markers followed by the prompt body. Only fields present in
// the YAML map are emitted, keeping storyboards terse.
func savePersona(contentDir string, fields map[string]interface{}) error {
	slug := toStr(fields["slug"])
	if slug == "" {
		return nil
	}
	dir := contentDir + "/personas"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("slug: " + slug + "\n")
	if v := toStr(fields["title"]); v != "" {
		b.WriteString("title: " + v + "\n")
	}
	if v := toStr(fields["role"]); v != "" {
		b.WriteString("role: " + v + "\n")
	}
	if v := toStr(fields["model"]); v != "" {
		b.WriteString("model: " + v + "\n")
	}
	if tags, ok := fields["tags"].([]interface{}); ok && len(tags) > 0 {
		b.WriteString("tags: [")
		for i, t := range tags {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(toStr(t))
		}
		b.WriteString("]\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(toStr(fields["body"]))
	b.WriteString("\n")
	return os.WriteFile(dir+"/"+slug+".md", []byte(b.String()), 0o644)
}

func toStr(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
