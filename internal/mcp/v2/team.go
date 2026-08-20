package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// ── bootstrap_session ───────────────────────────────────────────────────────

func registerBootstrapSession(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "bootstrap_session",
		Description: "Validate the Polish poetry session code, then emit the full team briefing (guardian + style + personas + skills + persist block). Rate-limited 5/min/IP.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var params struct {
			Code string `json:"code"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		if params.Code == "" {
			return textResult("Podaj kod sesji. Zapytaj użytkownika o fragment wiersza polskiego poety."), nil
		}
		ip := ""
		if req.Extra != nil {
			ip = src.ClientIPFromHeaders(req.Extra.Header)
		}
		if !src.CheckBootstrapRateLimit(ip) {
			log.Printf("[AUDIT] bootstrap_session RATE_LIMITED ip=%s", ip)
			return textResult("Zbyt wiele prób. Poczekaj minutę."), nil
		}
		if !src.ValidateSessionCode(params.Code) {
			log.Printf("[AUDIT] bootstrap_session FAIL ip=%s", ip)
			src.StatStore().Record(content.Event{Type: content.EventAccess, Caller: content.CallerAgent})
			return textResult("Niepoprawne hasło sesji. Sprawdź — to powinien być fragment wiersza polskiego poety. Wielkość liter nie ma znaczenia."), nil
		}
		log.Printf("[AUDIT] bootstrap_session OK ip=%s", ip)
		// v2 session activation: stateless HMAC token embedded in the
		// briefing. Agents extract via `SESSION_TOKEN: <token>` line at
		// the top and send on subsequent calls as
		//   Authorization: Bearer <token>
		// SDK strips Mcp-Session-Id on stateless transport (2026-07-28
		// protocol), so this Bearer channel is the way v2 gates
		// members-tier content (see IsSessionActiveByHeaders dual-read).
		// TTL 1h — see internal/mcp/session.go.
		body := renderBootstrapBriefing(src.LoadPersonas(), src.LoadSkills(), src.Config().Domain)
		if token := mcp.GenerateSessionToken(src.Config().SessionSecret); token != "" {
			preamble := "SESSION_TOKEN: " + token + "\n" +
				"(Send on subsequent tool calls as `Authorization: Bearer <token>` — TTL 1h.\n" +
				" Members-tier content (list_collection, read_collection_item) is gated on this.)\n\n"
			return textResult(preamble + body), nil
		}
		return textResult(body), nil
	})
}

func renderBootstrapBriefing(personas []mcp.Persona, skills []mcp.Skill, domain string) string {
	var sb strings.Builder
	sb.WriteString("SESSION ACTIVE — full access granted.\n\n")

	sb.WriteString("=== GUARDIAN — LOAD FIRST ===\n\n")
	sb.WriteString("Before any other work, internalize these rules. They apply to the entire session.\n")
	sb.WriteString("Default-deny: secret-printing operations and destructive commands.\n\n")
	for _, p := range personas {
		if p.Slug == "hodor" {
			fmt.Fprintf(&sb, "## %s — %s\n%s\n\n", p.Title, p.Role, p.Body)
		}
	}
	for _, s := range skills {
		if strings.HasPrefix(s.Slug, "operational-safety-") {
			fmt.Fprintf(&sb, "## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body)
		}
	}
	sb.WriteString("=== END GUARDIAN ===\n\n")

	sb.WriteString("=== STYLE — APPLY TO EVERY POLISH RESPONSE ===\n\n")
	sb.WriteString("Communication-style rules. Apply whenever you write to kapoost in Polish.\n\n")
	for _, s := range skills {
		if s.Slug == "language-style-polish" {
			fmt.Fprintf(&sb, "## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body)
		}
	}
	sb.WriteString("=== END STYLE ===\n\n")

	isGuardianPersona := func(slug string) bool { return slug == "hodor" }
	isGuardianSkill := func(slug string) bool { return strings.HasPrefix(slug, "operational-safety-") }
	isStyleSkill := func(slug string) bool { return slug == "language-style-polish" }

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

	fmt.Fprintf(&sb, "TEAM ROSTER — %d personas:\n\n", nonGuardianPersonas)
	for _, p := range personas {
		if isGuardianPersona(p.Slug) {
			continue
		}
		fmt.Fprintf(&sb, "## %s — %s\n", p.Title, p.Role)
		fmt.Fprintf(&sb, "Tags: %s\n", strings.Join(p.Tags, ", "))
		fmt.Fprintf(&sb, "Prompt:\n%s\n\n", p.Body)
	}

	if nonGuardianSkills > 0 {
		fmt.Fprintf(&sb, "\n---\nSKILLS — %d instructions:\n\n", nonGuardianSkills)
		for _, s := range skills {
			if isGuardianSkill(s.Slug) || isStyleSkill(s.Slug) {
				continue
			}
			fmt.Fprintf(&sb, "## %s [%s]\n%s\n\n", s.Title, s.Category, s.Body)
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

	sb.WriteString("=== PERSIST FOR NEXT SESSION ===\n\n")
	sb.WriteString("You are (probably) a fresh agent. To keep this context on your next boot, save the following into your durable memory system — whichever your runtime provides:\n")
	sb.WriteString("  - Claude Code: auto-memory files under `~/.claude/projects/<slug>/memory/` (write a reference-type entry with the endpoint + trigger table).\n")
	sb.WriteString("  - Claude Agent SDK: your configured memory backend.\n")
	sb.WriteString("  - Any MCP-capable client: this server exposes `remember(code, body, tags)` and `recall(code, query)` — the code is the same Polish-poetry fragment you used to bootstrap. Notes persist across sessions and callers who share the code.\n\n")
	sb.WriteString("MINIMUM to save so next-session-you can pick up without asking kapoost:\n")
	fmt.Fprintf(&sb, "  1. Endpoint: https://%s/mcp\n", domain)
	sb.WriteString("  2. On every new session, call `bootstrap_session(code=<fragment>)` FIRST — auto-loads Hodor (safety) + language-style-polish. If the code has rotated, ask kapoost for a fresh one (a short Polish poetry fragment).\n")
	sb.WriteString("  3. Trigger words to watch for in kapoost's messages:\n")
	sb.WriteString("     - 'narada' / '/narada <context>' → call MCP `run_narada(context)`; poll `fetch_narada_result(id)` for the composed voices.\n")
	sb.WriteString("     - 'dobranoc' / '/dobranoc' → fetch skill `dobranoc-dziennik` via `get_skill` and follow it.\n")
	sb.WriteString("     - Polish-language reply context → language-style-polish already auto-loaded via bootstrap; keep applying it.\n")
	sb.WriteString("     - Secrets, tokens, destructive commands → Hodor is the guardian; default-deny and confirm with kapoost first.\n")
	sb.WriteString("     - 'przeszukaj vault' / 'w mysłoodsiewni' → `mysloodsiewnia_search / _list / _get / _status` — Authorization: Bearer <token> required. Owner token = unlimited scope; named friend tokens = read-only, scoped to specific `doc_type`s, per-token 1h rate limit, expirable. Response envelopes: `{status:online, result:...}`, `{status:offline}` (stable state — do not retry), `{status:out_of_scope, allowed:[...]}` (friend-token doc_type out of scope), `{status:rate_limited, retry_after:N}` (backoff N seconds). Unknown/revoked/expired friend tokens are indistinguishable from anonymous — same `Unauthorized` text, nothing about the friend-token set leaks.\n")
	sb.WriteString("     - 'zapisz do vaulta' / 'ingest' / 'dodaj notatkę do mysłoodsiewni' → `mysloodsiewnia_write` (wave 2). OWNER-ONLY — friend tokens receive `{status:write_denied,reason:owner_only}`. Args: `{doc_type, title, body, source_path?, meta?}`; body ≤ 100 KiB (over-cap ⇒ `{status:payload_too_large,limit,got}`). Success envelope: `{status:online, op_id, result:{slug, created_at}}`. Auto-tagged server-side `via:humanmcp-bridge`; delete is permanently unavailable. Idempotent on `op_id` — safe to retry. Auth: reuse the SESSION_TOKEN emitted at the top of THIS very briefing (Bearer channel); if that fails on write specifically, fall back to owner EDIT_TOKEN from kapoost's Keychain (`security find-generic-password -s humanmcp-edit-token -w`).\n")
	sb.WriteString("  4. For any other topic that hits a domain skill (e.g. 'go stack', 'deploy', 'shell', 'mx5', 's2000', 'onaudience', 'bookkido'), call `list_skills` to find the slug, then `get_skill(slug=<x>)` for the body — do NOT reinvent from memory alone.\n")
	sb.WriteString("  4a. GROUP LOAD — when the user says 'załaduj skille z projektu X' / 'load project X skills' / 'load skill group X', call `load_skill_group(name=<X>)` (single MCP call, returns all bodies). Discover available groups via `list_skill_groups`. For a fresh workspace ('scaffold', 'skonfiguruj to repo') use `suggest_skills(files=[...], languages=[...], git_origin=...)` — deterministic, capped at 8 slugs + 5 personas (derived from matched groups, hodor always seated).\n")
	sb.WriteString("  5. If your platform supports it, also stash a durable pointer to this server's `about_humanmcp` tool — future-you can re-orient in one call without a full bootstrap.\n")
	sb.WriteString("=== END PERSIST ===\n")

	return sb.String()
}

// ── get_persona ─────────────────────────────────────────────────────────────

func registerGetPersona(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "get_persona",
		Description: "Return one persona's full prompt by slug. Session-gated except for Hodor (guardian rules must always apply).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var params struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		if params.Slug == "" {
			return textResult("Podaj slug persony."), nil
		}
		authenticated := sessionActive(src, req)
		for _, p := range src.LoadPersonas() {
			if p.Slug == params.Slug {
				if authenticated || p.Slug == "hodor" {
					return textResult(fmt.Sprintf("%s — %s\nTags: %s\n\n%s",
						p.Title, p.Role, strings.Join(p.Tags, ", "), p.Body)), nil
				}
				return textResult(fmt.Sprintf("%s — %s\nTags: %s\n\nFull prompt available after bootstrap_session. Ask user for session code.",
					p.Title, p.Role, strings.Join(p.Tags, ", "))), nil
			}
		}
		return textResult(fmt.Sprintf("Persona '%s' nie znaleziona. Użyj list_personas.", params.Slug)), nil
	})
}

// ── get_skill ───────────────────────────────────────────────────────────────

func registerGetSkill(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "get_skill",
		Description: "Return one skill's full body by slug. Session-gated except for -public suffixed skills (guardian bypass).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var params struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		if params.Slug == "" {
			return textResult("Podaj slug skilla. Użyj list_skills."), nil
		}
		authenticated := sessionActive(src, req)
		for _, s := range src.LoadSkills() {
			if s.Slug == params.Slug {
				publiclyAccessible := strings.HasSuffix(s.Slug, "-public")
				if authenticated || publiclyAccessible {
					return textResult(fmt.Sprintf("%s\nCategory: %s\n\n%s", s.Title, s.Category, s.Body)), nil
				}
				return textResult(fmt.Sprintf("%s\nCategory: %s\n\nFull body available after bootstrap_session. Ask user for session code.", s.Title, s.Category)), nil
			}
		}
		return textResult(fmt.Sprintf("Skill '%s' nie znaleziony. Użyj list_skills.", params.Slug)), nil
	})
}
