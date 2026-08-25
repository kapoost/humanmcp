package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── run_narada ──────────────────────────────────────────────────────────────

func registerRunNarada(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "run_narada",
		Description: "Create an async narada job: server routes context to 3-5 personas via keyword manifest, then generates each voice via Sonnet 4.6 (Haiku 4.5 for journal recaps). Returns job ID for polling. Pass `personas` to pick the voices yourself and skip the router entirely — the router matches keywords only and cannot read a request to include or exclude someone written in the context.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"context":{"type":"string"},"from":{"type":"string"},"personas":{"type":"array","items":{"type":"string"},"description":"Optional. Explicit persona slugs (from list_personas) to consult. When present the keyword manifest is not used and these exact personas answer, in this order. Omit for automatic keyword routing."}},"required":["context"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ip := ""
		if req.Extra != nil {
			ip = src.ClientIPFromHeaders(req.Extra.Header)
		}
		if !src.CheckNaradaRateLimit(ip) {
			log.Printf("[AUDIT] run_narada RATE_LIMITED ip=%s", ip)
			return textResult("Too many naradas from this caller — limit is 5 per hour. Try again later."), nil
		}
		var a struct {
			Context  string   `json:"context"`
			From     string   `json:"from"`
			Personas []string `json:"personas"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Context = strings.TrimSpace(a.Context)
		if a.Context == "" {
			return nil, fmt.Errorf("context is required")
		}
		if len(a.Context) > 4000 {
			a.Context = a.Context[:4000]
		}
		if len(a.From) > 64 {
			a.From = a.From[:64]
		}
		job, personas, err := src.CreateNaradaJob(a.Context, a.From, a.Personas)
		if err != nil {
			return textResult(err.Error()), nil
		}
		// Name the selection mode. When the router chose, say so and point
		// at the override: an agent whose user asked for specific voices
		// and got keyword-matched ones otherwise has no way to know the
		// escape hatch exists, and re-running with a reworded context only
		// shuffles the same keyword hits.
		selection := "Personas were chosen by the keyword manifest (no `personas` argument given).\nIf these are the wrong voices, re-run with personas=[\"slug\",...] — the router\nmatches keywords only and does not read include/exclude requests in the context."
		if len(a.Personas) > 0 {
			selection = "Personas were taken from your `personas` argument — the keyword manifest was skipped."
		}
		reply := fmt.Sprintf(`Narada started. %d personas selected: %s

%s

ID: %s
Created: %s

Poll fetch_narada_result(id=%q) — worker generates voices via Sonnet 4.6
(per persona: full persona body + Haiku 4.5 recap of their journal, if any).
Typical wall-time: 30-90s for 3-5 personas in parallel.

COMMIT TAG: when you implement any persona's recommendation and later commit
the code, include %q in the commit-message subject or body. /dobranoc uses
that tag to match rollbacks back to the recommending persona.`,
			len(personas), strings.Join(personas, ", "), selection,
			job.ID, job.CreatedAt.Format("2006-01-02 15:04 UTC"), job.ID,
			"[narada:"+job.ID+"]")
		return textResult(reply), nil
	})
}

// ── prepare_narada (offline) ────────────────────────────────────────────────

// Session-gated on purpose: the pack contains full persona bodies for the
// whole panel, which get_persona hands out only to bootstrapped callers.
// An open prepare_narada would be a five-at-a-time bypass of that gate.
func registerPrepareNarada(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "prepare_narada",
		Description: "Offline narada: returns the panel plus each persona's ready-to-run SYSTEM/USER prompts so YOU run them as your own subagents, instead of the server generating voices. No LLM cost, no rate limit, nothing recorded — no narada ID and no journal feedback. Prefer this over run_narada when your subagents can read material the server cannot (a repository, local files) or when the server has no API key. Session-gated.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"context":{"type":"string"},"personas":{"type":"array","items":{"type":"string"},"description":"Optional. Explicit persona slugs (from list_personas). When present the keyword manifest is not used. Omit for automatic keyword routing."},` + sessionTokenSchemaProp + `},"required":["context"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !sessionActiveOrToken(src, req) {
			return textResult("prepare_narada requires an active session — it returns full persona prompts. Call bootstrap_session first, then pass the token as session_token."), nil
		}
		var a struct {
			Context  string   `json:"context"`
			Personas []string `json:"personas"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Context = strings.TrimSpace(a.Context)
		if a.Context == "" {
			return nil, fmt.Errorf("context is required")
		}
		if len(a.Context) > 4000 {
			a.Context = a.Context[:4000]
		}
		pack, err := src.BuildNaradaPack(a.Context, a.Personas)
		if err != nil {
			return textResult(err.Error()), nil
		}
		return textResult(renderNaradaPack(pack)), nil
	})
}

func renderNaradaPack(pack content.NaradaPack) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "NARADA PACK (offline) — %d personas. You run them; the server does not.\n\n", len(pack.Personas))

	if pack.Routed {
		sb.WriteString("Panel: chosen by the keyword manifest (no `personas` argument given). It\n")
		sb.WriteString("substring-matches and cannot read an exclusion — re-run with personas=[...]\n")
		sb.WriteString("if these are the wrong voices.\n\n")
	} else {
		sb.WriteString("Panel: taken from your `personas` argument.\n\n")
	}

	sb.WriteString("HOW TO RUN IT\n")
	sb.WriteString("  1. Spawn ONE subagent per persona, all in parallel. Give each the SYSTEM\n")
	sb.WriteString("     block below verbatim as its instructions and the USER block as its task.\n")
	sb.WriteString("  2. Do NOT merge personas into a single agent and do NOT answer in their\n")
	sb.WriteString("     names yourself. The value is in independent passes; one agent playing\n")
	sb.WriteString("     five parts produces one opinion wearing five hats.\n")
	sb.WriteString("  3. Let your subagents read what the server cannot — the repository, the\n")
	sb.WriteString("     failing test, the actual file. That is this mode's whole advantage over\n")
	sb.WriteString("     run_narada, where personas see only the context string.\n")
	sb.WriteString("  4. Present every voice. They are built to disagree; reconciling them into\n")
	sb.WriteString("     one consensus paragraph throws away what you paid for.\n\n")

	if len(pack.Missing) > 0 {
		fmt.Fprintf(&sb, "SHORT PANEL — %d persona(s) were selected but could not be loaded: %s.\nThey are missing from content/personas/. The rest of the panel is intact.\n\n",
			len(pack.Missing), strings.Join(pack.Missing, ", "))
	}

	sb.WriteString("NOT RECORDED — this narada exists only in your session. There is no narada\n")
	sb.WriteString("ID, so do not invent a [narada:<id>] commit tag for it, and the personas'\n")
	sb.WriteString("journals will not learn from what they say here. Use run_narada(context,\n")
	sb.WriteString("personas=[...]) when you want the recorded pipeline and the reflection loop.\n")

	for i, p := range pack.Personas {
		fmt.Fprintf(&sb, "\n%s\n=== %d/%d — %s", strings.Repeat("=", 70), i+1, len(pack.Personas), p.Slug)
		if p.Title != "" || p.Role != "" {
			fmt.Fprintf(&sb, " (%s — %s)", p.Title, p.Role)
		}
		sb.WriteString("\n")
		switch p.JournalSource {
		case "patterns":
			sb.WriteString("Journal: synthesised patterns included in SYSTEM, unfiltered.\n")
		case "journal":
			sb.WriteString("Journal: raw reflections included in SYSTEM (patterns not synthesised yet).\n")
		default:
			sb.WriteString("Journal: none — this persona has no recorded mistakes yet.\n")
		}
		fmt.Fprintf(&sb, "\n--- SYSTEM ---\n%s\n\n--- USER ---\n%s\n", p.System, p.User)
	}
	return sb.String()
}

// ── fetch_narada_result ─────────────────────────────────────────────────────

func registerFetchNaradaResult(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "fetch_narada_result",
		Description: "Poll a narada job by ID. Statuses: pending, running, done, failed. When done, returns all persona voices.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ip := ""
		if req.Extra != nil {
			ip = src.ClientIPFromHeaders(req.Extra.Header)
		}
		if !src.CheckNaradaFetchRateLimit(ip) {
			log.Printf("[AUDIT] fetch_narada_result RATE_LIMITED ip=%s", ip)
			return textResult("Too many polls from this caller — limit is 60 per hour. Try again later."), nil
		}
		var a struct {
			ID string `json:"id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.ID = strings.TrimSpace(a.ID)
		if a.ID == "" {
			return nil, fmt.Errorf("id is required")
		}
		job, err := src.RitualStore().Get(a.ID)
		if err != nil {
			return textResult("Narada not found: " + err.Error()), nil
		}
		return textResult(renderNaradaResult(job)), nil
	})
}

func renderNaradaResult(job content.RitualJob) string {
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

// ── get_persona_journal (owner-only) ────────────────────────────────────────

func registerGetPersonaJournal(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "get_persona_journal",
		Description: "Read a persona's raw reflection journal. Owner-only — the journal is a private feedback loop, not for external callers.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult("Unauthorized — get_persona_journal requires an owner token."), nil
		}
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Slug = strings.TrimSpace(a.Slug)
		if a.Slug == "" {
			return textResult("slug is required"), nil
		}
		raw, err := src.PersonaJournalStore().Render(a.Slug)
		if err != nil {
			return textResult("Could not read journal: " + err.Error()), nil
		}
		if raw == "" {
			return textResult(fmt.Sprintf("Persona %q has no journal yet — no reflected mistakes recorded.", a.Slug)), nil
		}
		return textResult(raw), nil
	})
}

// ── record_persona_reflection (owner-only) ──────────────────────────────────

func registerRecordPersonaReflection(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "record_persona_reflection",
		Description: "Ask a persona to write a lesson-for-self after a rolled-back narada recommendation. Appends to the persona's journal via Sonnet/Haiku. Owner-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"narada_id":{"type":"string"},"persona_slug":{"type":"string"},"error_context":{"type":"string"}},"required":["narada_id","persona_slug","error_context"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult("Unauthorized — record_persona_reflection requires an owner token."), nil
		}
		var a struct {
			NaradaID     string `json:"narada_id"`
			PersonaSlug  string `json:"persona_slug"`
			ErrorContext string `json:"error_context"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.NaradaID = strings.TrimSpace(a.NaradaID)
		a.PersonaSlug = strings.TrimSpace(strings.ToLower(a.PersonaSlug))
		a.ErrorContext = strings.TrimSpace(a.ErrorContext)
		if a.NaradaID == "" || a.PersonaSlug == "" || a.ErrorContext == "" {
			return textResult("narada_id, persona_slug and error_context are all required"), nil
		}
		if len(a.ErrorContext) > 1000 {
			a.ErrorContext = a.ErrorContext[:1000]
		}
		if !src.LLMAvailable() {
			return textResult("LLM unavailable (no API key). Reflection not written."), nil
		}
		reflection, err := src.WriteReflection(a.NaradaID, a.PersonaSlug, a.ErrorContext)
		if err != nil {
			return textResult(err.Error()), nil
		}
		return textResult(fmt.Sprintf("Reflection recorded for %s in narada %s.\n\n%s",
			a.PersonaSlug, a.NaradaID, reflection)), nil
	})
}

// ── synthesise_persona_patterns (owner-only) ────────────────────────────────

func registerSynthesisePersonaPatterns(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "synthesise_persona_patterns",
		Description: "Force-run Sonnet synthesis over a persona's raw journal, replacing the previous pattern set. Owner-only — paid + writes internal state.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult("Unauthorized — synthesise_persona_patterns requires an owner token."), nil
		}
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Slug = strings.TrimSpace(strings.ToLower(a.Slug))
		if a.Slug == "" {
			return textResult("slug is required"), nil
		}
		if !src.LLMAvailable() {
			return textResult("LLM unavailable (no API key). Synthesis skipped."), nil
		}
		pat, count, err := src.SynthesisePersonaPatternsBySlug(a.Slug)
		if err != nil {
			return textResult("Synthesis failed: " + err.Error()), nil
		}
		if count == 0 {
			return textResult(fmt.Sprintf("Persona %q has no journal entries yet — nothing to synthesise.", a.Slug)), nil
		}
		return textResult(fmt.Sprintf("Synthesised %d entries into new pattern set for %s (model: %s).\n\n%s",
			count, a.Slug, pat.Model, pat.Patterns)), nil
	})
}
