// Package mcp exposes Backend — the shared state holder used by the
// v2 SDK handler (internal/mcp/v2) and any future in-process caller.
// After Tier C.d, this is the ONLY entry into MCP-facing state; the
// legacy v1 handler.go, its tool dispatch, and the parity_test are gone.
package mcp

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
	personapkg "github.com/kapoost/humanmcp-go/internal/personas"
	"github.com/kapoost/humanmcp-go/internal/ratelimit"
	"github.com/kapoost/humanmcp-go/internal/rituals"
)

// Persona is re-exported from internal/personas so callers already on the
// mcp.Persona name (v2 discovery/team) keep compiling. Do NOT delete this
// alias — several v2 tool files reference it.
type Persona = personapkg.Persona

// Skill is the JSON shape of a bundled skill file (content/skills/<slug>.json).
// Exposed here (not in a dedicated package) because it's small and only used
// by Backend loaders + v2 dispatch rendering.
type Skill struct {
	Slug      string   `json:"slug"`
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	UpdatedBy string   `json:"updated_by,omitempty"`
}

// Backend owns every store, rate limiter, and long-lived worker the v2 SDK
// handler needs to answer a tool call. One instance per process, constructed
// at boot in cmd/server/main.go.
type Backend struct {
	cfg             *config.Config
	store           *content.Store
	auth            *auth.Auth
	msgStore        *content.MessageStore
	statStore       *content.StatStore
	blobStore       *content.BlobStore
	questionStore   *content.QuestionStore
	memoryStore     *content.MemoryStore
	provenanceStore *content.ProvenanceStore
	collectionStore *content.CollectionStore
	// ritualWorker owns ritualStore + journalStore + llm + narada rate
	// limiters. See internal/rituals.
	ritualWorker *rituals.Worker
	sessions     map[string]time.Time // v1 Mcp-Session-Id → expiry (dual-read alongside v2 HMAC tokens)

	mu sync.Mutex
	// Rate limiters — keyed sliding windows, unified via internal/ratelimit.
	bootstrapBucket   *ratelimit.Bucket // per-IP: 5/min bootstrap_session
	askHumanBucket    *ratelimit.Bucket // per-IP: 5/hr ask_human
	fetchAnswerBucket *ratelimit.Bucket // per-IP: 30/hr fetch_answer polls
	friendTokenBucket *ratelimit.Bucket // per-tokenID: 1h window, per-token limit via AllowWithLimit

	// Bridge into the mysłoodsiewnia vault. Nil ⇒ tools report offline.
	liveness    *mysloodsiewnia.Liveness
	bridgeQueue *mysloodsiewnia.Queue
}

// NewBackend constructs the shared state. Callers must have already built
// the rituals.Worker (and typically Start()-ed it) so Backend and Worker
// share the same instance.
func NewBackend(cfg *config.Config, store *content.Store, a *auth.Auth, worker *rituals.Worker) *Backend {
	b := &Backend{
		cfg:               cfg,
		store:             store,
		auth:              a,
		msgStore:          content.NewMessageStore(cfg.ContentDir),
		statStore:         content.NewStatStore(cfg.ContentDir),
		blobStore:         content.NewBlobStore(cfg.ContentDir),
		questionStore:     content.NewQuestionStore(cfg.ContentDir),
		memoryStore:       content.NewMemoryStore(cfg.ContentDir),
		provenanceStore:   content.NewProvenanceStore(cfg.ContentDir),
		collectionStore:   content.NewCollectionStore(cfg.ContentDir),
		ritualWorker:      worker,
		sessions:          make(map[string]time.Time),
		bootstrapBucket:   ratelimit.New(time.Minute, 5, nil),
		askHumanBucket:    ratelimit.New(time.Hour, 5, nil),
		fetchAnswerBucket: ratelimit.New(time.Hour, 30, nil),
		// friendTokenBucket: default 30/hr, per-call overrides via AllowWithLimit.
		friendTokenBucket: ratelimit.New(time.Hour, 30, nil),
	}
	go b.cleanupLoop()
	return b
}

// ── Store accessors (implement v2 Source interface) ─────────────────────────

func (b *Backend) Config() *config.Config                              { return b.cfg }
func (b *Backend) Store() *content.Store                               { return b.store }
func (b *Backend) StatStore() *content.StatStore                       { return b.statStore }
func (b *Backend) ProvenanceStore() *content.ProvenanceStore           { return b.provenanceStore }
func (b *Backend) CollectionStore() *content.CollectionStore           { return b.collectionStore }
func (b *Backend) BlobStore() *content.BlobStore                       { return b.blobStore }
func (b *Backend) MsgStore() *content.MessageStore                     { return b.msgStore }
func (b *Backend) MemoryStore() *content.MemoryStore                   { return b.memoryStore }
func (b *Backend) QuestionStore() *content.QuestionStore               { return b.questionStore }
func (b *Backend) RitualStore() *content.RitualStore                   { return b.ritualWorker.RitualStore() }
func (b *Backend) PersonaJournalStore() *content.PersonaJournalStore   { return b.ritualWorker.PersonaJournalStore() }
func (b *Backend) LLMAvailable() bool                                  { return b.ritualWorker.LLMAvailable() }

// ── Rate-limit accessors ────────────────────────────────────────────────────

func (b *Backend) CheckAskHumanRateLimit(ip string) bool {
	allowed, _ := b.askHumanBucket.Allow(ip)
	return allowed
}
func (b *Backend) CheckFetchAnswerRateLimit(ip string) bool {
	allowed, _ := b.fetchAnswerBucket.Allow(ip)
	return allowed
}
func (b *Backend) CheckNaradaRateLimit(ip string) bool {
	return b.ritualWorker.CheckNaradaRateLimit(ip)
}
func (b *Backend) CheckNaradaFetchRateLimit(ip string) bool {
	return b.ritualWorker.CheckNaradaFetchRateLimit(ip)
}

// CheckBootstrapRateLimit exposes the bootstrap bucket. Same 5/min/IP budget.
func (b *Backend) CheckBootstrapRateLimit(ip string) bool {
	allowed, _ := b.bootstrapBucket.Allow(ip)
	return allowed
}

// ── Session / auth ──────────────────────────────────────────────────────────

// ValidateSessionCode reports whether the given poem fragment (or session
// secret) unlocks a team-tier session. Diacritic-tolerant so typos still
// match POET_POOL entries and diacritic-stripping agents get in.
func (b *Backend) ValidateSessionCode(code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	if b.cfg.SessionSecret != "" && code == strings.ToLower(b.cfg.SessionSecret) {
		return true
	}
	normalized := normalizePoem(code)
	current, previous := b.cfg.PickActivePoem(time.Now())
	if current != "" && normalized == normalizePoem(strings.ToLower(current)) {
		return true
	}
	if previous != "" && normalized == normalizePoem(strings.ToLower(previous)) {
		return true
	}
	return false
}

// IsOwnerRequestByHeaders reports whether Authorization: Bearer <token>
// matches any of the three configured owner tokens (edit / agent / session).
func (b *Backend) IsOwnerRequestByHeaders(hdr http.Header) bool {
	authHeader := hdr.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if b.cfg.EditToken != "" && token == b.cfg.EditToken {
		return true
	}
	if b.cfg.AgentToken != "" && token == b.cfg.AgentToken {
		return true
	}
	if b.cfg.SessionSecret != "" && token == b.cfg.SessionSecret {
		return true
	}
	return false
}

// IsSessionActiveByHeaders dual-reads the two active-session channels:
// v1 Mcp-Session-Id map first, then v2 HMAC Bearer session token. Either
// signal is enough — this call is idempotent, no state mutation.
func (b *Backend) IsSessionActiveByHeaders(hdr http.Header) bool {
	if sid := hdr.Get("Mcp-Session-Id"); sid != "" {
		b.mu.Lock()
		expiry, ok := b.sessions[sid]
		b.mu.Unlock()
		if ok && time.Now().Before(expiry) {
			return true
		}
	}
	authHeader := hdr.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if !strings.Contains(token, ".") {
		return false
	}
	return ValidateSessionToken(token, b.cfg.SessionSecret)
}

// ClientIPFromHeaders extracts the real client IP from Fly / X-Forwarded-For,
// falling back to empty string when neither is present (the SDK handler
// only receives req.Extra.Header, not the full request).
func (b *Backend) ClientIPFromHeaders(hdr http.Header) string {
	if ip := hdr.Get("Fly-Client-IP"); ip != "" {
		return ip
	}
	if ip := hdr.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	return ""
}

// SetBridge wires the shared liveness + queue from cmd/server/main.go so
// mysloodsiewnia_* tools can gate on vault reachability. Optional — if
// never called, the tools deterministically report offline.
func (b *Backend) SetBridge(liveness *mysloodsiewnia.Liveness, queue *mysloodsiewnia.Queue) {
	b.liveness = liveness
	b.bridgeQueue = queue
}

// Liveness returns the shared vault liveness store.
func (b *Backend) Liveness() *mysloodsiewnia.Liveness { return b.liveness }

// BridgeQueue returns the shared vault op queue.
func (b *Backend) BridgeQueue() *mysloodsiewnia.Queue { return b.bridgeQueue }

// ── Content loaders ─────────────────────────────────────────────────────────

// LoadPersonas walks content/personas/ and returns every valid persona.
func (b *Backend) LoadPersonas() []Persona {
	return personapkg.LoadAll(b.cfg.ContentDir)
}

// LoadSkills walks content/skills/ and returns every JSON skill file.
func (b *Backend) LoadSkills() []Skill {
	dir := filepath.Join(b.cfg.ContentDir, "skills")
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

// ── Rituals passthroughs (v2 Source interface) ──────────────────────────────

// CreateNaradaJob delegates to the shared rituals.Worker.
func (b *Backend) CreateNaradaJob(ctxText, from string, explicit []string) (content.RitualJob, []string, error) {
	return b.ritualWorker.CreateNaradaJob(ctxText, from, explicit)
}

// BuildNaradaPack delegates to the shared rituals.Worker.
func (b *Backend) BuildNaradaPack(ctxText string, explicit []string) (content.NaradaPack, error) {
	return b.ritualWorker.BuildNaradaPack(ctxText, explicit)
}

// WriteReflection delegates to the shared rituals.Worker.
func (b *Backend) WriteReflection(naradaID, personaSlug, errorContext string) (string, error) {
	return b.ritualWorker.WriteReflection(naradaID, personaSlug, errorContext)
}

// SynthesisePersonaPatternsBySlug delegates to the shared rituals.Worker.
func (b *Backend) SynthesisePersonaPatternsBySlug(slug string) (content.PersonaPatterns, int, error) {
	return b.ritualWorker.SynthesisePersonaPatternsBySlug(slug)
}

// ── Background maintenance ──────────────────────────────────────────────────

func (b *Backend) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		b.mu.Lock()
		for sid, expiry := range b.sessions {
			if now.After(expiry) {
				delete(b.sessions, sid)
			}
		}
		b.mu.Unlock()
		b.bootstrapBucket.Prune()
		b.askHumanBucket.Prune()
		b.fetchAnswerBucket.Prune()
		b.friendTokenBucket.Prune()
	}
}

// ── Utility (package-level) ─────────────────────────────────────────────────

// NormalizePoem strips Polish (and stray Czech) diacritics from a session
// code and collapses whitespace. Public so external tooling (e.g. poetgen)
// can normalise pool candidates identically to what the auth path does.
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
	fields := strings.Fields(out)
	return strings.Join(fields, " ")
}
