package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/llm"
)

// toolRunNarada creates an async ritual job. The heavy work (LLM calls per
// persona) happens in naradaWorkerLoop; this handler returns immediately
// with the job id so the caller can poll fetch_narada_result later.
func (h *Handler) toolRunNarada(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	ip := h.clientIP(r)
	if allowed, _ := h.naradaBucket.Allow(ip); !allowed {
		log.Printf("[AUDIT] run_narada RATE_LIMITED ip=%s", ip)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Too many naradas from this caller — limit is 5 per hour. Try again later."}}})
		return
	}
	var a struct {
		Context string `json:"context"`
		From    string `json:"from"`
	}
	json.Unmarshal(args, &a)
	a.Context = strings.TrimSpace(a.Context)
	if a.Context == "" {
		writeError(w, req.ID, -32602, "context is required")
		return
	}
	if len(a.Context) > 4000 {
		a.Context = a.Context[:4000]
	}
	if len(a.From) > 64 {
		a.From = a.From[:64]
	}

	manifest, err := content.LoadRitualManifest(h.cfg.ContentDir, "narada")
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not load narada manifest: " + err.Error()}}})
		return
	}
	personas := manifest.RoutePersonas(a.Context)
	if len(personas) == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Narada router returned no personas — check content/rituals/narada.json defaults."}}})
		return
	}

	job, err := h.ritualStore.Create("narada", a.Context, personas)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not create narada job: " + err.Error()}}})
		return
	}

	reply := fmt.Sprintf(`Narada started. %d personas selected: %s

ID: %s
Created: %s

Poll fetch_narada_result(id=%q) — worker generates voices via Sonnet 4.6
(per persona: full persona body + Haiku 4.5 recap of their journal, if any).
Typical wall-time: 30-90s for 3-5 personas in parallel.

COMMIT TAG: when you implement any persona's recommendation and later commit
the code, include %q in the commit-message subject or body. /dobranoc uses
that tag to match rollbacks back to the recommending persona.`,
		len(personas), strings.Join(personas, ", "),
		job.ID, job.CreatedAt.Format("2006-01-02 15:04 UTC"), job.ID,
		"[narada:"+job.ID+"]")

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: reply}}})
}

// toolFetchNaradaResult returns the current state of a narada job. Poll
// until Status is "done" or "failed".
func (h *Handler) toolFetchNaradaResult(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	ip := h.clientIP(r)
	if allowed, _ := h.naradaFetchBucket.Allow(ip); !allowed {
		log.Printf("[AUDIT] fetch_narada_result RATE_LIMITED ip=%s", ip)
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Too many polls from this caller — limit is 60 per hour. Try again later."}}})
		return
	}
	var a struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &a)
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		writeError(w, req.ID, -32602, "id is required")
		return
	}

	job, err := h.ritualStore.Get(a.ID)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Narada not found: " + err.Error()}}})
		return
	}

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

	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}})
}

// toolGetPersonaJournal returns the raw markdown journal for one persona.
// Owner-only — the journal is a private feedback loop, not something an
// external caller should mine.
func (h *Handler) toolGetPersonaJournal(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isOwnerRequest(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Unauthorized — get_persona_journal requires an owner token."}}})
		return
	}
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	a.Slug = strings.TrimSpace(a.Slug)
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}
	raw, err := h.journalStore.Render(a.Slug)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Could not read journal: " + err.Error()}}})
		return
	}
	if raw == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Persona %q has no journal yet — no reflected mistakes recorded.", a.Slug)}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text", Text: raw}}})
}

// toolRecordPersonaReflection asks a persona to write a lesson-for-self
// after one of its narada recommendations was rolled back. The reflection
// is appended to the persona's journal so future narady can incorporate it
// via the Haiku journal summary step.
func (h *Handler) toolRecordPersonaReflection(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isOwnerRequest(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Unauthorized — record_persona_reflection requires an owner token."}}})
		return
	}
	var a struct {
		NaradaID     string `json:"narada_id"`
		PersonaSlug  string `json:"persona_slug"`
		ErrorContext string `json:"error_context"`
	}
	json.Unmarshal(args, &a)
	a.NaradaID = strings.TrimSpace(a.NaradaID)
	a.PersonaSlug = strings.TrimSpace(strings.ToLower(a.PersonaSlug))
	a.ErrorContext = strings.TrimSpace(a.ErrorContext)
	if a.NaradaID == "" || a.PersonaSlug == "" || a.ErrorContext == "" {
		writeError(w, req.ID, -32602, "narada_id, persona_slug and error_context are all required")
		return
	}
	if len(a.ErrorContext) > 1000 {
		a.ErrorContext = a.ErrorContext[:1000]
	}

	job, err := h.ritualStore.Get(a.NaradaID)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Narada not found: " + err.Error()}}})
		return
	}
	var recommendation string
	for _, v := range job.Voices {
		if v.Slug == a.PersonaSlug {
			recommendation = v.Recommendation
			break
		}
	}
	if recommendation == "" {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Persona %q did not speak on narada %q — cannot reflect.", a.PersonaSlug, a.NaradaID)}}})
		return
	}

	persona, err := h.loadPersona(a.PersonaSlug)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Load persona %s: %s", a.PersonaSlug, err.Error())}}})
		return
	}

	if !h.llm.Available() {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "LLM unavailable (no API key). Reflection not written."}}})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	systemPrompt := buildReflectionSystemPrompt(persona.Body, h.journalStore, a.PersonaSlug)
	userPrompt := buildReflectionUserPrompt(job.Context, recommendation, a.ErrorContext)

	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
		Model:     modelForPersona(persona),
		System:    systemPrompt,
		User:      userPrompt,
		MaxTokens: 512,
	})
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "LLM error: " + err.Error()}}})
		return
	}
	entry := content.PersonaJournalEntry{
		At:             time.Now().UTC(),
		NaradaID:       a.NaradaID,
		Context:        oneLine(job.Context),
		Recommendation: oneLine(recommendation),
		ErrorSignal:    oneLine(a.ErrorContext),
		Reflection:     res.Text,
	}
	if err := h.journalStore.Append(a.PersonaSlug, entry); err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Journal append failed: " + err.Error()}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Reflection recorded for %s in narada %s.\n\n%s", a.PersonaSlug, a.NaradaID, res.Text)}}})
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

// toolSynthesisePersonaPatterns runs a forced Sonnet synthesis over the
// persona's raw journal, replacing the previous pattern set. Owner-only —
// synthesis is a paid operation and the resulting file is internal state,
// not something an external caller should be able to churn.
func (h *Handler) toolSynthesisePersonaPatterns(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	if !h.isOwnerRequest(r) {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Unauthorized — synthesise_persona_patterns requires an owner token."}}})
		return
	}
	var a struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(args, &a)
	a.Slug = strings.TrimSpace(strings.ToLower(a.Slug))
	if a.Slug == "" {
		writeError(w, req.ID, -32602, "slug is required")
		return
	}
	if !h.llm.Available() {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "LLM unavailable (no API key). Synthesis skipped."}}})
		return
	}
	pat, count, err := h.synthesisePersonaPatterns(a.Slug)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Synthesis failed: " + err.Error()}}})
		return
	}
	if count == 0 {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: fmt.Sprintf("Persona %q has no journal entries yet — nothing to synthesise.", a.Slug)}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Synthesised %d entries into new pattern set for %s (model: %s).\n\n%s",
			count, a.Slug, pat.Model, pat.Patterns)}}})
}

// synthesisePersonaPatterns runs the actual LLM call. Returns the new
// PersonaPatterns record on success; returns (zero, 0, nil) when the persona
// has no journal entries yet so the caller can distinguish "nothing to do"
// from "error".
func (h *Handler) synthesisePersonaPatterns(slug string) (content.PersonaPatterns, int, error) {
	entries, err := h.journalStore.List(slug)
	if err != nil {
		return content.PersonaPatterns{}, 0, err
	}
	if len(entries) == 0 {
		return content.PersonaPatterns{}, 0, nil
	}
	persona, err := h.loadPersona(slug)
	if err != nil {
		return content.PersonaPatterns{}, 0, err
	}
	previous, _ := h.journalStore.ReadPatterns(slug)

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

	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
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
	if err := h.journalStore.WritePatterns(slug, patterns); err != nil {
		return content.PersonaPatterns{}, len(entries), err
	}
	log.Printf("[patterns] synthesised %s: %d entries → %d chars of patterns", slug, len(entries), len(patterns.Patterns))
	return patterns, len(entries), nil
}

// patternSynthesisLoop scans persona journals every 6h and synthesises those
// that crossed the entries-since-last-synthesis threshold. The narada Haiku
// recap prefers the pattern file over the raw journal, so keeping patterns
// fresh directly improves persona voice quality without touching the hot
// narada path.
func (h *Handler) patternSynthesisLoop() {
	// Small delay at startup so the very first tick doesn't collide with
	// naradaWorkerLoop hammering the LLM at boot.
	time.Sleep(30 * time.Second)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	h.runOneSynthesisPass()
	for range ticker.C {
		h.runOneSynthesisPass()
	}
}

func (h *Handler) runOneSynthesisPass() {
	if !h.llm.Available() {
		return
	}
	slugs, err := h.journalStore.ListSlugsWithJournal()
	if err != nil {
		log.Printf("[patterns] list slugs: %v", err)
		return
	}
	for _, slug := range slugs {
		need, newCount, err := h.journalStore.NeedsSynthesis(slug)
		if err != nil {
			log.Printf("[patterns] needs-check %s: %v", slug, err)
			continue
		}
		if !need {
			continue
		}
		log.Printf("[patterns] scheduling synthesis for %s (%d new entries)", slug, newCount)
		if _, _, err := h.synthesisePersonaPatterns(slug); err != nil {
			log.Printf("[patterns] synthesis %s: %v", slug, err)
		}
	}
}

// naradaWorkerLoop drains the pending narada queue. Runs one job per tick to
// keep the worker predictable and easy to debug. Sprint 1: stub — every
// persona returns a placeholder. Sprint 2: swap the stub for a real LLM
// call keyed by persona system prompt.
func (h *Handler) naradaWorkerLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		pending, err := h.ritualStore.ListPending()
		if err != nil || len(pending) == 0 {
			continue
		}
		for _, job := range pending {
			h.processNaradaJob(job)
		}
	}
}

func (h *Handler) processNaradaJob(job content.RitualJob) {
	if err := h.ritualStore.MarkRunning(job.ID); err != nil {
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
			voice, err := h.generatePersonaVoice(slug, job.Context)
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
		_ = h.ritualStore.Fail(job.ID, "no voices generated")
		return
	}
	if err := h.ritualStore.Complete(job.ID, voices); err != nil {
		log.Printf("[narada-worker] complete %s: %v", job.ID, err)
	}
}

// generatePersonaVoice runs the two-model pipeline for one persona:
//   1. Load persona body from content/personas/<slug>.md.
//   2. If the persona has a journal, ask Haiku 4.5 to compress it into a
//      focused paragraph (3-5 sentences) framed by the current narada
//      context. Skipped when the journal is empty.
//   3. Ask Sonnet 4.6 to speak in the persona's voice, with the journal
//      summary appended to the system prompt so the persona "remembers".
//
// Graceful degradation: if the API key is missing or LLM errors, we fall
// back to a stub voice so the async plumbing keeps working in local dev
// and during outages.
func (h *Handler) generatePersonaVoice(slug, naradaContext string) (content.PersonaVoice, error) {
	persona, err := h.loadPersona(slug)
	if err != nil {
		return content.PersonaVoice{}, fmt.Errorf("load persona %s: %w", slug, err)
	}
	if !h.llm.Available() {
		return stubVoice(slug, naradaContext, "no api key configured"), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	journalSummary := h.summariseJournal(ctx, slug, naradaContext)
	systemPrompt := buildSystemPrompt(persona.Body, journalSummary)
	userPrompt := buildUserPrompt(naradaContext)
	model := modelForPersona(persona)

	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
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
// personas whose pattern set is missing or stale (fewer than a full pattern
// cycle's worth of entries).
func (h *Handler) summariseJournal(ctx context.Context, slug, naradaContext string) string {
	patterns, _ := h.journalStore.ReadPatterns(slug)
	if strings.TrimSpace(patterns.Patterns) != "" {
		return h.summariseFromPatterns(ctx, slug, naradaContext, patterns)
	}
	return h.summariseFromRawJournal(ctx, slug, naradaContext)
}

// summariseFromPatterns builds the narada recap from the compressed patterns
// file. Patterns are already durable and context-agnostic, so Haiku's job
// here is just to select which patterns are most relevant to the current
// narada context (rather than distilling them from scratch each time).
func (h *Handler) summariseFromPatterns(ctx context.Context, slug, naradaContext string, patterns content.PersonaPatterns) string {
	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
		Model: llm.ModelHaiku45,
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

// summariseFromRawJournal is the pre-patterns Sprint-2 path. Kept for
// personas whose journal is small enough that the periodic synthesis worker
// hasn't run yet — under the synthesis threshold, raw entries are still the
// best signal.
func (h *Handler) summariseFromRawJournal(ctx context.Context, slug, naradaContext string) string {
	entries, err := h.journalStore.List(slug)
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

	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
		Model: llm.ModelHaiku45,
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

// loadPersona reads and parses a persona file into the full Persona struct,
// giving callers access to the body plus frontmatter fields (Model, Tags, etc.).
func (h *Handler) loadPersona(slug string) (Persona, error) {
	path := filepath.Join(h.cfg.ContentDir, "personas", slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Persona{}, err
	}
	p := parsePersonaFile(string(data), slug)
	return p, nil
}

// modelForPersona maps the persona's Model frontmatter to a concrete Anthropic
// model id. Empty / unknown values fall back to Sonnet — safer default when
// unsure, and matches Sprint 2 baseline behaviour.
func modelForPersona(p Persona) string {
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

// ── Business methods exposed for the v2 SDK handler ──────────────────────────
// These wrap the private tool logic so v2 can trigger the same rituals
// without owning the ritualStore construction or the LLM plumbing.

// CreateNaradaJob routes context to the configured personas and creates a
// pending RitualJob picked up by naradaWorkerLoop. Returns the job plus the
// selected personas list so v2 can format the same "Narada started" reply.
func (h *Handler) CreateNaradaJob(ctxText, from string) (content.RitualJob, []string, error) {
	manifest, err := content.LoadRitualManifest(h.cfg.ContentDir, "narada")
	if err != nil {
		return content.RitualJob{}, nil, fmt.Errorf("Could not load narada manifest: %w", err)
	}
	personas := manifest.RoutePersonas(ctxText)
	if len(personas) == 0 {
		return content.RitualJob{}, nil, fmt.Errorf("Narada router returned no personas — check content/rituals/narada.json defaults.")
	}
	job, err := h.ritualStore.Create("narada", ctxText, personas)
	if err != nil {
		return content.RitualJob{}, nil, fmt.Errorf("Could not create narada job: %w", err)
	}
	return job, personas, nil
}

// WriteReflection runs the LLM reflection pipeline on a persona voice from
// a completed narada, appends the result to the persona journal, and
// returns the reflection text so v2 can include it in the reply.
func (h *Handler) WriteReflection(naradaID, personaSlug, errorContext string) (string, error) {
	job, err := h.ritualStore.Get(naradaID)
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
	persona, err := h.loadPersona(personaSlug)
	if err != nil {
		return "", fmt.Errorf("load persona %s: %w", personaSlug, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	systemPrompt := buildReflectionSystemPrompt(persona.Body, h.journalStore, personaSlug)
	userPrompt := buildReflectionUserPrompt(job.Context, recommendation, errorContext)
	res, err := h.llm.Complete(ctx, llm.CompleteRequest{
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
	if err := h.journalStore.Append(personaSlug, entry); err != nil {
		return "", fmt.Errorf("journal append failed: %w", err)
	}
	return res.Text, nil
}

// SynthesisePersonaPatternsBySlug is the public wrapper for the private
// synthesisePersonaPatterns method — v2 owner-only tool triggers this.
func (h *Handler) SynthesisePersonaPatternsBySlug(slug string) (content.PersonaPatterns, int, error) {
	return h.synthesisePersonaPatterns(slug)
}
