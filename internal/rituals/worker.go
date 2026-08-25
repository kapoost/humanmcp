// Package rituals owns the async narada pipeline: routing personas via
// manifest, generating each voice via Sonnet, journaling reflections, and
// periodically synthesising raw journal entries into pattern sets. Kept
// separate from internal/mcp so that non-HTTP consumers (Wave 4 horcrux,
// background jobs) can drive rituals without pulling the whole handler.
package rituals

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/llm"
	"github.com/kapoost/humanmcp-go/internal/personas"
	"github.com/kapoost/humanmcp-go/internal/ratelimit"
)

// Worker owns every store, limiter and LLM client the ritual pipeline needs.
// One instance per process; Start spawns the background loops.
type Worker struct {
	cfg          *config.Config
	ritualStore  *content.RitualStore
	journalStore *content.PersonaJournalStore
	llm          *llm.Client

	naradaBucket      *ratelimit.Bucket // per-IP: 5/hr run_narada
	naradaFetchBucket *ratelimit.Bucket // per-IP: 60/hr fetch_narada_result polls
}

// New builds a Worker with its stores rooted at cfg.ContentDir and an LLM
// client keyed by cfg.ClaudeAPIKey. Buckets are per-hour, matching the v1
// handler's original quotas so drop-in replacement preserves behavior.
func New(cfg *config.Config) *Worker {
	return &Worker{
		cfg:               cfg,
		ritualStore:       content.NewRitualStore(cfg.ContentDir),
		journalStore:      content.NewPersonaJournalStore(cfg.ContentDir),
		llm:               llm.New(cfg.ClaudeAPIKey),
		naradaBucket:      ratelimit.New(time.Hour, 5, nil),
		naradaFetchBucket: ratelimit.New(time.Hour, 60, nil),
	}
}

// Start launches the background loops. Safe to call once at process boot.
func (w *Worker) Start() {
	go w.naradaWorkerLoop()
	go w.patternSynthesisLoop()
	go w.cleanupLoop()
}

// Accessors — narrow surface for callers (v1 tool dispatch, v2 SDK handler).

func (w *Worker) RitualStore() *content.RitualStore                 { return w.ritualStore }
func (w *Worker) PersonaJournalStore() *content.PersonaJournalStore { return w.journalStore }
func (w *Worker) LLMAvailable() bool                                { return w.llm.Available() }

// CheckNaradaRateLimit reports whether the given IP is under quota for
// run_narada. Records the attempt when returning true.
func (w *Worker) CheckNaradaRateLimit(ip string) bool {
	allowed, _ := w.naradaBucket.Allow(ip)
	return allowed
}

// CheckNaradaFetchRateLimit reports whether the given IP is under quota
// for fetch_narada_result polls.
func (w *Worker) CheckNaradaFetchRateLimit(ip string) bool {
	allowed, _ := w.naradaFetchBucket.Allow(ip)
	return allowed
}

func (w *Worker) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		w.naradaBucket.Prune()
		w.naradaFetchBucket.Prune()
	}
}

// ── Public pipeline entrypoints (called from v1 dispatch + v2 SDK handler) ─

// CreateNaradaJob picks the personas for a narada and creates a pending
// RitualJob picked up by naradaWorkerLoop. Returns the job plus the
// selected personas list so callers can format their own "Narada
// started" reply.
//
// explicit is the caller-supplied persona list. When non-empty it wins
// outright and the keyword manifest is never consulted — the router
// scores nothing, so a context that happens to contain "design" or
// "log" pulls in architecture/defensive voices no matter how much
// on-topic material surrounds them, and there was previously no way for
// a caller who knew exactly whose expertise they wanted to say so.
func (w *Worker) CreateNaradaJob(ctxText, from string, explicit []string) (content.RitualJob, []string, error) {
	manifest, err := content.LoadRitualManifest(w.cfg.ContentDir, "narada")
	if err != nil {
		return content.RitualJob{}, nil, fmt.Errorf("Could not load narada manifest: %w", err)
	}
	selected, err := w.resolveNaradaPersonas(manifest, ctxText, explicit)
	if err != nil {
		return content.RitualJob{}, nil, err
	}
	job, err := w.ritualStore.Create("narada", ctxText, selected)
	if err != nil {
		return content.RitualJob{}, nil, fmt.Errorf("Could not create narada job: %w", err)
	}
	return job, selected, nil
}

// BuildNaradaPack assembles an offline narada: the same panel selection as
// CreateNaradaJob, but instead of queueing Sonnet calls it returns the
// prompts themselves so the caller can run each persona as their own
// subagent.
//
// Nothing is written to disk. That is the point and also the cost — an
// offline narada has no ID, so it carries no [narada:<id>] commit tag and
// the personas' journals never learn from it. The rendered reply says so;
// see registerPrepareNarada.
//
// Worth knowing when choosing between the two: server-side personas only
// ever see the context string, while the caller's subagents can usually
// read the actual repository. For questions about code in front of the
// caller, offline is frequently the better narada, not the fallback one.
func (w *Worker) BuildNaradaPack(ctxText string, explicit []string) (content.NaradaPack, error) {
	manifest, err := content.LoadRitualManifest(w.cfg.ContentDir, "narada")
	if err != nil {
		return content.NaradaPack{}, fmt.Errorf("Could not load narada manifest: %w", err)
	}
	selected, err := w.resolveNaradaPersonas(manifest, ctxText, explicit)
	if err != nil {
		return content.NaradaPack{}, err
	}
	pack := content.NaradaPack{
		Context: ctxText,
		Routed:  len(normalizeSlugs(explicit)) == 0,
	}
	for _, slug := range selected {
		p, err := personas.Load(w.cfg.ContentDir, slug)
		if err != nil {
			// Mirror the online pipeline, which loses this one voice and
			// still completes the job. Failing the whole pack here would
			// mean one stale entry in default_personas takes down offline
			// narady entirely. Recorded in Missing so the caller sees a
			// short panel as a fact, not as the panel they asked for.
			log.Printf("[narada-pack] skipping %s: %v", slug, err)
			pack.Missing = append(pack.Missing, slug)
			continue
		}
		recap, source := w.offlineJournalRecap(slug)
		pack.Personas = append(pack.Personas, content.NaradaVoicePrompt{
			Slug:          slug,
			Title:         p.Title,
			Role:          p.Role,
			System:        buildSystemPrompt(p.Body, recap),
			User:          buildUserPrompt(ctxText),
			JournalSource: source,
		})
	}
	if len(pack.Personas) == 0 {
		return content.NaradaPack{}, fmt.Errorf("None of the selected personas could be loaded: %s. Check content/personas/ against content/rituals/narada.json.",
			strings.Join(pack.Missing, ", "))
	}
	return pack, nil
}

// offlineJournalRecap is the no-LLM counterpart of summariseJournal. The
// online path spends a Haiku call narrowing the persona's lessons to the
// ones this context needs; with no model in the loop the honest move is to
// ship the whole synthesised set and let the caller's subagent do the
// narrowing — it is reading the same material with a full model anyway.
//
// Raw journal entries are the fallback for personas whose patterns have
// not been synthesised yet, capped because unlike the patterns file they
// grow without bound and would crowd out the persona's own prompt.
func (w *Worker) offlineJournalRecap(slug string) (recap, source string) {
	if patterns, _ := w.journalStore.ReadPatterns(slug); strings.TrimSpace(patterns.Patterns) != "" {
		return fmt.Sprintf("Twoje utrwalone wzorce (synteza z %d wpisów, %s) — wszystkie, nie tylko te trafne dla tej narady:\n\n%s",
			patterns.EntriesAtSynthesis,
			patterns.SynthesisedAt.Format("2006-01-02"),
			strings.TrimSpace(patterns.Patterns)), "patterns"
	}
	entries, err := w.journalStore.List(slug)
	if err != nil || len(entries) == 0 {
		return "", ""
	}
	const maxEntries = 5
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	var b strings.Builder
	b.WriteString("Twoje ostatnie wnioski z dziennika pomyłek (najnowsze pierwsze):\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- %s (%s): %s\n", e.At.Format("2006-01-02"), e.NaradaID, oneLine(e.Reflection))
	}
	return strings.TrimSpace(b.String()), "journal"
}

// resolveNaradaPersonas returns the slugs a narada will run: the caller's
// explicit list when given, otherwise the keyword-manifest route.
//
// An explicit list deliberately bypasses min_personas — asking for a
// single voice is a legitimate request, and padding it from
// default_personas would reintroduce exactly the unasked-for voices the
// caller was trying to avoid. max_personas still applies, since each
// persona costs one Sonnet call.
func (w *Worker) resolveNaradaPersonas(m *content.RitualManifest, ctxText string, explicit []string) ([]string, error) {
	requested := normalizeSlugs(explicit)
	if len(requested) == 0 {
		selected := m.RoutePersonas(ctxText)
		if len(selected) == 0 {
			return nil, fmt.Errorf("Narada router returned no personas — check content/rituals/narada.json defaults.")
		}
		return selected, nil
	}
	if len(requested) > m.MaxPersonas {
		return nil, fmt.Errorf("Too many personas: %d requested, max is %d (one Sonnet call each). Trim the list, or raise max_personas in content/rituals/narada.json.",
			len(requested), m.MaxPersonas)
	}

	// Unknown slugs are a hard error, not a silent drop. A caller who
	// names five personas and silently gets three back cannot tell a
	// typo ("contrarian" instead of "lukasz-mazur") from a router
	// disagreement — which is the same class of invisible failure that
	// made the keyword routing hard to diagnose in the first place.
	roster := map[string]bool{}
	var known []string
	for _, p := range personas.LoadAll(w.cfg.ContentDir) {
		roster[p.Slug] = true
		known = append(known, p.Slug)
	}
	var unknown []string
	for _, slug := range requested {
		if !roster[slug] {
			unknown = append(unknown, slug)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(known)
		return nil, fmt.Errorf("Unknown persona slug(s): %s. Use list_personas for slugs — they are full slugs, not display names (e.g. lukasz-mazur, not \"contrarian\"). Available: %s.",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return requested, nil
}

// normalizeSlugs lowercases, trims, drops empties and dedupes while
// preserving caller order — the order personas are listed in is the
// order their voices come back.
func normalizeSlugs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// WriteReflection runs the LLM reflection pipeline on a persona voice from
// a completed narada, appends the result to the persona journal, and
// returns the reflection text.
func (w *Worker) WriteReflection(naradaID, personaSlug, errorContext string) (string, error) {
	job, err := w.ritualStore.Get(naradaID)
	if err != nil {
		return "", fmt.Errorf("narada not found: %w", err)
	}
	var recommendation string
	for _, v := range job.Voices {
		if v.Slug == personaSlug {
			recommendation = v.Recommendation
			break
		}
	}
	if recommendation == "" {
		return "", fmt.Errorf("persona %q did not speak on narada %q — cannot reflect", personaSlug, naradaID)
	}
	persona, err := personas.Load(w.cfg.ContentDir, personaSlug)
	if err != nil {
		return "", fmt.Errorf("load persona %s: %w", personaSlug, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	systemPrompt := buildReflectionSystemPrompt(persona.Body, w.journalStore, personaSlug)
	userPrompt := buildReflectionUserPrompt(job.Context, recommendation, errorContext)
	res, err := w.llm.Complete(ctx, llm.CompleteRequest{
		Model:     modelForPersona(persona),
		System:    systemPrompt,
		User:      userPrompt,
		MaxTokens: 512,
	})
	if err != nil {
		return "", fmt.Errorf("llm error: %w", err)
	}
	entry := content.PersonaJournalEntry{
		At:             time.Now().UTC(),
		NaradaID:       naradaID,
		Context:        oneLine(job.Context),
		Recommendation: oneLine(recommendation),
		ErrorSignal:    oneLine(errorContext),
		Reflection:     res.Text,
	}
	if err := w.journalStore.Append(personaSlug, entry); err != nil {
		return "", fmt.Errorf("journal append failed: %w", err)
	}
	return res.Text, nil
}

// SynthesisePersonaPatternsBySlug runs a forced Sonnet synthesis over the
// persona's raw journal, replacing the previous pattern set. Returns
// (zero, 0, nil) when the persona has no journal entries yet so the caller
// can distinguish "nothing to do" from "error".
func (w *Worker) SynthesisePersonaPatternsBySlug(slug string) (content.PersonaPatterns, int, error) {
	entries, err := w.journalStore.List(slug)
	if err != nil {
		return content.PersonaPatterns{}, 0, err
	}
	if len(entries) == 0 {
		return content.PersonaPatterns{}, 0, nil
	}
	persona, err := personas.Load(w.cfg.ContentDir, slug)
	if err != nil {
		return content.PersonaPatterns{}, 0, err
	}
	previous, _ := w.journalStore.ReadPatterns(slug)

	var journal strings.Builder
	journal.WriteString("Twoje wpisy do dziennika (najnowsze najpierw):\n\n")
	for i, e := range entries {
		fmt.Fprintf(&journal, "### Wpis %d — %s (%s)\n", i+1, e.At.Format("2006-01-02"), e.NaradaID)
		if s := oneLine(e.Context); s != "" {
			fmt.Fprintf(&journal, "Kontekst: %s\n", s)
		}
		if s := oneLine(e.Recommendation); s != "" {
			fmt.Fprintf(&journal, "Rekomendacja: %s\n", s)
		}
		if s := oneLine(e.ErrorSignal); s != "" {
			fmt.Fprintf(&journal, "Sygnał błędu: %s\n", s)
		}
		fmt.Fprintf(&journal, "Wniosek: %s\n\n", strings.TrimSpace(e.Reflection))
	}
	if previous.Patterns != "" {
		fmt.Fprintf(&journal, "\n---\n\nPoprzednia synteza wzorców (na %d wpisach, %s):\n\n%s\n",
			previous.EntriesAtSynthesis, previous.SynthesisedAt.Format("2006-01-02"), previous.Patterns)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := w.llm.Complete(ctx, llm.CompleteRequest{
		Model: llm.ModelSonnet46,
		System: fmt.Sprintf(`%s

Jesteś teraz w trybie autorefleksji nad własnym dziennikiem pomyłek. Twoim zadaniem jest wyciągnąć 3-5 TRWAŁYCH wzorców — nie pojedynczych incydentów, tylko powtarzających się nawyków, ślepych punktów lub błędnych założeń, które widać przez wiele wpisów. Pisz w drugiej osobie, konkretnie, po polsku, w swoim charakterystycznym rytmie. Format: numerowana lista, każdy wzorzec zaczyna się od pogrubionej etykiety (2-4 słowa), po niej myślnik i 1-2 zdania opisu z konkretem, na co uważać. Jeśli poprzednia synteza wciąż jest aktualna dla części wzorców — zachowaj je, ale przeformułuj żeby były świeże po najnowszych wpisach. Bez dydaktyzmu, bez ogólników.`, persona.Body),
		User:      journal.String(),
		MaxTokens: 800,
	})
	if err != nil {
		return content.PersonaPatterns{}, len(entries), err
	}
	patterns := content.PersonaPatterns{
		SynthesisedAt:      time.Now().UTC(),
		EntriesAtSynthesis: len(entries),
		Patterns:           strings.TrimSpace(res.Text),
		Model:              llm.ModelSonnet46,
	}
	if err := w.journalStore.WritePatterns(slug, patterns); err != nil {
		return content.PersonaPatterns{}, len(entries), err
	}
	log.Printf("[patterns] synthesised %s: %d entries → %d chars of patterns", slug, len(entries), len(patterns.Patterns))
	return patterns, len(entries), nil
}

// ── Worker internals ────────────────────────────────────────────────────────

// naradaWorkerLoop drains the pending narada queue. Runs one job per tick
// to keep the worker predictable and easy to debug.
func (w *Worker) naradaWorkerLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		pending, err := w.ritualStore.ListPending()
		if err != nil || len(pending) == 0 {
			continue
		}
		for _, job := range pending {
			w.processNaradaJob(job)
		}
	}
}

func (w *Worker) processNaradaJob(job content.RitualJob) {
	if err := w.ritualStore.MarkRunning(job.ID); err != nil {
		log.Printf("[narada-worker] mark running %s: %v", job.ID, err)
		return
	}

	// Run every persona in parallel — narada is capped at 5 personas so a
	// simple goroutine-per-persona fan-out is cheap and matches how the
	// mysłoodsiewnia Python side does it with asyncio.gather. Sequential
	// was 5×Sonnet ≈ 50s; parallel is bounded by the slowest single call.
	type indexed struct {
		i     int
		voice content.PersonaVoice
		err   error
	}
	results := make(chan indexed, len(job.Personas))
	var wg sync.WaitGroup
	for i, slug := range job.Personas {
		wg.Add(1)
		go func(i int, slug string) {
			defer wg.Done()
			voice, err := w.generatePersonaVoice(slug, job.Context)
			results <- indexed{i: i, voice: voice, err: err}
		}(i, slug)
	}
	wg.Wait()
	close(results)

	// Re-order by manifest position so the response is deterministic.
	slots := make([]*content.PersonaVoice, len(job.Personas))
	for r := range results {
		if r.err != nil {
			log.Printf("[narada-worker] persona %s failed for job %s: %v", job.Personas[r.i], job.ID, r.err)
			continue
		}
		v := r.voice
		slots[r.i] = &v
	}
	voices := make([]content.PersonaVoice, 0, len(slots))
	for _, v := range slots {
		if v != nil {
			voices = append(voices, *v)
		}
	}

	if len(voices) == 0 {
		_ = w.ritualStore.Fail(job.ID, "no voices generated")
		return
	}
	if err := w.ritualStore.Complete(job.ID, voices); err != nil {
		log.Printf("[narada-worker] complete %s: %v", job.ID, err)
	}
}

// generatePersonaVoice runs the two-model pipeline for one persona:
//  1. Load persona body from content/personas/<slug>.md.
//  2. If the persona has a journal, ask Haiku 4.5 to compress it into a
//     focused paragraph (3-5 sentences) framed by the current narada
//     context. Skipped when the journal is empty.
//  3. Ask Sonnet 4.6 to speak in the persona's voice, with the journal
//     summary appended to the system prompt so the persona "remembers".
//
// Graceful degradation: if the API key is missing or LLM errors, we fall
// back to a stub voice so the async plumbing keeps working in local dev
// and during outages.
func (w *Worker) generatePersonaVoice(slug, naradaContext string) (content.PersonaVoice, error) {
	persona, err := personas.Load(w.cfg.ContentDir, slug)
	if err != nil {
		return content.PersonaVoice{}, fmt.Errorf("load persona %s: %w", slug, err)
	}
	if !w.llm.Available() {
		return stubVoice(slug, naradaContext, "no api key configured"), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	journalSummary := w.summariseJournal(ctx, slug, naradaContext)
	systemPrompt := buildSystemPrompt(persona.Body, journalSummary)
	userPrompt := buildUserPrompt(naradaContext)
	model := modelForPersona(persona)

	res, err := w.llm.Complete(ctx, llm.CompleteRequest{
		Model:     model,
		System:    systemPrompt,
		User:      userPrompt,
		MaxTokens: 1024,
	})
	if err != nil {
		log.Printf("[narada] %s failed for %s: %v — falling back to stub", model, slug, err)
		return stubVoice(slug, naradaContext, err.Error()), nil
	}
	return content.PersonaVoice{
		Slug:           slug,
		Recommendation: res.Text,
		ModelUsed:      model,
	}, nil
}

// summariseJournal returns a short recap of the persona's past reflections,
// focused on lessons that might apply to the current narada context. Prefers
// the synthesised pattern file over raw journal entries — patterns are the
// compressed, curated view; the raw journal is only used as a fallback for
// personas whose pattern set is missing or stale.
func (w *Worker) summariseJournal(ctx context.Context, slug, naradaContext string) string {
	patterns, _ := w.journalStore.ReadPatterns(slug)
	if strings.TrimSpace(patterns.Patterns) != "" {
		return w.summariseFromPatterns(ctx, slug, naradaContext, patterns)
	}
	return w.summariseFromRawJournal(ctx, slug, naradaContext)
}

func (w *Worker) summariseFromPatterns(ctx context.Context, slug, naradaContext string, patterns content.PersonaPatterns) string {
	res, err := w.llm.Complete(ctx, llm.CompleteRequest{
		Model:  llm.ModelHaiku45,
		System: "Jesteś assistentem, który wybiera 2-4 najbardziej trafne wzorce z dziennika persony pod kątem konkretnej narady. Pisz do tej persony w drugiej osobie, konkretnie, po polsku. Zachowaj oryginalne sformułowania — nie parafrazuj wzorców, tylko wskaż te, które pasują do sytuacji, i połącz je krótkim komentarzem 1-2 zdaniami.",
		User: fmt.Sprintf("Kontekst nowej narady:\n%s\n\nTwoje utrwalone wzorce (synteza z %d wpisów, %s):\n\n%s\n\nWskaż 2-4 z nich, które są najbardziej relewantne dla tej narady, i zwięźle dopowiedz, na co masz uważać.",
			naradaContext, patterns.EntriesAtSynthesis, patterns.SynthesisedAt.Format("2006-01-02"), patterns.Patterns),
		MaxTokens: 400,
	})
	if err != nil {
		log.Printf("[narada] haiku pattern selection failed for %s: %v", slug, err)
		return ""
	}
	return strings.TrimSpace(res.Text)
}

func (w *Worker) summariseFromRawJournal(ctx context.Context, slug, naradaContext string) string {
	entries, err := w.journalStore.List(slug)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Twój dziennik pomyłek (najnowsze na górze, max 10):\n\n")
	limit := len(entries)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		e := entries[i]
		fmt.Fprintf(&b, "- %s (%s): rekomendowałaś/eś \"%s\"; poszło źle: %s; wniosek: %s\n",
			e.At.Format("2006-01-02"), e.NaradaID, oneLine(e.Recommendation), oneLine(e.ErrorSignal), oneLine(e.Reflection))
	}
	journal := b.String()

	res, err := w.llm.Complete(ctx, llm.CompleteRequest{
		Model:  llm.ModelHaiku45,
		System: "Jesteś assistentem, który streszcza dziennik pomyłek jednej persony do 3-5 zdań, żeby pomóc jej wypowiedzieć się mądrzej na naradzie. Pisz do tej persony w drugiej osobie, konkretnie, po polsku. Podkreślaj wzorce błędów, nie pojedyncze incydenty. Nie wymyślaj wniosków spoza dziennika.",
		User: fmt.Sprintf("Kontekst nowej narady:\n%s\n\n%s\nPodsumuj 3-5 zdaniami, na co masz uważać przy tej naradzie.",
			naradaContext, journal),
		MaxTokens: 400,
	})
	if err != nil {
		log.Printf("[narada] haiku summary failed for %s: %v", slug, err)
		return ""
	}
	return strings.TrimSpace(res.Text)
}

// patternSynthesisLoop scans persona journals every 6h and synthesises those
// that crossed the entries-since-last-synthesis threshold. The narada Haiku
// recap prefers the pattern file over the raw journal, so keeping patterns
// fresh directly improves persona voice quality without touching the hot
// narada path.
func (w *Worker) patternSynthesisLoop() {
	// Small delay at startup so the very first tick doesn't collide with
	// naradaWorkerLoop hammering the LLM at boot.
	time.Sleep(30 * time.Second)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	w.runOneSynthesisPass()
	for range ticker.C {
		w.runOneSynthesisPass()
	}
}

func (w *Worker) runOneSynthesisPass() {
	if !w.llm.Available() {
		return
	}
	slugs, err := w.journalStore.ListSlugsWithJournal()
	if err != nil {
		log.Printf("[patterns] list slugs: %v", err)
		return
	}
	for _, slug := range slugs {
		need, newCount, err := w.journalStore.NeedsSynthesis(slug)
		if err != nil {
			log.Printf("[patterns] needs-check %s: %v", slug, err)
			continue
		}
		if !need {
			continue
		}
		log.Printf("[patterns] scheduling synthesis for %s (%d new entries)", slug, newCount)
		if _, _, err := w.SynthesisePersonaPatternsBySlug(slug); err != nil {
			log.Printf("[patterns] synthesis %s: %v", slug, err)
		}
	}
}

// ── Prompt + reply helpers ──────────────────────────────────────────────────

func buildSystemPrompt(personaBody, journalSummary string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(personaBody))
	if journalSummary != "" {
		b.WriteString("\n\n---\n\nRekapitulacja twojego dziennika przed tą naradą:\n\n")
		b.WriteString(journalSummary)
	}
	return b.String()
}

func buildUserPrompt(naradaContext string) string {
	return fmt.Sprintf(`Kontekst narady:

%s

Zabierz głos w tej sprawie. Odpowiadaj w SWOIM głosie — z charakterystycznym rytmem, słownictwem, długością zdań opisanymi w twoim promptcie. Nie streszczaj kontekstu, tylko wyciągnij rekomendację. 4-8 zdań. Konkret, nie ogólniki.`, naradaContext)
}

func buildReflectionSystemPrompt(personaBody string, store *content.PersonaJournalStore, slug string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(personaBody))
	entries, _ := store.List(slug)
	if len(entries) > 0 {
		b.WriteString("\n\n---\n\nTwoje wcześniejsze wpisy do dziennika pomyłek (najnowsze pierwsze, max 10):\n\n")
		limit := len(entries)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			e := entries[i]
			fmt.Fprintf(&b, "- %s (%s): %s\n", e.At.Format("2006-01-02"), e.NaradaID, oneLine(e.Reflection))
		}
	}
	return b.String()
}

func buildReflectionUserPrompt(naradaContext, recommendation, errorContext string) string {
	return fmt.Sprintf(`Na naradzie o kontekście:

%s

Rekomendowałaś/eś:

%s

Poszło źle. Sygnał błędu:

%s

Napisz wniosek dla siebie do własnego dziennika. Pisz do siebie w pierwszej osobie, w SWOIM głosie z systemowego promptu. 3-6 zdań, konkretnie, bez ogólników. Skup się na wzorcu, który wychwyciłaś/eś — coś, co ma zmienić twoją rekomendację przy podobnym kontekście w przyszłości. Nie tłumacz się i nie usprawiedliwiaj — nazwij pomyłkę i wyciągnij lekcję.`,
		naradaContext, recommendation, errorContext)
}

func stubVoice(slug, naradaContext, reason string) content.PersonaVoice {
	snippet := naradaContext
	if len(snippet) > 120 {
		snippet = snippet[:120] + "…"
	}
	return content.PersonaVoice{
		Slug:           slug,
		Recommendation: fmt.Sprintf("[stub — %s]\n\n%s looked at: %s", reason, slug, snippet),
		ModelUsed:      "stub",
	}
}

// modelForPersona maps the persona's Model frontmatter to a concrete
// Anthropic model id. Empty / unknown values fall back to Sonnet.
func modelForPersona(p personas.Persona) string {
	switch strings.ToLower(strings.TrimSpace(p.Model)) {
	case "haiku", "haiku-4-5", "haiku4-5":
		return llm.ModelHaiku45
	case "sonnet", "sonnet-4-6", "sonnet4-6", "":
		return llm.ModelSonnet46
	default:
		return llm.ModelSonnet46
	}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// ── Shared reply renderers used by both v1 tool dispatch and v2 SDK ─────────

// RenderNaradaStarted formats the human-facing "Narada started" reply that
// both v1 and v2 return from run_narada. Kept here so a tweak to the copy
// lands in exactly one place.
func RenderNaradaStarted(personaList []string, job content.RitualJob) string {
	return fmt.Sprintf(`Narada started. %d personas selected: %s

ID: %s
Created: %s

Poll fetch_narada_result(id=%q) — worker generates voices via Sonnet 4.6
(per persona: full persona body + Haiku 4.5 recap of their journal, if any).
Typical wall-time: 30-90s for 3-5 personas in parallel.

COMMIT TAG: when you implement any persona's recommendation and later commit
the code, include %q in the commit-message subject or body. /dobranoc uses
that tag to match rollbacks back to the recommending persona.`,
		len(personaList), strings.Join(personaList, ", "),
		job.ID, job.CreatedAt.Format("2006-01-02 15:04 UTC"), job.ID,
		"[narada:"+job.ID+"]")
}

// RenderNaradaResult formats the fetch_narada_result reply.
func RenderNaradaResult(job content.RitualJob) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Narada %s — status: %s\n\n", job.ID, job.Status)
	fmt.Fprintf(&sb, "Personas: %s\n", strings.Join(job.Personas, ", "))
	fmt.Fprintf(&sb, "Created:  %s\n", job.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	if !job.StartedAt.IsZero() {
		fmt.Fprintf(&sb, "Started:  %s\n", job.StartedAt.Format("2006-01-02 15:04:05 UTC"))
	}
	if !job.CompletedAt.IsZero() {
		fmt.Fprintf(&sb, "Ended:    %s\n", job.CompletedAt.Format("2006-01-02 15:04:05 UTC"))
	}
	sb.WriteString("\n")

	switch job.Status {
	case content.RitualPending:
		sb.WriteString("Waiting for worker to pick up the job. Poll again in 5-30s.")
	case content.RitualRunning:
		sb.WriteString("Worker is generating voices. Poll again in 5-30s.")
	case content.RitualFailed:
		fmt.Fprintf(&sb, "Failed: %s", job.Error)
	case content.RitualDone:
		sb.WriteString("=== Voices ===\n\n")
		for _, v := range job.Voices {
			fmt.Fprintf(&sb, "--- %s ---\n", v.Slug)
			if v.ModelUsed != "" {
				fmt.Fprintf(&sb, "(model: %s)\n", v.ModelUsed)
			}
			sb.WriteString(v.Recommendation)
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "COMMIT TAG: when implementing any of these recommendations, add %q to your commit-message so /dobranoc can trace rollbacks back.",
			"[narada:"+job.ID+"]")
	}
	return sb.String()
}
