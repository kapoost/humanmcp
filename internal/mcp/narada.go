package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/rituals"
)

// toolRunNarada creates an async ritual job. Handler is a thin dispatch
// shell — the pipeline lives in internal/rituals.
func (h *Handler) toolRunNarada(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	ip := h.clientIP(r)
	if !h.ritualWorker.CheckNaradaRateLimit(ip) {
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
	job, personas, err := h.ritualWorker.CreateNaradaJob(a.Context, a.From)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: err.Error()}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: rituals.RenderNaradaStarted(personas, job)}}})
}

// toolFetchNaradaResult returns the current state of a narada job.
func (h *Handler) toolFetchNaradaResult(w http.ResponseWriter, r *http.Request, req *Request, args json.RawMessage) {
	ip := h.clientIP(r)
	if !h.ritualWorker.CheckNaradaFetchRateLimit(ip) {
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
	job, err := h.ritualWorker.RitualStore().Get(a.ID)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "Narada not found: " + err.Error()}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: rituals.RenderNaradaResult(job)}}})
}

// toolGetPersonaJournal returns the raw markdown journal for one persona.
// Owner-only.
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
	raw, err := h.ritualWorker.PersonaJournalStore().Render(a.Slug)
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
// after one of its narada recommendations was rolled back. Owner-only.
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
	if !h.ritualWorker.LLMAvailable() {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "LLM unavailable (no API key). Reflection not written."}}})
		return
	}
	reflection, err := h.ritualWorker.WriteReflection(a.NaradaID, a.PersonaSlug, a.ErrorContext)
	if err != nil {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: err.Error()}}})
		return
	}
	writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
		Text: fmt.Sprintf("Reflection recorded for %s in narada %s.\n\n%s", a.PersonaSlug, a.NaradaID, reflection)}}})
}

// toolSynthesisePersonaPatterns runs a forced Sonnet synthesis over the
// persona's raw journal, replacing the previous pattern set. Owner-only.
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
	if !h.ritualWorker.LLMAvailable() {
		writeResult(w, req.ID, CallResult{Content: []ContentBlock{{Type: "text",
			Text: "LLM unavailable (no API key). Synthesis skipped."}}})
		return
	}
	pat, count, err := h.ritualWorker.SynthesisePersonaPatternsBySlug(a.Slug)
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

// ── v2 Source-interface passthroughs (delegate to the worker) ───────────────

// CreateNaradaJob delegates to the shared rituals.Worker. Retained on
// Handler for v2 Source-interface compatibility during the drop window.
func (h *Handler) CreateNaradaJob(ctxText, from string) (content.RitualJob, []string, error) {
	return h.ritualWorker.CreateNaradaJob(ctxText, from)
}

// WriteReflection delegates to the shared rituals.Worker.
func (h *Handler) WriteReflection(naradaID, personaSlug, errorContext string) (string, error) {
	return h.ritualWorker.WriteReflection(naradaID, personaSlug, errorContext)
}

// SynthesisePersonaPatternsBySlug delegates to the shared rituals.Worker.
func (h *Handler) SynthesisePersonaPatternsBySlug(slug string) (content.PersonaPatterns, int, error) {
	return h.ritualWorker.SynthesisePersonaPatternsBySlug(slug)
}
