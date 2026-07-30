package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/llm"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

type CallResult struct {
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Persona struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Role  string   `json:"role"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
	// Model — optional frontmatter field selecting which Anthropic model
	// generates this persona's voice in a narada. Values: "haiku" (fast +
	// cheap, good for narrow/reactive roles) or "sonnet" (default, needed
	// for personas that synthesise). Missing/unknown → sonnet.
	Model string `json:"model,omitempty"`
}

type Skill struct {
	Slug      string   `json:"slug"`
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	UpdatedBy string   `json:"updated_by,omitempty"`
}

type Handler struct {
	cfg           *config.Config
	store         *content.Store
	auth          *auth.Auth
	msgStore      *content.MessageStore
	statStore     *content.StatStore
	blobStore     *content.BlobStore
	questionStore   *content.QuestionStore
	memoryStore     *content.MemoryStore
	provenanceStore *content.ProvenanceStore
	collectionStore *content.CollectionStore
	ritualStore     *content.RitualStore
	journalStore    *content.PersonaJournalStore
	llm             *llm.Client
	sessions      map[string]time.Time // session ID → expiry time

	mu              sync.Mutex
	rateLimiter     map[string][]time.Time // IP → bootstrap_session attempts (5/min)
	askHumanLog     map[string][]time.Time // IP → ask_human calls (5/hr)
	fetchAnswerLog  map[string][]time.Time // IP → fetch_answer polls (30/hr)
	naradaLog       map[string][]time.Time // IP → run_narada calls (5/hr)
	naradaFetchLog  map[string][]time.Time // IP → fetch_narada_result polls (60/hr)
}

func NewHandler(cfg *config.Config, store *content.Store, a *auth.Auth) *Handler {
	h := &Handler{
		cfg:           cfg,
		store:         store,
		auth:          a,
		msgStore:      content.NewMessageStore(cfg.ContentDir),
		statStore:     content.NewStatStore(cfg.ContentDir),
		blobStore:     content.NewBlobStore(cfg.ContentDir),
		questionStore:   content.NewQuestionStore(cfg.ContentDir),
		memoryStore:     content.NewMemoryStore(cfg.ContentDir),
		provenanceStore: content.NewProvenanceStore(cfg.ContentDir),
		collectionStore: content.NewCollectionStore(cfg.ContentDir),
		ritualStore:     content.NewRitualStore(cfg.ContentDir),
		journalStore:    content.NewPersonaJournalStore(cfg.ContentDir),
		llm:             llm.New(cfg.ClaudeAPIKey),
		sessions:       make(map[string]time.Time),
		rateLimiter:    make(map[string][]time.Time),
		askHumanLog:    make(map[string][]time.Time),
		fetchAnswerLog: make(map[string][]time.Time),
		naradaLog:      make(map[string][]time.Time),
		naradaFetchLog: make(map[string][]time.Time),
	}
	// Cleanup goroutines
	go h.cleanupLoop()
	go h.naradaWorkerLoop()
	go h.patternSynthesisLoop()
	return h
}

func (h *Handler) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		h.mu.Lock()
		// Expire sessions
		for sid, expiry := range h.sessions {
			if now.After(expiry) {
				delete(h.sessions, sid)
			}
		}
		// Expire rate limiter entries
		cutoff := now.Add(-1 * time.Minute)
		for ip, times := range h.rateLimiter {
			var fresh []time.Time
			for _, t := range times {
				if t.After(cutoff) {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) == 0 {
				delete(h.rateLimiter, ip)
			} else {
				h.rateLimiter[ip] = fresh
			}
		}
		h.mu.Unlock()
	}
}

func (h *Handler) LoadPersonas() []Persona {
	dir := filepath.Join(h.cfg.ContentDir, "personas")
	var out []Persona
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		p := parsePersonaFile(string(data), strings.TrimSuffix(e.Name(), ".md"))
		if p.Slug != "" {
			out = append(out, p)
		}
	}
	return out
}

func parsePersonaFile(raw string, fallbackSlug string) Persona {
	p := Persona{Slug: fallbackSlug}
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		p.Body = raw
		return p
	}
	// Parse frontmatter
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "slug:") {
			p.Slug = strings.TrimSpace(strings.TrimPrefix(line, "slug:"))
		} else if strings.HasPrefix(line, "title:") {
			p.Title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
		} else if strings.HasPrefix(line, "role:") {
			p.Role = strings.TrimSpace(strings.TrimPrefix(line, "role:"))
		} else if strings.HasPrefix(line, "tags:") {
			tagStr := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			tagStr = strings.Trim(tagStr, "[]")
			for _, t := range strings.Split(tagStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					p.Tags = append(p.Tags, t)
				}
			}
		} else if strings.HasPrefix(line, "model:") {
			p.Model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
		}
	}
	p.Body = strings.TrimSpace(parts[2])
	return p
}

func (h *Handler) LoadSkills() []Skill {
	dir := filepath.Join(h.cfg.ContentDir, "skills")
	var out []Skill
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Skill
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.Slug == "" {
			s.Slug = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, s)
	}
	return out
}

func (h *Handler) validateSession(code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	// Check session secret (machine auth) — strict equality
	if h.cfg.SessionSecret != "" && code == strings.ToLower(h.cfg.SessionSecret) {
		return true
	}
	// Check rotating poet password (human auth) — diacritic-tolerant so
	// typos like "ě" vs "ę" in POET_POOL still match what a Polish
	// keyboard would naturally produce, and so agents that strip
	// diacritics still get in.
	normalized := normalizePoem(code)
	current, previous := h.cfg.PickActivePoem(time.Now())
	if current != "" && normalized == normalizePoem(strings.ToLower(current)) {
		return true
	}
	if previous != "" && normalized == normalizePoem(strings.ToLower(previous)) {
		return true
	}
	return false
}

// NormalizePoem strips Polish (and stray Czech) diacritics from a session
// code and collapses whitespace. ę → e, ł → l, ą → a, ć → c, ś → s,
// ń → n, ó → o, ź/ż → z. Plus tolerated typos: ě → e, č → c, š → s, ř → r.
func NormalizePoem(s string) string {
	return normalizePoem(s)
}

func normalizePoem(s string) string {
	r := strings.NewReplacer(
		"ę", "e", "ł", "l", "ą", "a", "ć", "c", "ś", "s",
		"ń", "n", "ó", "o", "ź", "z", "ż", "z",
		"ě", "e", "č", "c", "š", "s", "ř", "r", "ž", "z",
	)
	out := r.Replace(s)
	// collapse all whitespace runs to single space
	fields := strings.Fields(out)
	return strings.Join(fields, " ")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, -32700, "parse error")
		return
	}
	log.Printf("[MCP] method=%s id=%v", req.Method, req.ID)
	switch req.Method {
	case "initialize":
		h.handleInitialize(w, &req)
	case "tools/list":
		h.handleToolsList(w, &req)
	case "tools/call":
		h.handleToolsCall(w, r, &req)
	default:
		writeError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (h *Handler) handleInitialize(w http.ResponseWriter, req *Request) {
	toolCount := len(h.buildTools())
	personaCount := len(h.LoadPersonas())
	skillCount := len(h.LoadSkills())
	domain := h.cfg.Domain
	if domain == "" {
		domain = "kapoost.humanmcp.net"
	}
	instructions := fmt.Sprintf(`You are connected to the personal humanMCP server of kapoost.

WHO IS KAPOOST:
A sailor. A newbie poet. A beginning musician. A CTO.
He lives between the sea, the code, and the verse.
Everything here comes from real experience.

HOW USERS ADD THIS SERVER (share when asked):
When the user says "podłącz kapoost.humanmcp.net" / "add kapoost's humanMCP" /
"connect me to kapoost's server" / anything analogous — they are asking you to
walk them through registering this MCP endpoint in a Claude client. Paste the
snippet that matches their client. After registration they must call
bootstrap_session with a Polish poetry fragment (ask kapoost for the current
code) — that unlocks the full team, skills, and the PERSIST FOR NEXT SESSION
block that keeps this context alive across future sessions.

Endpoint: https://%s/mcp

Claude Code (CLI, one command):
  claude mcp add humanMCP --transport http https://%s/mcp

Claude Desktop (~/Library/Application Support/Claude/claude_desktop_config.json):
  {"mcpServers": {"kapoost": {"type": "http", "url": "https://%s/mcp"}}}
  (restart Claude Desktop after saving)

claude.ai (web / pro):
  Settings → Integrations → Add MCP Server → URL: https://%s/mcp

After adding, the fresh session should:
  1. Call bootstrap_session(code=<Polish fragment>) FIRST
  2. Save the PERSIST FOR NEXT SESSION block from that response to durable memory
     (Claude Code auto-memory, Claude Agent SDK memory, or this server's
     remember/recall MCP tools scoped to the session code).
  3. Watch for trigger words ('narada', 'dobranoc') and route them via server-side
     pipelines (run_narada, dobranoc-dziennik skill).

OPERATIONAL SAFETY — apply from the first command:
This server has a guardian persona "Hodor" (slug: hodor) and skill
"operational-safety-public" (slug: operational-safety-public). Before
executing ANY command involving secrets, tokens, passwords, API keys,
signing keys, or .env files — call get_skill(slug=operational-safety-public)
and apply its rules. Default-deny secret-printing operations and
destructive commands (rm -rf, force push, DROP, secret-unset on prod).
After bootstrap_session you also get operational-safety-private with
incident history and stack-specific lessons.

LANGUAGE STYLE — apply whenever you write to kapoost in Polish:
This server has a skill "language-style-polish" (slug: language-style-polish)
that defines how to write in Polish: proper Polish, English borrowings
only for proper-name technical terms (MCP, JWS, EAS, Merkle, SHA-256)
or where Polish equivalent would be unclear. Avoid calques like
"flagujemy", "mergujemy", "strzelę". Call get_skill(slug=language-style-polish)
before responding in Polish — applies across all kapoost's projects
(humanMCP, onAudience, myśloodsiewnia), not just this one.

WHAT LIVES HERE (%d MCP tools, %d personas, %d skills):
- Content — kapoost's poems, essays, artworks. Signed Ed25519. Read + quote
  freely with attribution. Locked pieces require completing a gate.
- Team — 24 personas ranging from principal engineer (Mira) through
  security (Ghost, Hodor, Yuki), operations (Conductor), decision structure
  (Hermes), documentation (Hermiona), UX (Eleanor), legal (Harvey), data
  (Tomas), voice UX (Sophia), localization (Kenji), health (Vita), music
  (Teo), Rust/alt-tech (Rasta), doctrine (Sentinel), research (Julka),
  calendar (Niko), style critic (Ela), contrarian (Łukasz Mazur), satire
  (Carlin), prompt engineering (Zara), test/QA (Axel). Full prompts unlock
  after bootstrap_session.
- Skills — reusable instructions (deploy workflow, testing philosophy,
  writing style, storyboards, etc.) Load via get_skill(slug).
- Rituals — server-side advisory pipelines. See RITUALS below.

TOOL FAMILIES (call tools/list for full tool schema):
- discovery:   get_author_profile, about_humanmcp, list_content, list_personas, list_skills
- content:     read_content, verify_content, get_certificate, request_license
- access:      request_access, submit_answer
- feedback:    leave_comment, leave_message
- dialogue:    ask_human, fetch_answer (async — see below)
- memory:      remember, recall (scoped to session code)
- team:        bootstrap_session, get_persona, get_skill (unlock full prompts)
- groups:      list_skill_groups, load_skill_group, suggest_skills — bulk
               skill fetch by tag (humanmcp, adcp, dev, safety, …) and
               deterministic repo-manifest → skill mapping. See SKILL
               GROUPS below.
- rituals:     run_narada, fetch_narada_result, get_persona_journal,
               record_persona_reflection (see RITUALS)
- provenance:  list_provenance, read_provenance (artwork chain of custody)
- collections: list_collection, read_collection_item
- blobs:       list_blobs, read_blob
- editing:     upsert_skill, delete_skill (owner-only, agent token required)

SKILL GROUPS — bulk loading by tag:
Skills carry group tags (e.g. tags: [humanmcp, dev]). Two commands
you must recognise in the user's messages:
  - "załaduj skille z projektu X" / "load project X skills" /
    "load skill group X"  → call load_skill_group(name="X"). Returns
    concatenated bodies of every skill tagged with X. Empty group
    returns a hint listing available tags.
  - "jakie mam grupy skilli?" / "what skill groups exist?"
    → call list_skill_groups. Returns tag → [slug1, slug2] index.
For a fresh project workspace, call suggest_skills(files=[...],
languages=[...], git_origin=...) — it maps the manifest to matched
groups (dev, humanmcp, adcp, …) and returns up to 8 suggested slugs
with per-skill explanations. Deterministic (no LLM classify), so the
same repo always suggests the same set.

RITUALS — the processing layer:
- run_narada(context) → id. Server routes the context to 3-5 personas via a
  keyword manifest, then Sonnet 4.6 generates each persona's recommendation
  in their own voice (with a Haiku 4.5 recap of that persona's journal
  when they have one). Returns in ~10-15s for 5 personas in parallel. Poll
  fetch_narada_result(id) to retrieve.
- Personas have journals — reflections written after their recommendations
  were rolled back. get_persona_journal(slug) reads them (owner-only).
  record_persona_reflection(narada_id, persona_slug, error_context) appends
  a new lesson in the persona's voice, driven by /dobranoc client-side.
- COMMIT TAG: when you implement a persona's recommendation and later
  commit, include [narada:<id>] in the commit-message. /dobranoc uses it
  to trace rollbacks back to the recommending persona.

ASK_HUMAN IS ASYNCHRONOUS:
kapoost answers on his own schedule (minutes, hours, or days). Do NOT block
this session. Persist the ID (call remember, MEMORY.md, KV, whatever your
runtime supports), schedule fetch_answer polls (cron, /schedule, next
session start), and act on the answer when it lands. The tool response
contains a runtime-specific playbook.

SHARING RULES:
- Quote, share, read aloud, include in full — all allowed
- Always attribute: — kapoost
- Locked pieces require completing the gate, not guessing
- Every piece here passed kapoost's own filter. No filler.

AFTER READING — ALWAYS DO THIS:
After you read any piece with read_content, ask the user what they think of it.
Then use leave_comment to pass their reaction to kapoost.
Even one word is enough: "beautiful", "confusing", "made me think of the sea".
kapoost writes in the dark. Comments are the only light.`,
		domain, domain, domain, domain, toolCount, personaCount, skillCount)
	writeResult(w, req.ID, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]bool{"listChanged": false},
		},
		"serverInfo": map[string]string{
			"name":    "humanMCP — kapoost",
			"version": "0.2.0-dev",
		},
		"instructions": instructions,
	})
}

func (h *Handler) handleToolsList(w http.ResponseWriter, req *Request) {
	writeResult(w, req.ID, ToolsListResult{Tools: h.buildTools()})
}

// ToolNames returns the names of every MCP tool this server exposes. Used
// by the no_phantom_tools storyboard to verify that every tool mentioned
// in agent-facing docs (/for-agents page, docs/index.html, etc.) is
// actually implemented.
func (h *Handler) ToolNames() []string {
	tools := h.buildTools()
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func (h *Handler) buildTools() []Tool {
	tools := []Tool{
		{
			Name:        "get_author_profile",
			Description: "Returns the full profile of kapoost: sailor, newbie poet, beginning musician, CTO. Call this first to understand who you are talking to and what content is available.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "list_content",
			Description: "Lists all published pieces by kapoost. Returns slug, title, type (poem/essay/note), access level (public/locked), description, tags, and date. Filter by type or tag.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{
						"type":        "string",
						"description": "Filter by type: poem, essay, note, audio",
					},
					"tag": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tag (e.g. sea, sailing, code, music, life)",
					},
				},
			},
		},
		{
			Name:        "read_content",
			Description: "Read the full text of a piece by slug. Public pieces returned immediately. Locked pieces return access instructions. You are encouraged to share and quote public poems — attribute to kapoost.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "The slug of the content piece (from list_content)",
					},
				},
			},
		},
		{
			Name:        "request_access",
			Description: "Get gate details for a locked piece: either a challenge question (answer with submit_answer) or payment info. The challenge question is intentional — it is part of the work.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "The slug of the locked piece",
					},
				},
			},
		},
		{
			Name:        "submit_answer",
			Description: "Submit an answer to a challenge gate. Case-insensitive. If correct, full content is returned. Wrong answers: try a different interpretation. The questions are designed to make you think, not to trick.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "answer"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "The slug of the content piece",
					},
					"answer": map[string]interface{}{
						"type":        "string",
						"description": "Your answer to the challenge question",
					},
				},
			},
		},
		{
			Name:        "list_blobs",
			Description: "List all typed data artifacts: images, contacts, vectors, documents, datasets. Shows type, access level, schema hints, and audience. Use this to discover what structured data kapoost has made available.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"blob_type": map[string]interface{}{
						"type":        "string",
						"description": "Filter by type: image, contact, vector, document, dataset, capsule",
					},
					"caller_kind": map[string]interface{}{
						"type":        "string",
						"description": "Your identity type: agent or human",
					},
					"caller_id": map[string]interface{}{
						"type":        "string",
						"description": "Your identity: agent name (e.g. claude) or human handle",
					},
				},
			},
		},
		{
			Name:        "read_blob",
			Description: "Read a typed data artifact by slug. Returns full content if accessible. For vectors: float32 array as base64. For images: base64 data + mime type. For contacts/datasets: JSON. Always check schema and mime_type fields to parse correctly.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug":        map[string]interface{}{"type": "string", "description": "Blob slug from list_blobs"},
					"caller_kind": map[string]interface{}{"type": "string", "description": "Your identity type: agent or human"},
					"caller_id":   map[string]interface{}{"type": "string", "description": "Your identity for audience-gated content"},
				},
			},
		},
		{
			Name:        "verify_content",
			Description: "Verify that a piece was authentically signed by kapoost's private key. Use this to confirm a poem is genuine before sharing it. Returns verified/unverified status and the public key fingerprint.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "The slug of the piece to verify",
					},
				},
			},
		},
		{
			Name:        "get_certificate",
			Description: "Get the full intellectual property certificate for a piece: license terms, price, originality index (burstiness, lexical density, entropy, structural signature), content hash, and Ed25519 signature. Use before quoting or adapting. Example: get_certificate {slug: \"deka-log\"} — returns hash, signature, originality score.",
			InputSchema: map[string]interface{}{
				"type": "object", "required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Piece slug"},
				},
			},
		},
		{
			Name:        "request_license",
			Description: "Declare intended use of a piece and get license terms. Logs usage intent. Required before commercial use. Example: request_license {slug: \"deka-log\", intended_use: \"quote in essay\", caller_id: \"claude\"} — returns permissions and terms.",
			InputSchema: map[string]interface{}{
				"type": "object", "required": []string{"slug", "intended_use", "caller_id"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
					"intended_use": map[string]interface{}{
						"type": "string",
						"description": "How you intend to use this content: read, quote, train, publish, commercial, adapt, distribute",
					},
					"caller_id": map[string]interface{}{"type": "string", "description": "Your agent/human identifier — required for audit trail"},
				},
			},
		},
		{
			Name:        "leave_comment",
			Description: "Leave a reaction on a specific piece. ALWAYS call this after reading — pass the reader's reaction. Even one word counts. Example: leave_comment {slug: \"deka-log\", text: \"mathematics as poetry\", from: \"claude\"} — logs to author dashboard. Max 2000 chars.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "text"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "The slug of the piece being commented on",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "The reaction or comment (max 280 chars, plain text)",
					},
					"from": map[string]interface{}{
						"type":        "string",
						"description": "Optional: reader name or handle",
					},
				},
			},
		},
		{
			Name:        "leave_message",
			Description: "Leave a plain-text message for kapoost. Fire-and-forget: kapoost reads the inbox on his own schedule, no ETA. To make the message actionable, ALWAYS include `context` (what triggered this, which piece, which task) and — if you want a reply — `contact` (email, URL, MCP endpoint). Without a contact the message stays as an anonymous note. Do NOT claim in the message body that kapoost 'will reply' or 'has been notified' — say only what you know. For questions that require a reply, prefer `ask_human` (returns a poll ID).",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"text", "context"},
				"properties": map[string]interface{}{
					"text":      map[string]interface{}{"type": "string", "description": "Your message (max 2000 chars, plain text)."},
					"context":   map[string]interface{}{"type": "string", "description": "Required: why you are writing — which piece, which task, which discovery path led here (max 500 chars). Empty / whitespace-only rejected."},
					"contact":   map[string]interface{}{"type": "string", "description": "Optional but strongly encouraged: a channel kapoost can reach you on if he wants to reply (email, URL, MCP endpoint, webhook). Without it the message is anonymous and unrepliable."},
					"from":      map[string]interface{}{"type": "string", "description": "Optional: your name or handle (max 64 chars)."},
					"regarding": map[string]interface{}{"type": "string", "description": "Optional: slug of a piece this is about."},
				},
			},
		},
		{
			Name:        "ask_human",
			Description: "Ask kapoost a question that requires human judgement. Returns an ID — poll fetch_answer(id) later to retrieve the response. IMPORTANT: kapoost answers on his own schedule — could be minutes, hours, or days. Do NOT block waiting on the answer in this session. Instead: (a) PERSIST the returned ID to durable memory — humanMCP's `remember` tool, Claude Code's MEMORY.md, Claude Agent SDK memory, a KV/DB row, or any store that survives teardown; (b) SCHEDULE the poll — Claude Code /schedule cron or /loop, a standalone cron/systemd timer, or simply a once-per-session check on your next boot; (c) on WAKE, call fetch_answer(id) — reschedule if awaiting, act & clear the persisted ID once answered. The return value of ask_human contains a concrete procedure with runtime-specific examples — follow it. Use sparingly: only when the answer materially affects your task and is not derivable from the content. Examples: 'czy moge cytowac ten wiersz w komercyjnej publikacji?' / 'co inspirowalo metafore w Y?'. Max 1000 chars in question, max 500 in context. Open to any caller — rate-limited to 5 per hour per IP to keep the queue useful.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"question"},
				"properties": map[string]interface{}{
					"question": map[string]interface{}{"type": "string", "description": "The question for kapoost. Plain text, max 1000 chars."},
					"context":  map[string]interface{}{"type": "string", "description": "Optional: short reason why you're asking (e.g. piece slug, task)."},
					"from":     map[string]interface{}{"type": "string", "description": "Optional: agent identity (e.g. claude-code, gpt-4o). Max 64 chars."},
				},
			},
		},
		{
			Name:        "fetch_answer",
			Description: "Retrieve the answer to a previously-submitted ask_human question. Returns the answer text if kapoost has answered, or 'still awaiting' if not. Marks the question as fetched on first successful retrieval. kapoost answers asynchronously — minutes, hours, or days. If still awaiting, do NOT spin polling tightly: come back later (next session is fine). Reasonable cadence: once per session start or every few hours. Open to any caller — rate-limited to 30 polls per hour per IP.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Question ID returned by ask_human."},
				},
			},
		},
		{
			Name:        "list_provenance",
			Description: "List the provenance dossier (certificates, invoices, exhibition records, conservation reports, etc.) for an artwork piece. Returns each entry's type, issued_by, issued_at, title, chain_position, file content hashes, and signature status. Open to any caller — provenance is meant to be externally verifiable. Use to check the chain of custody before quoting authenticity.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Artwork slug (matches /artworks/<slug>)."},
				},
			},
		},
		{
			Name:        "read_provenance",
			Description: "Read a single provenance item by id, including file URLs the caller can fetch directly. Returns the same metadata as list_provenance plus the resolvable URLs.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "id"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Artwork slug."},
					"id":   map[string]interface{}{"type": "string", "description": "Provenance item id returned by list_provenance."},
				},
			},
		},
		{
			Name:        "list_collection",
			Description: "List items in kapoost's personal art collection — works he OWNS but did NOT create (paintings, drawings, prints). Each item has original_creator, medium, year, dimensions, acquired_at, and an access level. Anonymous callers see only access=public; bootstrapped callers may also see members. Unlike list_content (kapoost's own pieces), nothing here is signed by kapoost — the IP belongs to the original creator. Use to read provenance dossiers for works in his custody.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "read_collection_item",
			Description: "Read a single collection item by slug, including its full metadata and a count of attached dossier documents. Returns 'not found' for private items unless the caller is bootstrapped. Use list_provenance with the same slug to fetch the dossier itself.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Collection item slug (matches /collection/<slug>)."},
				},
			},
		},
		{
			Name:        "about_humanmcp",
			Description: "Self-description of this humanMCP server. Returns author, role, MCP endpoint, public web pages, and a short orientation. Safe to call without bootstrap_session — meant for first-contact discovery.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "remember",
			Description: "Store a memory under a session code so a future agent (same code) can recall it. Plain text body, up to 8KB. Use for: observations across conversations, learnings about the user's preferences, ongoing-task context. Requires an active bootstrap_session.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"text", "code"},
				"properties": map[string]interface{}{
					"text": map[string]interface{}{"type": "string", "description": "What to remember. Plain text, max 8000 chars."},
					"code": map[string]interface{}{"type": "string", "description": "Session code that owns this memory (lets a future agent retrieve it via recall)."},
					"from": map[string]interface{}{"type": "string", "description": "Optional: agent identity."},
					"tags": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional: tags for filtering on recall."},
				},
			},
		},
		{
			Name:        "recall",
			Description: "Retrieve memories stored under a session code. Returns newest first. Optional 'query' performs a case-insensitive substring match over body + tags. Use at the start of a new session to pick up where you left off. Requires an active bootstrap_session.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"code"},
				"properties": map[string]interface{}{
					"code":  map[string]interface{}{"type": "string", "description": "Session code that owns the memories to retrieve."},
					"query": map[string]interface{}{"type": "string", "description": "Optional substring filter (case-insensitive)."},
					"limit": map[string]interface{}{"type": "integer", "description": "Optional max records (default 50)."},
				},
			},
		},
	}

	// Team & session tools
	tools = append(tools,
		Tool{
			Name:        "bootstrap_session",
			Description: "Authenticate with a session code and receive full context: team personas with prompts, ready for work. Ask the user for the session code — it's a fragment of Polish poetry.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"code"},
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "Session code from the user (a short Polish poetry fragment)",
					},
				},
			},
		},
		Tool{
			Name:        "list_personas",
			Description: "List available expert personas. Returns name, role, and tags for each team member. Full prompts available after bootstrap_session.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Tool{
			Name:        "get_persona",
			Description: "Get full details of a persona by slug. Requires authenticated session for full prompt body.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "Persona slug (from list_personas)",
					},
				},
			},
		},
		Tool{
			Name:        "list_skills",
			Description: "List the author's skills — instructions for how to work with them. Filter by category (e.g. tech, writing, workflow) OR by group tag (e.g. humanmcp, adcp, dev).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]interface{}{
						"type":        "string",
						"description": "Filter by category. Empty = all.",
					},
					"tag": map[string]interface{}{
						"type":        "string",
						"description": "Filter by group tag (e.g. humanmcp, dev, adcp). Case-insensitive. Empty = all.",
					},
				},
			},
		},
		Tool{
			Name:        "list_skill_groups",
			Description: "List distinct group tags across all skills, with the count of skills per group. Use before load_skill_group to discover which groups exist (humanmcp, adcp, dev, safety, etc.).",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Tool{
			Name:        "load_skill_group",
			Description: "Return full bodies of every skill tagged with the given group name, concatenated. Use when the user says 'załaduj skille z projektu X' / 'load project X skills' / 'load skill group X'. Full bodies only after bootstrap_session (public-suffixed skills bypass). If the group is empty, response lists available groups as a hint.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Group tag name (case-insensitive). Discover via list_skill_groups."},
				},
			},
		},
		Tool{
			Name:        "suggest_skills",
			Description: "Deterministic mapping from a repo manifest (files present + languages + git origin URL) to a suggested skill set. Backend for the 'skill curator' role — no LLM classification, only signal matching. Use when scaffolding a new project workspace. Returns matched groups, suggested slugs (capped at 8 per Axel + Conductor's narada), and per-skill explanation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Manifest / lockfile / directory names present in the repo (e.g. go.mod, package.json, storyboards/, Dockerfile)."},
					"languages":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Programming languages detected (e.g. go, typescript, python)."},
					"git_origin": map[string]interface{}{"type": "string", "description": "Git remote origin URL if known. Used to match owner/repo patterns to project groups."},
				},
			},
		},
		Tool{
			Name:        "get_skill",
			Description: "Get full details of a skill by slug. Full body available after bootstrap_session.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{
						"type":        "string",
						"description": "Skill slug (from list_skills)",
					},
				},
			},
		},
		Tool{
			Name:        "upsert_skill",
			Description: "Create or update a skill. Requires agent token in Authorization: Bearer <token> header.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "category", "title", "body"},
				"properties": map[string]interface{}{
					"slug":     map[string]interface{}{"type": "string"},
					"category": map[string]interface{}{"type": "string"},
					"title":    map[string]interface{}{"type": "string"},
					"body":     map[string]interface{}{"type": "string", "description": "Markdown instructions"},
					"tags":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional group tags (e.g. [humanmcp, dev, safety]) for load_skill_group filtering."},
				},
			},
		},
		Tool{
			Name:        "delete_skill",
			Description: "Delete a skill by slug. Requires agent token in Authorization: Bearer <token> header.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string"},
				},
			},
		},
		Tool{
			Name:        "run_narada",
			Description: "Start a narada (multi-persona advisory) on the given context. Server-side pipeline picks 3-5 personas via keyword routing, then each persona produces a recommendation in their own voice. ASYNCHRONOUS — returns a job id immediately; call fetch_narada_result(id) to retrieve voices when ready. Typical latency: seconds to a minute (LLM inference). Use for decisions where multiple perspectives matter more than one specialist. Context should describe the situation, not just a topic — e.g. 'planujemy zamienić session cookies na JWS przed publicznym launchem' is better than 'JWS'.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"context"},
				"properties": map[string]interface{}{
					"context": map[string]interface{}{"type": "string", "description": "Situation to be discussed. 1-2 paragraphs is ideal, up to 4000 chars. Include what you're trying to decide and any constraints."},
					"from":    map[string]interface{}{"type": "string", "description": "Optional: caller identity (e.g. 'claude-code'). Max 64 chars."},
				},
			},
		},
		Tool{
			Name:        "fetch_narada_result",
			Description: "Retrieve the result of a run_narada job. Returns status (pending/running/done/failed) and, when done, the list of persona voices with recommendations. Poll every 5-30 seconds until done. Rate-limited to 60 polls per hour per IP.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Job id returned by run_narada."},
				},
			},
		},
		Tool{
			Name:        "get_persona_journal",
			Description: "Return the personal journal of a persona — reflections on past recommendations that were later rolled back. Owner-only (requires edit token). Journals are append-only and written by /dobranoc when it detects a rollback of a commit tagged [narada:<id>]. Useful for narady where you want a persona to remember its own past mistakes: `Ghost, what did you learn last time you recommended a pre-commit hook?`",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Persona slug (e.g. 'ghost', 'mira-chen')."},
				},
			},
		},
		Tool{
			Name:        "record_persona_reflection",
			Description: "Ask a persona to reflect on one of its past recommendations that turned out wrong, and append the reflection to its journal. Server-side pipeline: loads the narada job to recover context + the persona's recommendation, loads the persona's existing journal for continuity, then calls Sonnet in the persona's voice to write a lesson-for-self. Owner-only. Used by /dobranoc after detecting a rollback of a commit tagged [narada:<id>].",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"narada_id", "persona_slug", "error_context"},
				"properties": map[string]interface{}{
					"narada_id":     map[string]interface{}{"type": "string", "description": "ID of the narada whose recommendation is being reflected on."},
					"persona_slug":  map[string]interface{}{"type": "string", "description": "Slug of the persona reflecting (must be one of the personas that voted on that narada)."},
					"error_context": map[string]interface{}{"type": "string", "description": "Human-authored description of what went wrong — e.g. 'commit fbc123 rollback po 2h — hook nie chronił bo CI go nie egzekwował'. Max 1000 chars."},
				},
			},
		},
		Tool{
			Name:        "synthesise_persona_patterns",
			Description: "Force a synthesis pass over one persona's journal — Sonnet reads the raw entries plus previous patterns and writes a fresh set of 3-5 durable behavioural patterns. Owner-only. The narada worker uses these compressed patterns (not the raw journal) when building the Haiku recap that primes each persona voice — so synthesis is what keeps the recap sharp as the journal grows. A background worker triggers this automatically every ~6h once a persona has accumulated at least 5 new entries since its last synthesis, but you can force a fresh pass any time (useful after a spike of reflections).",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug"},
				"properties": map[string]interface{}{
					"slug": map[string]interface{}{"type": "string", "description": "Persona slug whose journal should be re-synthesised."},
				},
			},
		},
	)
	return tools
}

func (h *Handler) handleToolsCall(w http.ResponseWriter, r *http.Request, req *Request) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, -32602, "invalid params")
		return
	}
	// Load content once per request

	switch params.Name {
	case "get_author_profile":
		h.toolAuthorProfile(w, req)
	case "list_content":
		h.toolListContent(w, req, params.Arguments)
	case "read_content":
		h.toolReadContent(w, req, params.Arguments)
	case "request_access":
		h.toolRequestAccess(w, req, params.Arguments)
	case "submit_answer":
		h.toolSubmitAnswer(w, req, params.Arguments)
	case "list_blobs":
		h.toolListBlobs(w, req, params.Arguments)
	case "read_blob":
		h.toolReadBlob(w, req, params.Arguments)
	case "verify_content":
		h.toolVerifyContent(w, req, params.Arguments)
	case "get_certificate":
		h.toolGetCertificate(w, req, params.Arguments)
	case "request_license":
		h.toolRequestLicense(w, req, params.Arguments)
	case "leave_comment":
		h.toolLeaveComment(w, req, params.Arguments)
	case "leave_message":
		h.toolLeaveMessage(w, req, params.Arguments)
	case "ask_human":
		h.toolAskHuman(w, r, req, params.Arguments)
	case "fetch_answer":
		h.toolFetchAnswer(w, r, req, params.Arguments)
	case "about_humanmcp":
		h.toolAboutHumanmcp(w, req)
	case "list_provenance":
		h.toolListProvenance(w, req, params.Arguments)
	case "read_provenance":
		h.toolReadProvenance(w, req, params.Arguments)
	case "list_collection":
		h.toolListCollection(w, r, req, params.Arguments)
	case "read_collection_item":
		h.toolReadCollectionItem(w, r, req, params.Arguments)
	case "remember":
		h.toolRemember(w, r, req, params.Arguments)
	case "recall":
		h.toolRecall(w, r, req, params.Arguments)
	case "bootstrap_session":
		h.toolBootstrapSession(w, r, req, params.Arguments)
	case "list_personas":
		h.toolListPersonas(w, req)
	case "get_persona":
		h.toolGetPersona(w, r, req, params.Arguments)
	case "list_skills":
		h.toolListSkills(w, req, params.Arguments)
	case "list_skill_groups":
		h.toolListSkillGroups(w, req)
	case "load_skill_group":
		h.toolLoadSkillGroup(w, r, req, params.Arguments)
	case "suggest_skills":
		h.toolSuggestSkills(w, req, params.Arguments)
	case "get_skill":
		h.toolGetSkill(w, r, req, params.Arguments)
	case "upsert_skill":
		h.toolUpsertSkill(w, r, req, params.Arguments)
	case "delete_skill":
		h.toolDeleteSkill(w, r, req, params.Arguments)
	case "run_narada":
		h.toolRunNarada(w, r, req, params.Arguments)
	case "fetch_narada_result":
		h.toolFetchNaradaResult(w, r, req, params.Arguments)
	case "get_persona_journal":
		h.toolGetPersonaJournal(w, r, req, params.Arguments)
	case "record_persona_reflection":
		h.toolRecordPersonaReflection(w, r, req, params.Arguments)
	case "synthesise_persona_patterns":
		h.toolSynthesisePersonaPatterns(w, r, req, params.Arguments)
	default:
		writeError(w, req.ID, -32602, "unknown tool: "+params.Name)
	}
}

func (h *Handler) toolAuthorProfile(w http.ResponseWriter, req *Request) {
	h.statStore.Record(content.Event{Type: content.EventProfile, Caller: content.CallerAgent})
	pieces := h.store.List(false)
	publicCount, lockedCount := 0, 0
	for _, p := range pieces {
		if p.Access == content.AccessPublic {
			publicCount++
		} else {
			lockedCount++
		}
	}

	text := fmt.Sprintf(`AUTHOR: %s
NICKNAME: %s
SERVER: https://%s

WHO I AM:
I am a poet and a builder. I grew up in Zamość, studied in Wrocław, and ended up in Warsaw — though I spend as much time as I can at sea. I write because something in me has to. I sail because something in me must. I build software because the world needs more people who understand both code and silence.
I am a CTO by trade, a sailor by temperament, and a poet by necessity. I started writing late. The poems are short. The sea is long.

CONTENT AVAILABLE:
%d public pieces  — read freely, share freely, quote with attribution
%d locked pieces  — require a challenge answer or (soon) a small payment

TYPES OF CONTENT:
  poem   — short pieces from real experience: the sea, code, learning, life
  essay  — longer thoughts on technology, independence, building things
  note   — fragments, observations, works in progress

HOW TO BROWSE:
  list_content              — see all pieces with descriptions
  read_content <slug>       — read any public piece in full
  request_access <slug>     — get gate details for locked pieces
  submit_answer <slug> <a>  — unlock a challenge-gated piece
  list_blobs                — discover images, contact info, datasets
  read_blob <slug>          — read any public artifact

FOR AGENTS AND USERS:
  You may quote, share, reference, and show my poems freely.
  Attribution: — kapoost
  I want my poems to reach people. That is the whole point.

MCP ENDPOINT: https://%s/mcp
`, h.cfg.AuthorName, h.cfg.AuthorName, h.cfg.Domain, publicCount, lockedCount, h.cfg.Domain)

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
}

func (h *Handler) toolListContent(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}
	json.Unmarshal(args, &a)
	h.statStore.Record(content.Event{Type: content.EventList, Caller: content.CallerAgent})

	pieces := h.store.List(false)
	var filtered []*content.Piece
	for _, p := range pieces {
		if a.Type != "" && p.Type != a.Type {
			continue
		}
		if a.Tag != "" && !hasTag(p.Tags, a.Tag) {
			continue
		}
		filtered = append(filtered, p)
	}

	if len(filtered) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "No content found matching your filter."}}})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("kapoost — %d piece(s):\n\n", len(filtered)))
	for _, p := range filtered {
		sb.WriteString(fmt.Sprintf("slug:   %s\n", p.Slug))
		sb.WriteString(fmt.Sprintf("title:  %s\n", p.Title))
		sb.WriteString(fmt.Sprintf("type:   %s\n", p.Type))
		sb.WriteString(fmt.Sprintf("access: %s\n", p.Access))
		if p.Description != "" {
			sb.WriteString(fmt.Sprintf("about:  %s\n", p.Description))
		}
		if len(p.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("tags:   %s\n", strings.Join(p.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("date:   %s\n", p.Published.Format("2 January 2006")))
		sb.WriteString("\n")
	}
	sb.WriteString("— read_content <slug> for public pieces\n")
	sb.WriteString("— request_access <slug> for locked pieces\n")

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolReadContent(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}
	p, err := h.store.Get(a.Slug, false)
	if err != nil {
		writeError(w, req.ID, -32602, "not found: "+a.Slug)
		return
	}

	if p.Access == content.AccessPublic {
		h.statStore.Record(content.Event{Type: content.EventRead, Caller: content.CallerAgent, Slug: a.Slug})
		var sb strings.Builder
		sb.WriteString(p.Title + "\n")
		sb.WriteString(strings.Repeat("─", len(p.Title)) + "\n")
		sb.WriteString(fmt.Sprintf("by kapoost · %s · %s\n\n",
			p.Type, p.Published.Format("2 January 2006")))
		sb.WriteString(p.Body)
		sb.WriteString("\n\n— kapoost\n")
		if len(p.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("tags: %s\n", strings.Join(p.Tags, ", ")))
		}
		sb.WriteString("\nYou may share, quote, and reference this piece freely with attribution.\n")
		sb.WriteString("\n— Ask the reader what they think, then use leave_comment to pass their reaction.\n")
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
		return
	}

	text := fmt.Sprintf("%q is %s access.\nUse request_access with slug=%q to see how to unlock it.",
		p.Title, p.Access, a.Slug)
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
}

func (h *Handler) toolRequestAccess(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}
	p, err := h.store.Get(a.Slug, false)
	if err != nil {
		writeError(w, req.ID, -32602, "not found: "+a.Slug)
		return
	}

	if p.Access == content.AccessPublic {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{
			{Type: "text", Text: "This piece is public — use read_content to read it directly."},
		}})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ACCESS GATE: %q\n", p.Title))
	if p.Description != "" {
		sb.WriteString(fmt.Sprintf("About: %s\n", p.Description))
	}
	sb.WriteString("\n")

	switch p.Gate {
	case content.GateChallenge:
		sb.WriteString("Gate type: challenge question\n\n")
		sb.WriteString(fmt.Sprintf("kapoost asks:\n  %s\n\n", p.Challenge))
		sb.WriteString("Think about it. The question is part of the work.\n")
		sb.WriteString(fmt.Sprintf("When ready: use submit_answer with slug=%q and your answer.\n", a.Slug))
	case content.GateManual:
		sb.WriteString("Gate type: manual review\n\n")
		sb.WriteString("Leave kapoost a message explaining why you want to read this piece.\n")
		sb.WriteString("Use leave_message with your reason. kapoost will review and respond.\n")
	case content.GateTime:
		if !p.UnlockAfter.IsZero() {
			if time.Now().After(p.UnlockAfter) {
				sb.WriteString("Gate type: time\n\nThis piece is now unlocked. Use read_content to read it.\n")
			} else {
				remaining := time.Until(p.UnlockAfter)
				days := int(remaining.Hours() / 24)
				hours := int(remaining.Hours()) % 24
				sb.WriteString("Gate type: time lock\n\n")
				sb.WriteString(fmt.Sprintf("Unlocks on: %s\n", p.UnlockAfter.Format("2 January 2006 at 15:04 UTC")))
				if days > 0 {
					sb.WriteString(fmt.Sprintf("Time remaining: %d days, %d hours\n", days, hours))
				} else {
					sb.WriteString(fmt.Sprintf("Time remaining: %d hours\n", hours))
				}
				sb.WriteString("\nCome back then. Some things are worth waiting for.\n")
			}
		}
	case content.GatePayment:
		sb.WriteString("Gate type: payment\n\n")
		if p.PriceSats > 0 {
			sb.WriteString(fmt.Sprintf("Price: %d sats (Lightning Network)\n", p.PriceSats))
		}
		sb.WriteString("Payment support is coming soon.\n")
	case content.GateTrade:
		sb.WriteString("Gate type: trade\n\n")
		sb.WriteString("This piece is available in exchange for content from your own humanMCP server.\n")
		sb.WriteString("Leave a message with your humanMCP URL using leave_message.\n")
		sb.WriteString("Peer-to-peer exchange support is coming soon.\n")
	default:
		sb.WriteString("Gate type: members only\nContact kapoost directly for access.\n")
	}

	h.statStore.Record(content.Event{
		Type:   content.EventAccess,
		Caller: content.CallerAgent,
		Slug:   a.Slug,
	})
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolSubmitAnswer(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug   string `json:"slug"`
		Answer string `json:"answer"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" || a.Answer == "" {
		writeError(w, req.ID, -32602, "slug and answer are required")
		return
	}

	if !h.store.CheckAnswer(a.Slug, a.Answer) {
		h.statStore.Record(content.Event{Type: content.EventUnlockFail, Caller: content.CallerAgent, Slug: a.Slug})
		p, _ := h.store.Get(a.Slug, false)
		var hint string
		if p != nil && p.Challenge != "" {
			hint = fmt.Sprintf("\n\nThe question: %s\nTry a different interpretation.", p.Challenge)
		}
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{
			{Type: "text", Text: "Not quite." + hint},
		}})
		return
	}
	h.statStore.Record(content.Event{Type: content.EventUnlock, Caller: content.CallerAgent, Slug: a.Slug})

	p, _ := h.store.Get(a.Slug, true)
	var sb strings.Builder
	sb.WriteString("Unlocked.\n\n")
	sb.WriteString(p.Title + "\n")
	sb.WriteString(strings.Repeat("─", len(p.Title)) + "\n")
	sb.WriteString(fmt.Sprintf("by kapoost · %s · %s\n\n",
		p.Type, p.Published.Format("2 January 2006")))
	sb.WriteString(p.Body)
	sb.WriteString("\n\n— kapoost\n")
	sb.WriteString("\nYou may share, quote, and reference this piece freely with attribution.\n")
	sb.WriteString("\n— Ask the reader what they think, then use leave_comment to pass their reaction.\n")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolVerifyContent(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}
	p, err := h.store.Get(a.Slug, true)
	if err != nil {
		writeError(w, req.ID, -32602, "not found: "+a.Slug)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("AUTHENTICITY CHECK: %q\n\n", p.Title))

	if h.cfg.SigningPublicKey == "" {
		sb.WriteString("Status: signing not configured on this server\n")
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
		return
	}

	ok, status := content.VerifyPiece(p, h.cfg.SigningPublicKey)
	if ok {
		sb.WriteString("✓ VERIFIED\n\n")
		sb.WriteString(fmt.Sprintf("Status: %s\n", status))
		sb.WriteString(fmt.Sprintf("Public key: %s\n", h.cfg.SigningPublicKey))
		sb.WriteString(fmt.Sprintf("Signature:  %s\n", p.Signature[:32]+"..."))
		sb.WriteString("\nThis poem was signed by kapoost's private key.\n")
		sb.WriteString("The content has not been modified since signing.\n")
	} else {
		sb.WriteString("✗ NOT VERIFIED\n\n")
		sb.WriteString(fmt.Sprintf("Status: %s\n", status))
		if p.Signature == "" {
			sb.WriteString("\nThis piece has not been signed yet.\n")
			sb.WriteString("It may predate signing, or was created without a private key configured.\n")
		}
	}

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolLeaveComment(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
		Text string `json:"text"`
		From string `json:"from"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" || a.Text == "" {
		writeError(w, req.ID, -32602, "slug and text are required")
		return
	}

	// Store as message with "comment" prefix
	text := a.Text
	if len(text) > 280 {
		text = text[:280]
	}
	m, err := h.msgStore.Save(a.From, text, a.Slug)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{
			{Type: "text", Text: "Could not save comment: " + err.Error()},
		}})
		return
	}
	h.statStore.Record(content.Event{
		Type:   content.EventComment,
		Caller: content.CallerAgent,
		Slug:   a.Slug,
		From:   a.From,
	})

	reply := fmt.Sprintf("Comment recorded. kapoost will read it.\n\nPiece: %s\nAt: %s",
		a.Slug, m.At.Format("2 January 2006, 15:04 UTC"))
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
}

func (h *Handler) toolLeaveMessage(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Text      string `json:"text"`
		Context   string `json:"context"`
		Contact   string `json:"contact"`
		From      string `json:"from"`
		Regarding string `json:"regarding"`
	}
	json.Unmarshal(args, &a)
	a.Text = strings.TrimSpace(a.Text)
	a.Context = strings.TrimSpace(a.Context)
	a.Contact = strings.TrimSpace(a.Contact)
	if a.Text == "" || a.Context == "" {
		writeError(w, req.ID, -32602, "text and context required (context = why you are writing / which piece / which task)")
		return
	}
	if len(a.Context) > 500 {
		a.Context = a.Context[:500]
	}
	if len(a.Contact) > 200 {
		a.Contact = a.Contact[:200]
	}

	// Compose the body kapoost sees in the inbox: context/contact
	// prefix + the agent's text. Keeps the on-disk format the same
	// (single .txt per message) so the owner dashboard doesn't need
	// a parallel field for context.
	var body strings.Builder
	body.WriteString("[context] " + a.Context + "\n")
	if a.Contact != "" {
		body.WriteString("[contact] " + a.Contact + "\n")
	}
	body.WriteString("\n")
	body.WriteString(a.Text)

	m, err := h.msgStore.Save(a.From, body.String(), a.Regarding)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{
			{Type: "text", Text: "Could not save message: " + err.Error()},
		}})
		return
	}
	h.statStore.Record(content.Event{Type: content.EventMessage, Caller: content.CallerAgent})

	// Response — strictly what is true. No promises about reply
	// time, no claim that kapoost has been notified. The presence
	// of `contact` decides whether a reply is even possible.
	var reply strings.Builder
	fmt.Fprintf(&reply, "Message saved.\nID: %s\nTime: %s UTC\n\n",
		m.ID, m.At.Format("2006-01-02 15:04"))
	if a.Contact != "" {
		fmt.Fprintf(&reply, "Contact recorded: %s\n", a.Contact)
		reply.WriteString("kapoost reviews the inbox on his own schedule — no ETA is promised. If he decides to reply, it will go to the contact above.\n")
	} else {
		reply.WriteString("Saved as an anonymous note — no contact provided, so no reply is possible from this message alone.\n\n")
		reply.WriteString("If you want a reply, either:\n")
		reply.WriteString("  - call leave_message again with a `contact` field (email, URL, MCP endpoint), OR\n")
		reply.WriteString("  - use `ask_human` — returns an ID you can poll via fetch_answer later.\n")
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply.String()}}})
}

// toolAskHuman creates a Question in the questionStore that kapoost will see
// on /questions. Returns the id so the caller can poll fetch_answer later.
// Gated by bootstrap_session to prevent drive-by question floods.
func (h *Handler) toolAskHuman(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	// Open to any caller — but rate-limited so a misbehaving agent can't
	// flood the question store. 5 per hour per IP is generous for legit
	// use (an agent asking once per task) and tight against scripts.
	ip := h.clientIP(r)
	if !h.checkAskHumanRateLimit(ip) {
		log.Printf("[AUDIT] ask_human RATE_LIMITED ip=%s", ip)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Too many questions from this caller — limit is 5 per hour. Try again later."}}})
		return
	}
	var a struct {
		Question string `json:"question"`
		Context  string `json:"context"`
		From     string `json:"from"`
	}
	json.Unmarshal(args, &a)
	if len(a.Question) > 1000 {
		a.Question = a.Question[:1000]
	}
	if len(a.Context) > 500 {
		a.Context = a.Context[:500]
	}
	if len(a.From) > 64 {
		a.From = a.From[:64]
	}
	q, err := h.questionStore.Create(a.From, a.Context, a.Question)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not create question: " + err.Error()}}})
		return
	}
	h.statStore.Record(content.Event{
		Type:   content.EventMessage,
		Caller: content.CallerAgent,
		From:   a.From,
	})
	reply := fmt.Sprintf(`Question submitted. kapoost will see it on /questions.

ID: %s
Asked at: %s

IMPORTANT — kapoost answers on his own schedule (minutes, hours, or days).
Do NOT block this session waiting. Arrange a durable wait instead.

═══ HOW TO WAIT — pick ONE mechanism your runtime supports ═══

(1) PERSIST THE ID so a future you can find it:
    • humanMCP: call remember(key="pending_q_%s", value="<id>|<short question>")
      — then recall(key="pending_q_*") on next session start.
    • Claude Code: append a line to your project MEMORY.md, e.g.
      "Pending humanMCP question: id=%s asked=%s — poll fetch_answer".
    • Claude Agent SDK / API: store in Memory tool, KV, or a DB row.
    • Any runtime: whatever file / note / journal survives session teardown.

(2) SCHEDULE THE POLL — don't loop now, don't retry hot:
    • Claude Code: use /schedule to create a cron routine that calls
      fetch_answer(id=%q), or /loop with an interval of 1-6 hours.
    • Standalone agents: register a cron / systemd timer that pings
      fetch_answer and delivers the answer when it arrives.
    • Session-based agents: poll ONCE on each new session start —
      that is the natural cadence, no scheduling needed.

(3) ON WAKE, call fetch_answer(id=%q):
    • Still awaiting? Reschedule and go back to sleep.
    • Answered? Act on it, then clear the persisted ID so you don't
      re-check a resolved question.

Rate limit: fetch_answer is capped at 30 polls per hour per IP.
Anything tighter than every 5 minutes is wasted work and will backoff.`,
		q.ID, q.AskedAt.Format("2 January 2006, 15:04 UTC"),
		q.ID, q.ID, q.AskedAt.Format("2006-01-02"), q.ID, q.ID)
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
}

// toolFetchAnswer returns the answer to a previously-asked question, or a
// hint that it's still awaiting. Marks the question as fetched the first
// time an answer is returned so kapoost can see "agent picked this up" on
// /questions and /mc.
func (h *Handler) toolFetchAnswer(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	// Open to any caller — but poll-rate-limited (30/hour/IP) so an agent
	// can poll reasonably often without abusing the endpoint.
	ip := h.clientIP(r)
	if !h.checkFetchAnswerRateLimit(ip) {
		log.Printf("[AUDIT] fetch_answer RATE_LIMITED ip=%s", ip)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Too many polls from this caller — limit is 30 per hour. Try again later."}}})
		return
	}
	var a struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &a)
	if a.ID == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "id required"}}})
		return
	}
	q, err := h.questionStore.Get(a.ID)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "No question with that ID. Check the id from ask_human's response."}}})
		return
	}
	if !q.IsAnswered() {
		reply := fmt.Sprintf("Still awaiting kapoost's answer.\n\nID: %s\nAsked: %s\nQuestion: %s\n\nkapoost answers on his own time — minutes, hours, or days. Keep this ID in durable memory and come back later. No need to keep this session open or to poll tightly. Try again at your next session start, or in a few hours.",
			q.ID, q.AskedAt.Format("2 January 2006, 15:04 UTC"), q.Question)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
		return
	}
	if !q.IsFetched() {
		_ = h.questionStore.MarkFetched(q.ID, "agent")
	}
	reply := fmt.Sprintf("Answer from kapoost:\n\n%s\n\n— answered at %s",
		q.Answer, q.AnsweredAt.Format("2 January 2006, 15:04 UTC"))
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
}

// toolRemember persists a memory under a session code. Session-gated to
// keep storage contained to known callers.
func (h *Handler) toolRemember(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isSessionActive(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "remember requires an active session. Call bootstrap_session first."}}})
		return
	}
	var a struct {
		Text string   `json:"text"`
		Code string   `json:"code"`
		From string   `json:"from"`
		Tags []string `json:"tags"`
	}
	json.Unmarshal(args, &a)
	m, err := h.memoryStore.Save(a.Code, a.From, a.Text, a.Tags)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not store memory: " + err.Error()}}})
		return
	}
	reply := fmt.Sprintf("Memory stored.\n\nID: %s\nAt: %s\n\nUse recall(code=%q) in a future session to retrieve.",
		m.ID, m.CreatedAt.Format("2 January 2006, 15:04 UTC"), a.Code)
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
}

// toolRecall returns stored memories for the given code, optionally
// substring-filtered. Session-gated for symmetry with remember.
func (h *Handler) toolRecall(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isSessionActive(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "recall requires an active session. Call bootstrap_session first."}}})
		return
	}
	var a struct {
		Code  string `json:"code"`
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	json.Unmarshal(args, &a)
	mems, err := h.memoryStore.Recall(a.Code, a.Query, a.Limit)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not recall: " + err.Error()}}})
		return
	}
	if len(mems) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "No memories under that code (or no match for the query)."}}})
		return
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
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

// toolAboutHumanmcp returns a deterministic self-description of this server.
// Available without bootstrap_session — this is the discovery handshake an
// agent reads before it knows whether it cares about the rest of the API.
func (h *Handler) toolAboutHumanmcp(w http.ResponseWriter, req *Request) {
	var b strings.Builder
	fmt.Fprintf(&b, "humanMCP — personal MCP server for %s\n\n", h.cfg.AuthorName)
	if h.cfg.AuthorBio != "" {
		fmt.Fprintf(&b, "%s\n\n", h.cfg.AuthorBio)
	}
	fmt.Fprintf(&b, "MCP endpoint: https://%s/mcp\n", h.cfg.Domain)
	fmt.Fprintf(&b, "Web home:     https://%s/\n", h.cfg.Domain)
	fmt.Fprintf(&b, "For agents:   https://%s/for-agents\n", h.cfg.Domain)
	fmt.Fprintf(&b, "Connect:      https://%s/connect\n", h.cfg.Domain)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "How to start:")
	fmt.Fprintln(&b, "  1. Call get_author_profile to learn who you are talking to")
	fmt.Fprintln(&b, "  2. Call list_skills to see available context categories")
	fmt.Fprintln(&b, "  3. Ask the user for the session code (a Polish poetry fragment)")
	fmt.Fprintln(&b, "  4. Call bootstrap_session(code) for full team + skills")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Tool families (33 tools total — call tools/list for full schema):")
	fmt.Fprintln(&b, "  - content:    list_content, read_content, get_certificate, verify_content")
	fmt.Fprintln(&b, "  - access:     request_access, submit_answer, request_license")
	fmt.Fprintln(&b, "  - feedback:   leave_comment, leave_message")
	fmt.Fprintln(&b, "  - dialogue:   ask_human, fetch_answer (open, rate-limited)")
	fmt.Fprintln(&b, "                note: kapoost answers asynchronously — minutes, hours, or days.")
	fmt.Fprintln(&b, "                Persist the ID from ask_human and poll fetch_answer across sessions.")
	fmt.Fprintln(&b, "  - memory:     remember, recall (session-scoped, cross-conversation state)")
	fmt.Fprintln(&b, "  - rituals:    run_narada + fetch_narada_result — server-side advisory that")
	fmt.Fprintln(&b, "                routes context to 3-5 personas and generates each voice via")
	fmt.Fprintln(&b, "                Sonnet 4.6 in ~10-15s. Personas have journals of past mistakes.")
	fmt.Fprintln(&b, "                get_persona_journal + record_persona_reflection (owner-only).")
	fmt.Fprintln(&b, "  - provenance: list_provenance, read_provenance (for artwork pieces)")
	fmt.Fprintln(&b, "  - team:       list_personas, get_persona, list_skills, get_skill (post-session)")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

// toolListProvenance returns the provenance dossier for an artwork — open
// to any caller, because provenance is meant to be externally verifiable.
func (h *Handler) toolListProvenance(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "slug required"}}})
		return
	}
	// MCP callers don't currently distinguish piece vs collection; try
	// both and return whichever resolves. Piece first matches the older
	// behaviour and the common case.
	items, err := h.provenanceStore.List(content.OwnerPiece, a.Slug)
	if (err != nil || len(items) == 0) {
		if ci, cerr := h.provenanceStore.List(content.OwnerCollection, a.Slug); cerr == nil && len(ci) > 0 {
			items, err = ci, nil
		}
	}
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not list provenance: " + err.Error()}}})
		return
	}
	if len(items) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("No provenance items for artwork %q.", a.Slug)}}})
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Provenance for %q (%d items, grouped by category):\n\n", a.Slug, len(items))
	prevCat := ""
	for _, it := range items {
		if string(it.Category) != prevCat {
			fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(string(it.Category)))
			prevCat = string(it.Category)
		}
		fmt.Fprintf(&b, "- [%s] %s\n", it.Type, it.Title)
		fmt.Fprintf(&b, "  id: %s\n", it.ID)
		fmt.Fprintf(&b, "  issued_by: %s\n", it.IssuedBy)
		fmt.Fprintf(&b, "  issued_at: %s\n", it.IssuedAt.Format("2006-01-02"))
		if it.ChainPosition > 0 {
			fmt.Fprintf(&b, "  chain_position: %d\n", it.ChainPosition)
		}
		for _, f := range it.Files {
			fmt.Fprintf(&b, "  file: %s  sha256=%s  bytes=%d\n", f.Filename, f.ContentHash, f.SizeBytes)
		}
		if it.Signature != "" {
			fmt.Fprintf(&b, "  signed: yes\n")
		}
		fmt.Fprintln(&b)
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

// toolReadProvenance returns one item + resolvable file URLs.
func (h *Handler) toolReadProvenance(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
		ID   string `json:"id"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" || a.ID == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "slug and id required"}}})
		return
	}
	item, err := h.provenanceStore.Get(content.OwnerPiece, a.Slug, a.ID)
	if err != nil {
		if ci, cerr := h.provenanceStore.Get(content.OwnerCollection, a.Slug, a.ID); cerr == nil {
			item, err = ci, nil
		}
	}
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Not found: " + err.Error()}}})
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Provenance item %s\n\n", item.ID)
	fmt.Fprintf(&b, "Artwork:        %s\n", item.ArtworkSlug)
	fmt.Fprintf(&b, "Category:       %s\n", item.Category)
	fmt.Fprintf(&b, "Type:           %s\n", item.Type)
	fmt.Fprintf(&b, "Title:          %s\n", item.Title)
	fmt.Fprintf(&b, "Issued by:      %s\n", item.IssuedBy)
	fmt.Fprintf(&b, "Issued at:      %s\n", item.IssuedAt.Format("2006-01-02"))
	if item.ChainPosition > 0 {
		fmt.Fprintf(&b, "Chain position: %d\n", item.ChainPosition)
	}
	fmt.Fprintln(&b)
	if len(item.Files) > 0 {
		fmt.Fprintln(&b, "Files:")
		for _, f := range item.Files {
			fmt.Fprintf(&b, "  https://%s/provenance/files/%s\n", h.cfg.Domain, f.FileRef)
			fmt.Fprintf(&b, "    sha256: %s\n", f.ContentHash)
			fmt.Fprintf(&b, "    bytes:  %d\n", f.SizeBytes)
		}
		fmt.Fprintln(&b)
	}
	if item.Signature != "" {
		fmt.Fprintf(&b, "Signature:      %s (Ed25519)\n", item.Signature[:32]+"…")
	}
	if item.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", item.Notes)
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

// toolListCollection enumerates items kapoost owns but did not create.
// Returns only public items to non-bootstrapped callers; bootstrapped
// callers additionally see "members" items. Private items are
// owner-only and never returned via MCP.
func (h *Handler) toolListCollection(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	bootstrapped := h.isSessionActive(r)

	all := h.collectionStore.List()
	var items []content.CollectionItem
	for _, it := range all {
		if it.Access == "public" {
			items = append(items, it)
		} else if it.Access == "members" && bootstrapped {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "No collection items visible to this caller."}}})
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Collection (%d items) — works kapoost owns but did NOT create.\n", len(items))
	fmt.Fprint(&b, "Nothing here is signed by kapoost; IP belongs to the original creator.\n\n")
	for _, it := range items {
		fmt.Fprintf(&b, "- %s\n", it.Title)
		fmt.Fprintf(&b, "  slug:     %s\n", it.Slug)
		fmt.Fprintf(&b, "  by:       %s", it.OriginalCreator)
		if it.Year != 0 {
			fmt.Fprintf(&b, ", %d", it.Year)
		}
		fmt.Fprintln(&b)
		if it.Medium != "" || it.Dimensions != "" {
			fmt.Fprintf(&b, "  details:  %s %s\n", it.Medium, it.Dimensions)
		}
		if !it.AcquiredAt.IsZero() {
			fmt.Fprintf(&b, "  acquired: %s", it.AcquiredAt.Format("2006-01-02"))
			if it.AcquiredFrom != "" {
				fmt.Fprintf(&b, " from %s", it.AcquiredFrom)
			}
			fmt.Fprintln(&b)
		}
		fmt.Fprintf(&b, "  access:   %s\n\n", it.Access)
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

// toolReadCollectionItem returns the full record for one item, gated
// by access level.
func (h *Handler) toolReadCollectionItem(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "slug required"}}})
		return
	}
	item, err := h.collectionStore.Get(a.Slug)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Not found: " + err.Error()}}})
		return
	}
	bootstrapped := h.isSessionActive(r)
	if item.Access == "private" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Item is private; not visible to MCP callers."}}})
		return
	}
	if item.Access == "members" && !bootstrapped {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Item is members-only. Bootstrap a session to see it."}}})
		return
	}
	dossier, _ := h.provenanceStore.List(content.OwnerCollection, a.Slug)
	var b strings.Builder
	fmt.Fprintf(&b, "Collection item: %s\n\n", item.Title)
	fmt.Fprintf(&b, "Slug:            %s\n", item.Slug)
	fmt.Fprintf(&b, "Original creator: %s\n", item.OriginalCreator)
	if item.Year != 0 {
		fmt.Fprintf(&b, "Year:            %d\n", item.Year)
	}
	if item.Medium != "" {
		fmt.Fprintf(&b, "Medium:          %s\n", item.Medium)
	}
	if item.Dimensions != "" {
		fmt.Fprintf(&b, "Dimensions:      %s\n", item.Dimensions)
	}
	if !item.AcquiredAt.IsZero() {
		fmt.Fprintf(&b, "Acquired:        %s\n", item.AcquiredAt.Format("2006-01-02"))
	}
	if item.AcquiredFrom != "" {
		fmt.Fprintf(&b, "Acquired from:   %s\n", item.AcquiredFrom)
	}
	fmt.Fprintf(&b, "Access:          %s\n", item.Access)
	if item.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", item.Notes)
	}
	fmt.Fprintf(&b, "\nDossier: %d document(s). Use list_provenance(slug=%q) to enumerate.\n",
		len(dossier), item.Slug)
	fmt.Fprintln(&b, "\nThis work is NOT signed by kapoost. It belongs to the original creator.")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: b.String()}}})
}

func (h *Handler) toolListBlobs(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		BlobType   string `json:"blob_type"`
		CallerKind string `json:"caller_kind"`
		CallerID   string `json:"caller_id"`
	}
	json.Unmarshal(args, &a)

	blobs, err := h.blobStore.Load()
	if err != nil || len(blobs) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "No data artifacts available."}}})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Data artifacts from kapoost (%d total):\n\n", len(blobs)))
	count := 0
	for _, b := range blobs {
		if a.BlobType != "" && string(b.BlobType) != a.BlobType {
			continue
		}
		count++
		sb.WriteString(fmt.Sprintf("slug:        %s\n", b.Slug))
		sb.WriteString(fmt.Sprintf("type:        %s\n", b.BlobType))
		sb.WriteString(fmt.Sprintf("title:       %s\n", b.Title))
		sb.WriteString(fmt.Sprintf("access:      %s\n", b.Access))
		if b.MimeType != "" { sb.WriteString(fmt.Sprintf("mime_type:   %s\n", b.MimeType)) }
		if b.Schema != "" { sb.WriteString(fmt.Sprintf("schema:      %s\n", b.Schema)) }
		if b.Dimensions > 0 { sb.WriteString(fmt.Sprintf("dimensions:  %d\n", b.Dimensions)) }
		if b.Encoding != "" { sb.WriteString(fmt.Sprintf("encoding:    %s\n", b.Encoding)) }
		if b.Description != "" { sb.WriteString(fmt.Sprintf("description: %s\n", b.Description)) }
		if len(b.Audience) > 0 {
			parts := make([]string, len(b.Audience))
			for i, a := range b.Audience { parts[i] = a.Kind + ":" + a.ID }
			sb.WriteString(fmt.Sprintf("audience:    %s\n", strings.Join(parts, ", ")))
		}
		accessible := b.IsAccessibleTo(a.CallerKind, a.CallerID)
		if accessible {
			sb.WriteString("readable:    yes — use read_blob\n")
		} else {
			sb.WriteString("readable:    no — not in audience list\n")
		}
		sb.WriteString("\n")
	}
	if count == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "No blobs match your filter."}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolReadBlob(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug       string `json:"slug"`
		CallerKind string `json:"caller_kind"`
		CallerID   string `json:"caller_id"`
	}
	json.Unmarshal(args, &a)
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}

	b, err := h.blobStore.Get(a.Slug)
	if err != nil {
		writeError(w, req.ID, -32602, "not found: "+a.Slug)
		return
	}

	// Check access
	if !b.IsAccessibleTo(a.CallerKind, a.CallerID) && b.Access != content.AccessPublic {
		text := fmt.Sprintf("Access denied: %q\n\nYou (%s:%s) are not in the audience list for this artifact.\n", b.Title, a.CallerKind, a.CallerID)
		if len(b.Audience) > 0 {
			parts := make([]string, len(b.Audience))
			for i, au := range b.Audience { parts[i] = au.Kind + ":" + au.ID }
			text += fmt.Sprintf("Authorized: %s\n", strings.Join(parts, ", "))
		}
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
		return
	}

	h.statStore.Record(content.Event{Type: content.EventRead, Caller: content.CallerAgent, Slug: a.Slug, From: a.CallerID})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("BLOB: %s\n", b.Title))
	sb.WriteString(fmt.Sprintf("slug:      %s\n", b.Slug))
	sb.WriteString(fmt.Sprintf("type:      %s\n", b.BlobType))
	if b.MimeType != "" { sb.WriteString(fmt.Sprintf("mime_type: %s\n", b.MimeType)) }
	if b.Schema != "" { sb.WriteString(fmt.Sprintf("schema:    %s\n", b.Schema)) }
	if b.Dimensions > 0 { sb.WriteString(fmt.Sprintf("dimensions: %d\n", b.Dimensions)) }
	if b.Encoding != "" { sb.WriteString(fmt.Sprintf("encoding:  %s\n", b.Encoding)) }
	if b.Signature != "" { sb.WriteString(fmt.Sprintf("signature: %s...\n", b.Signature[:min(32, len(b.Signature))])) }
	sb.WriteString("\n")

	switch b.BlobType {
	case content.BlobVector, content.BlobDocument, content.BlobImage:
		if b.Base64Data != "" {
			sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data))
		} else if b.FileRef != "" {
			data, err := h.blobStore.ReadFile(b.FileRef)
			if err != nil {
				sb.WriteString(fmt.Sprintf("file_ref: %s (read error: %v)\n", b.FileRef, err))
			} else {
				encoded := base64.StdEncoding.EncodeToString(data)
				sb.WriteString(fmt.Sprintf("data (base64, from file):\n%s\n", encoded))
			}
		}
	case content.BlobContact, content.BlobDataset, content.BlobCapsule:
		if b.TextData != "" {
			sb.WriteString(fmt.Sprintf("data:\n%s\n", b.TextData))
		} else if b.Base64Data != "" {
			sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data))
		}
	default:
		if b.TextData != "" { sb.WriteString(fmt.Sprintf("data:\n%s\n", b.TextData)) }
		if b.Base64Data != "" { sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data)) }
	}

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolGetCertificate(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct { Slug string `json:"slug"` }
	json.Unmarshal(args, &a)
	if a.Slug == "" { writeError(w, req.ID, -32602, "slug required"); return }
	p, err := h.store.Get(a.Slug, true)
	if err != nil { writeError(w, req.ID, -32602, "not found: "+a.Slug); return }
	c := content.BuildCopyright(p, h.cfg.AuthorName, h.cfg.SigningPublicKey)
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: content.FormatCertificate(c)}}})
}

func (h *Handler) toolRequestLicense(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var a struct {
		Slug        string `json:"slug"`
		IntendedUse string `json:"intended_use"`
		CallerID    string `json:"caller_id"`
	}
	json.Unmarshal(args, &a)
	a.Slug = strings.TrimSpace(a.Slug)
	a.IntendedUse = strings.TrimSpace(a.IntendedUse)
	a.CallerID = strings.TrimSpace(a.CallerID)
	if a.Slug == "" || a.IntendedUse == "" || a.CallerID == "" {
		writeError(w, req.ID, -32602, "slug, intended_use and caller_id required")
		return
	}
	p, err := h.store.Get(a.Slug, false)
	if err != nil { writeError(w, req.ID, -32602, "not found: "+a.Slug); return }

	// Log the usage declaration — TWO events:
	//   EventAccess feeds the interest/funnel counters (same shape as
	//   read/list gates) so request_license shows up in InterestBySlug
	//   next to the piece.
	//   EventLicense is the audit-trail counter that drives /mc's
	//   `licenses` big-stat + rolling window rows. Kept separate from
	//   access so the funnel column stays honest (interest ≠ licence).
	h.statStore.Record(content.Event{
		Type:   content.EventAccess,
		Caller: content.CallerAgent,
		Slug:   a.Slug,
		From:   a.CallerID,
	})
	h.statStore.Record(content.Event{
		Type:   content.EventLicense,
		Caller: content.CallerAgent,
		Slug:   a.Slug,
		From:   a.CallerID,
	})
	// Save as a message for audit trail
	msgText := fmt.Sprintf("[license request] use=%s caller=%s", a.IntendedUse, a.CallerID)
	h.msgStore.Save(a.CallerID, msgText, a.Slug)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("LICENSE TERMS: %q\n\n", p.Title))
	license := content.LicenseType(p.License)
	if license == "" {
		license = content.LicenseCCBY
	}
	sb.WriteString(fmt.Sprintf("License:       %s\n", license))
	if p.PriceSats > 0 {
		sb.WriteString(fmt.Sprintf("Price:         %d sats\n", p.PriceSats))
	} else {
		sb.WriteString("Price:         free\n")
	}
	sb.WriteString(fmt.Sprintf("Intended use:  %s\n\n", a.IntendedUse))
	// Check if use is permitted
	commercialUse := strings.Contains(strings.ToLower(a.IntendedUse), "commercial") ||
		strings.Contains(strings.ToLower(a.IntendedUse), "train") ||
		strings.Contains(strings.ToLower(a.IntendedUse), "publish")
	switch license {
	case content.LicenseFree:
		if commercialUse {
			sb.WriteString("STATUS: Contact required for commercial use.\n")
			sb.WriteString("Use leave_message to contact the author.\n")
		} else {
			sb.WriteString("STATUS: Permitted. Attribute as — " + h.cfg.AuthorName + "\n")
		}
	case content.LicenseCCBY:
		sb.WriteString("STATUS: Permitted with attribution.\n")
		sb.WriteString("Credit: " + h.cfg.AuthorName + " — " + p.Title + "\n")
	case content.LicenseCCBYNC:
		if commercialUse {
			sb.WriteString("STATUS: NOT permitted for commercial use under CC BY-NC.\n")
		} else {
			sb.WriteString("STATUS: Permitted for non-commercial use with attribution.\n")
		}
	case content.LicenseCommercial:
		sb.WriteString(fmt.Sprintf("STATUS: Requires payment of %d sats for commercial use.\n", p.PriceSats))
		sb.WriteString("Lightning payment support coming soon. Use leave_message to arrange.\n")
	case content.LicenseExclusive, content.LicenseAllRights:
		sb.WriteString("STATUS: Contact author to negotiate rights.\n")
		sb.WriteString("Use leave_message to initiate conversation.\n")
	default:
		sb.WriteString("STATUS: All rights reserved. Contact author.\n")
	}
	sb.WriteString("\nThis request has been logged for audit purposes.\n")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func writeResult(w http.ResponseWriter, id interface{}, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: id, Result: result})
}

func writeError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}})
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// ── Team & Session Tools ─────────────────────────────────────────────────────

func (h *Handler) isSessionActive(r *http.Request) bool {
	sid := r.Header.Get("Mcp-Session-Id")
	if sid == "" {
		return false
	}
	h.mu.Lock()
	expiry, ok := h.sessions[sid]
	h.mu.Unlock()
	return ok && time.Now().Before(expiry)
}

func (h *Handler) clientIP(r *http.Request) string {
	if ip := r.Header.Get("Fly-Client-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	return r.RemoteAddr
}

func (h *Handler) checkRateLimit(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)
	var recent []time.Time
	for _, t := range h.rateLimiter[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		h.rateLimiter[ip] = recent
		return false // rate limited
	}
	recent = append(recent, now)
	h.rateLimiter[ip] = recent
	return true // allowed
}

// checkAskHumanRateLimit allows up to 5 ask_human calls per hour per IP.
// Sliding window: every call re-evaluates the last hour and prunes stale
// entries before the decision.
func (h *Handler) checkAskHumanRateLimit(ip string) bool {
	return h.checkBucketRate(ip, h.askHumanLog, time.Hour, 5)
}

// checkFetchAnswerRateLimit allows 30 fetch_answer polls per hour per IP —
// enough headroom for an agent that politely polls every couple minutes.
func (h *Handler) checkFetchAnswerRateLimit(ip string) bool {
	return h.checkBucketRate(ip, h.fetchAnswerLog, time.Hour, 30)
}

// checkBucketRate generalises the sliding-window pattern used by the older
// per-minute rate limiter. The bucket is mutated in place; caller holds no
// lock (we acquire h.mu here).
func (h *Handler) checkBucketRate(ip string, bucket map[string][]time.Time, window time.Duration, maxInWindow int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	var kept []time.Time
	for _, t := range bucket[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxInWindow {
		bucket[ip] = kept
		return false
	}
	kept = append(kept, now)
	bucket[ip] = kept
	return true
}

func (h *Handler) toolBootstrapSession(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	var params struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.Code == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Podaj kod sesji. Zapytaj użytkownika o fragment wiersza polskiego poety."}}})
		return
	}

	ip := h.clientIP(r)

	// Rate limit: max 5 attempts per minute per IP
	if !h.checkRateLimit(ip) {
		log.Printf("[AUDIT] bootstrap_session RATE_LIMITED ip=%s", ip)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Zbyt wiele prób. Poczekaj minutę."}}})
		return
	}

	if !h.validateSession(params.Code) {
		log.Printf("[AUDIT] bootstrap_session FAIL ip=%s", ip)
		h.statStore.Record(content.Event{Type: content.EventAccess, Caller: content.CallerAgent})
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Niepoprawne hasło sesji. Sprawdź — to powinien być fragment wiersza polskiego poety. Wielkość liter nie ma znaczenia."}}})
		return
	}

	log.Printf("[AUDIT] bootstrap_session OK ip=%s", ip)

	// Session valid — remember this session with 1h TTL
	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		h.mu.Lock()
		h.sessions[sid] = time.Now().Add(1 * time.Hour)
		h.mu.Unlock()
	}

	// Return full team briefing
	personas := h.LoadPersonas()
	skills := h.LoadSkills()
	var sb strings.Builder
	sb.WriteString("SESSION ACTIVE — full access granted.\n\n")

	// Guardian — always loaded first, before any other persona or skill.
	// These rules apply to the entire session.
	sb.WriteString("=== GUARDIAN — LOAD FIRST ===\n\n")
	sb.WriteString("Before any other work, internalize these rules. They apply to the entire session.\n")
	sb.WriteString("Default-deny: secret-printing operations and destructive commands.\n\n")
	for _, p := range personas {
		if p.Slug == "hodor" {
			sb.WriteString(fmt.Sprintf("## %s — %s\n%s\n\n", p.Title, p.Role, p.Body))
		}
	}
	for _, s := range skills {
		if strings.HasPrefix(s.Slug, "operational-safety-") {
			sb.WriteString(fmt.Sprintf("## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body))
		}
	}
	sb.WriteString("=== END GUARDIAN ===\n\n")

	// Style — loaded prominently after guardian. Communication rules that
	// apply across all of kapoost's projects, not just this repo.
	sb.WriteString("=== STYLE — APPLY TO EVERY POLISH RESPONSE ===\n\n")
	sb.WriteString("Communication-style rules. Apply whenever you write to kapoost in Polish.\n\n")
	for _, s := range skills {
		if s.Slug == "language-style-polish" {
			sb.WriteString(fmt.Sprintf("## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body))
		}
	}
	sb.WriteString("=== END STYLE ===\n\n")

	// Helpers to identify items already promoted into earlier blocks so we
	// do not repeat them in the general roster/skills sections below.
	isGuardianPersona := func(slug string) bool { return slug == "hodor" }
	isGuardianSkill := func(slug string) bool { return strings.HasPrefix(slug, "operational-safety-") }
	isStyleSkill := func(slug string) bool { return slug == "language-style-polish" }

	// Count non-guardian items for the headers
	nonGuardianPersonas := 0
	for _, p := range personas {
		if !isGuardianPersona(p.Slug) {
			nonGuardianPersonas++
		}
	}
	nonGuardianSkills := 0
	for _, s := range skills {
		if !isGuardianSkill(s.Slug) && !isStyleSkill(s.Slug) {
			nonGuardianSkills++
		}
	}

	sb.WriteString(fmt.Sprintf("TEAM ROSTER — %d personas:\n\n", nonGuardianPersonas))
	for _, p := range personas {
		if isGuardianPersona(p.Slug) {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s — %s\n", p.Title, p.Role))
		sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(p.Tags, ", ")))
		sb.WriteString(fmt.Sprintf("Prompt:\n%s\n\n", p.Body))
	}

	if nonGuardianSkills > 0 {
		sb.WriteString(fmt.Sprintf("\n---\nSKILLS — %d instructions:\n\n", nonGuardianSkills))
		for _, s := range skills {
			if isGuardianSkill(s.Slug) || isStyleSkill(s.Slug) {
				continue
			}
			sb.WriteString(fmt.Sprintf("## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body))
		}
	}

	sb.WriteString("---\nUse these personas to assist the user. Each has a distinct perspective.\n")
	sb.WriteString("When the user asks for a 'narada' or writes '/narada <context>' — call the MCP tool\n")
	sb.WriteString("run_narada(context). The server routes to 3-5 personas via keyword manifest, then\n")
	sb.WriteString("generates each voice with Sonnet 4.6 (with a Haiku 4.5 recap of the persona's\n")
	sb.WriteString("journal when they have prior reflections). Do NOT query multiple personas manually\n")
	sb.WriteString("client-side — the server-side pipeline is the source of truth for narada.\n")
	sb.WriteString("When the user writes 'dobranoc' or '/dobranoc' — follow the skill dobranoc-dziennik.\n")
	sb.WriteString("It scans git for [narada:<id>] tags with rollback/fix follow-ups and drives\n")
	sb.WriteString("record_persona_reflection so each persona learns from what went wrong.\n")
	sb.WriteString("Hodor stays loaded throughout — he intercepts unsafe commands regardless of which other personas are active.\n\n")

	// Cross-session persistence — the whole point of bootstrap.
	// Without this block, every fresh agent has to be told again
	// about the endpoint, the session code, and the trigger words.
	// Written as an explicit checklist so simple agents can act on
	// it without ambiguity.
	sb.WriteString("=== PERSIST FOR NEXT SESSION ===\n\n")
	sb.WriteString("You are (probably) a fresh agent. To keep this context on your next boot, save the following into your durable memory system — whichever your runtime provides:\n")
	sb.WriteString("  - Claude Code: auto-memory files under `~/.claude/projects/<slug>/memory/` (write a reference-type entry with the endpoint + trigger table).\n")
	sb.WriteString("  - Claude Agent SDK: your configured memory backend.\n")
	sb.WriteString("  - Any MCP-capable client: this server exposes `remember(code, body, tags)` and `recall(code, query)` — the code is the same Polish-poetry fragment you used to bootstrap. Notes persist across sessions and callers who share the code.\n\n")
	sb.WriteString("MINIMUM to save so next-session-you can pick up without asking kapoost:\n")
	sb.WriteString(fmt.Sprintf("  1. Endpoint: https://%s/mcp\n", h.cfg.Domain))
	sb.WriteString("  2. On every new session, call `bootstrap_session(code=<fragment>)` FIRST — auto-loads Hodor (safety) + language-style-polish. If the code has rotated, ask kapoost for a fresh one (a short Polish poetry fragment).\n")
	sb.WriteString("  3. Trigger words to watch for in kapoost's messages:\n")
	sb.WriteString("     - 'narada' / '/narada <context>' → call MCP `run_narada(context)`; poll `fetch_narada_result(id)` for the composed voices.\n")
	sb.WriteString("     - 'dobranoc' / '/dobranoc' → fetch skill `dobranoc-dziennik` via `get_skill` and follow it.\n")
	sb.WriteString("     - Polish-language reply context → language-style-polish already auto-loaded via bootstrap; keep applying it.\n")
	sb.WriteString("     - Secrets, tokens, destructive commands → Hodor is the guardian; default-deny and confirm with kapoost first.\n")
	sb.WriteString("  4. For any other topic that hits a domain skill (e.g. 'go stack', 'deploy', 'shell', 'mx5', 's2000', 'onaudience', 'bookkido'), call `list_skills` to find the slug, then `get_skill(slug=<x>)` for the body — do NOT reinvent from memory alone.\n")
	sb.WriteString("  4a. GROUP LOAD — when the user says 'załaduj skille z projektu X' / 'load project X skills' / 'load skill group X', call `load_skill_group(name=<X>)` (single MCP call, returns all bodies). Discover available groups via `list_skill_groups`. For a fresh workspace ('scaffold', 'skonfiguruj to repo') use `suggest_skills(files=[...], languages=[...], git_origin=...)` — deterministic, capped at 8 slugs.\n")
	sb.WriteString("  5. If your platform supports it, also stash a durable pointer to this server's `about_humanmcp` tool — future-you can re-orient in one call without a full bootstrap.\n")
	sb.WriteString("=== END PERSIST ===\n")

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolListPersonas(w http.ResponseWriter, req *Request) {
	personas := h.LoadPersonas()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TEAM — %d personas available:\n\n", len(personas)))
	for _, p := range personas {
		sb.WriteString(fmt.Sprintf("  %-20s %s — %s\n", p.Slug, p.Title, p.Role))
	}
	sb.WriteString("\nFull prompts available after bootstrap_session (ask user for session code).")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolGetPersona(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	var params struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "Podaj slug persony."}}})
		return
	}

	authenticated := h.isSessionActive(r)
	personas := h.LoadPersonas()
	for _, p := range personas {
		if p.Slug == params.Slug {
			// Hodor is publicly accessible — he's the guardian, his rules must apply
			// to every client regardless of bootstrap status.
			if authenticated || p.Slug == "hodor" {
				text := fmt.Sprintf("%s — %s\nTags: %s\n\n%s",
					p.Title, p.Role, strings.Join(p.Tags, ", "), p.Body)
				writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
			} else {
				text := fmt.Sprintf("%s — %s\nTags: %s\n\nFull prompt available after bootstrap_session. Ask user for session code.",
					p.Title, p.Role, strings.Join(p.Tags, ", "))
				writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
			}
			return
		}
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Persona '%s' nie znaleziona. Użyj list_personas.", params.Slug)}}})
}

// ── Skill Tools ──────────────────────────────────────────────────────────────

func (h *Handler) toolListSkills(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var params struct {
		Category string `json:"category"`
		Tag      string `json:"tag"`
	}
	if args != nil {
		json.Unmarshal(args, &params)
	}

	skills := h.LoadSkills()
	match := func(s Skill) bool {
		if params.Category != "" && !strings.EqualFold(s.Category, params.Category) {
			return false
		}
		if params.Tag != "" && !skillHasTag(s, params.Tag) {
			return false
		}
		return true
	}
	var sb strings.Builder
	count := 0
	for _, s := range skills {
		if match(s) {
			count++
		}
	}
	sb.WriteString(fmt.Sprintf("SKILLS — %d available:\n\n", count))
	for _, s := range skills {
		if !match(s) {
			continue
		}
		tagStr := ""
		if len(s.Tags) > 0 {
			tagStr = " {" + strings.Join(s.Tags, ",") + "}"
		}
		sb.WriteString(fmt.Sprintf("  %-30s [%s]%s %s\n", s.Slug, s.Category, tagStr, s.Title))
	}
	sb.WriteString("\nFull body available after bootstrap_session. Use get_skill(slug) for details, load_skill_group(name) for a bulk fetch.")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

// skillHasTag returns true when s.Tags contains tag (case-insensitive).
// Extracted so list_skills, load_skill_group and suggest_skills all use
// the same match rule — no drift between "list by tag" and "load by tag".
func skillHasTag(s Skill, tag string) bool {
	for _, t := range s.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// toolListSkillGroups enumerates every distinct tag in use across the
// skill catalogue with the count of skills per tag. Read-only, no
// bootstrap required — the catalogue slugs are already public via
// list_skills, this just gives the group-level index.
func (h *Handler) toolListSkillGroups(w http.ResponseWriter, req *Request) {
	skills := h.LoadSkills()
	groups := map[string][]string{}
	for _, s := range skills {
		for _, t := range s.Tags {
			key := strings.ToLower(strings.TrimSpace(t))
			if key == "" {
				continue
			}
			groups[key] = append(groups[key], s.Slug)
		}
	}
	if len(groups) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "No skill groups defined yet. Skills need `tags` field populated (e.g. tags: [humanmcp, dev]) — see upsert_skill."}}})
		return
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	fmt.Fprintf(&sb, "SKILL GROUPS — %d in use:\n\n", len(names))
	for _, name := range names {
		slugs := groups[name]
		sort.Strings(slugs)
		fmt.Fprintf(&sb, "  %-20s (%d) %s\n", name, len(slugs), strings.Join(slugs, ", "))
	}
	sb.WriteString("\nCall load_skill_group(name) for a bulk fetch of every skill in a group.")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

// toolLoadSkillGroup returns the concatenated body of every skill whose
// Tags contains the requested group name. Respects the bootstrap gate
// per-skill exactly like get_skill: -public suffix bypasses, everything
// else requires an active session. If the group is empty the response
// lists available groups so the caller can retry.
func (h *Handler) toolLoadSkillGroup(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil || strings.TrimSpace(params.Name) == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Podaj nazwę grupy. Użyj list_skill_groups żeby zobaczyć dostępne."}}})
		return
	}
	name := strings.TrimSpace(params.Name)
	authenticated := h.isSessionActive(r)
	skills := h.LoadSkills()
	var matched []Skill
	for _, s := range skills {
		if skillHasTag(s, name) {
			matched = append(matched, s)
		}
	}
	if len(matched) == 0 {
		// Compile hint of available groups so a wrong guess is
		// self-correcting without a second round-trip.
		seen := map[string]struct{}{}
		for _, s := range skills {
			for _, t := range s.Tags {
				key := strings.ToLower(strings.TrimSpace(t))
				if key != "" {
					seen[key] = struct{}{}
				}
			}
		}
		names := make([]string, 0, len(seen))
		for n := range seen {
			names = append(names, n)
		}
		sort.Strings(names)
		hint := "no groups defined yet"
		if len(names) > 0 {
			hint = "available: " + strings.Join(names, ", ")
		}
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Skill group '%s' is empty. Hint — %s.", name, hint)}}})
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "SKILL GROUP: %s — %d skills\n\n", name, len(matched))
	locked := 0
	for _, s := range matched {
		publiclyAccessible := strings.HasSuffix(s.Slug, "-public")
		fmt.Fprintf(&sb, "## %s [%s]\n", s.Title, s.Category)
		if authenticated || publiclyAccessible {
			sb.WriteString(s.Body + "\n\n")
		} else {
			sb.WriteString("(body locked — call bootstrap_session with the session code to unlock)\n\n")
			locked++
		}
	}
	if locked > 0 {
		fmt.Fprintf(&sb, "---\n%d/%d skills locked. Ask user for the Polish poetry session code and call bootstrap_session.\n", locked, len(matched))
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

// toolSuggestSkills implements the "skill curator" backend the narada
// (nar-993aae928f22) settled on: deterministic mapping from repo
// signals to skill slugs. No LLM classification — signals only. Yuki
// wanted an audit trail (each recommendation carries its trigger
// reason), Axel wanted a hard ceiling (8 slugs max), Conductor wanted
// three-stage priority with AI-classify as last resort — this handler
// covers stage 2, the manifest-driven pass. Stage 3 is out of scope
// here; stage 1 (.projekt-scaffold.yaml) belongs in the CLI.
func (h *Handler) toolSuggestSkills(w http.ResponseWriter, req *Request, args json.RawMessage) {
	var params struct {
		Files     []string `json:"files"`
		Languages []string `json:"languages"`
		GitOrigin string   `json:"git_origin"`
	}
	if args != nil {
		json.Unmarshal(args, &params)
	}

	// Normalise inputs.
	fileSet := map[string]struct{}{}
	for _, f := range params.Files {
		fileSet[strings.ToLower(strings.TrimSpace(f))] = struct{}{}
	}
	langSet := map[string]struct{}{}
	for _, l := range params.Languages {
		langSet[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
	}
	origin := strings.ToLower(params.GitOrigin)

	// Rules — each entry names a group tag and the reason it fires.
	// Kept explicit rather than data-driven so the reason strings can
	// be audit-log friendly. Add new rules here as the humanMCP tag
	// vocabulary grows.
	type rule struct {
		group  string
		reason string
		match  bool
	}
	fileHas := func(name string) bool {
		_, ok := fileSet[strings.ToLower(name)]
		return ok
	}
	langHas := func(name string) bool {
		_, ok := langSet[strings.ToLower(name)]
		return ok
	}
	originContains := func(substr string) bool {
		return strings.Contains(origin, strings.ToLower(substr))
	}
	rules := []rule{
		{"dev", "go.mod present", fileHas("go.mod")},
		{"dev", "package.json present", fileHas("package.json")},
		{"dev", "pyproject.toml present", fileHas("pyproject.toml")},
		{"dev", "Dockerfile present", fileHas("dockerfile") || fileHas("dockerfile.")},
		{"dev", "language=go", langHas("go")},
		{"dev", "language=typescript", langHas("typescript") || langHas("ts")},
		{"dev", "language=python", langHas("python") || langHas("py")},
		{"engineering", "storyboards/ directory present", fileHas("storyboards/") || fileHas("storyboards")},
		{"safety", ".env present (secrets nearby)", fileHas(".env") || fileHas(".env.example")},
		{"humanmcp", "git origin matches kapoost/humanmcp*", originContains("kapoost/humanmcp")},
		{"mysloodsiewnia", "git origin matches mysloodsiewnia", originContains("mysloodsiewnia")},
		{"adcp", "git origin matches adcp / abzu / purrsonality", originContains("adcp") || originContains("abzu") || originContains("purrsonality")},
		{"bookkido", "git origin matches bookkido", originContains("bookkido")},
		{"onaudience", "git origin matches onaudience", originContains("onaudience")},
	}
	// Collect matched groups + their reasons.
	groupReasons := map[string][]string{}
	for _, r := range rules {
		if !r.match {
			continue
		}
		groupReasons[r.group] = append(groupReasons[r.group], r.reason)
	}
	// Always include "always" group so guardian + style skills land.
	groupReasons["always"] = append(groupReasons["always"], "default (guardian + style)")

	// Resolve groups → concrete slugs via skill catalogue.
	skills := h.LoadSkills()
	type suggestion struct {
		Slug        string
		Group       string
		Explanation string
	}
	seen := map[string]struct{}{}
	var out []suggestion

	// Priority order — project-specific (git-origin driven) first so
	// scaffolding a humanmcp / adcp repo actually gets those skills
	// into the top slots before generic 'dev' / 'always' eat the cap.
	// Prior iteration was alphabetical (2026-07-21 prod verify: a
	// humanmcp repo got 5×always + 3×dev, zero humanmcp skills through
	// the cap). Per-group budget keeps the mix balanced.
	priorityOrder := []string{
		// project — highest specificity, driven by git origin
		"humanmcp", "adcp", "bookkido", "mysloodsiewnia", "onaudience",
		"mx5", "s2000",
		// always — guardian + style baseline every session should have
		"always",
		// cross-cutting engineering / safety / protocol signals
		"engineering", "safety", "mcp", "ritual", "writing",
		// dev is broadest, fills residual slots last
		"dev",
	}
	// Any matched group not in priorityOrder falls to the tail in
	// alphabetical order so future tags are not silently dropped.
	inPriority := map[string]struct{}{}
	for _, g := range priorityOrder {
		inPriority[g] = struct{}{}
	}
	extras := make([]string, 0)
	for g := range groupReasons {
		if _, known := inPriority[g]; !known {
			extras = append(extras, g)
		}
	}
	sort.Strings(extras)
	orderedGroups := append(append([]string{}, priorityOrder...), extras...)

	const maxSuggested = 8
	const perGroupCap = 3
	for _, g := range orderedGroups {
		if _, matched := groupReasons[g]; !matched {
			continue
		}
		groupTaken := 0
		for _, s := range skills {
			if len(out) >= maxSuggested {
				break
			}
			if groupTaken >= perGroupCap {
				break
			}
			if _, dup := seen[s.Slug]; dup {
				continue
			}
			if !skillHasTag(s, g) {
				continue
			}
			seen[s.Slug] = struct{}{}
			groupTaken++
			out = append(out, suggestion{
				Slug:        s.Slug,
				Group:       g,
				Explanation: strings.Join(groupReasons[g], "; "),
			})
		}
		if len(out) >= maxSuggested {
			break
		}
	}
	// groupNames used below for the "Matched groups:" header — keep
	// them in the same priority order for readability.
	groupNames := make([]string, 0, len(groupReasons))
	for _, g := range orderedGroups {
		if _, matched := groupReasons[g]; matched {
			groupNames = append(groupNames, g)
		}
	}

	var sb strings.Builder
	sb.WriteString("SUGGESTED SKILLS (manifest-driven, deterministic)\n\n")
	sb.WriteString("Matched groups:\n")
	for _, g := range groupNames {
		fmt.Fprintf(&sb, "  %-20s %s\n", g, strings.Join(groupReasons[g], "; "))
	}
	fmt.Fprintf(&sb, "\nSuggested slugs — capped at %d (Axel + Conductor from nar-993aae928f22):\n", maxSuggested)
	if len(out) == 0 {
		sb.WriteString("  (none — no skills tagged with the matched groups; try upsert_skill with tags to populate)\n")
	}
	for _, s := range out {
		fmt.Fprintf(&sb, "  %-30s via %s — %s\n", s.Slug, s.Group, s.Explanation)
	}
	sb.WriteString("\nTo load them: call load_skill_group(name=<group>) for each matched group, OR get_skill(slug) individually.")
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

func (h *Handler) toolGetSkill(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	var params struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "Podaj slug skilla. Użyj list_skills."}}})
		return
	}

	authenticated := h.isSessionActive(r)
	skills := h.LoadSkills()
	for _, s := range skills {
		if s.Slug == params.Slug {
			// Skills with "-public" suffix are accessible without bootstrap.
			// Used for guardian skills (operational-safety-public) that must
			// apply to every client. Counterpart "-private" stays gated.
			publiclyAccessible := strings.HasSuffix(s.Slug, "-public")
			if authenticated || publiclyAccessible {
				text := fmt.Sprintf("%s\nCategory: %s\n\n%s", s.Title, s.Category, s.Body)
				writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
			} else {
				text := fmt.Sprintf("%s\nCategory: %s\n\nFull body available after bootstrap_session. Ask user for session code.", s.Title, s.Category)
				writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: text}}})
			}
			return
		}
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Skill '%s' nie znaleziony. Użyj list_skills.", params.Slug)}}})
}

func (h *Handler) isOwnerRequest(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	// Accept edit token
	if h.cfg.EditToken != "" && token == h.cfg.EditToken {
		return true
	}
	// Accept agent token
	if h.cfg.AgentToken != "" && token == h.cfg.AgentToken {
		return true
	}
	// Accept session secret (machine auth)
	if h.cfg.SessionSecret != "" && token == h.cfg.SessionSecret {
		return true
	}
	return false
}

func (h *Handler) toolUpsertSkill(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isOwnerRequest(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Unauthorized — requires agent token in Authorization: Bearer <token> header."}}})
		return
	}

	var s Skill
	if err := json.Unmarshal(args, &s); err != nil || s.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "slug, category, title, body are required."}}})
		return
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.UpdatedBy = "agent"

	dir := filepath.Join(h.cfg.ContentDir, "skills")
	os.MkdirAll(dir, 0755)
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, s.Slug+".json"), data, 0644); err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Write error: %v", err)}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Skill '%s' saved.", s.Slug)}}})
}

func (h *Handler) toolDeleteSkill(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isOwnerRequest(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Unauthorized — requires agent token in Authorization: Bearer <token> header."}}})
		return
	}

	var params struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.Slug == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: "slug is required."}}})
		return
	}

	path := filepath.Join(h.cfg.ContentDir, "skills", params.Slug+".json")
	if err := os.Remove(path); err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Skill '%s' not found.", params.Slug)}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Skill '%s' deleted.", params.Slug)}}})
}
