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
		Description: "Create an async narada job: server routes context to 3-5 personas via keyword manifest, then generates each voice via Sonnet 4.6 (Haiku 4.5 for journal recaps). Returns job ID for polling.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"context":{"type":"string"},"from":{"type":"string"}},"required":["context"]}`),
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
			Context string `json:"context"`
			From    string `json:"from"`
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
		job, personas, err := src.CreateNaradaJob(a.Context, a.From)
		if err != nil {
			return textResult(err.Error()), nil
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
		return textResult(reply), nil
	})
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
