// Package v2 is the MCP 2026-07-28 handler built on the official go-sdk.
// It runs alongside the legacy internal/mcp Handler during the migration
// window: /mcp keeps 2024-11-05 semantics, /mcp/v2 speaks stateless
// Streamable HTTP with the new spec.
//
// v2 reuses state (personas, skills, config) from the legacy handler via
// exported accessors so a single Handler instance still owns loading and
// caching. Tool bodies are duplicated on purpose — parity with v1 is
// asserted by storyboards, not by shared render helpers.
package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// Source is what v2 needs from the legacy handler. Kept narrow so v2 stays
// decoupled from wire-layer methods that are being phased out.
type Source interface {
	LoadPersonas() []mcp.Persona
	LoadSkills() []mcp.Skill
}

// New wires an SDK server + StreamableHTTPHandler for path-based mounting.
// Stateless: true is mandatory for 2026-07-28 per the SDK v1.7.0 release notes.
func New(cfg *config.Config, src Source) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "humanMCP — kapoost",
		Version: "0.2.0-dev",
	}, nil)

	registerAboutHumanmcp(server, cfg)
	registerListPersonas(server, src)
	registerListSkills(server, src)

	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return server
	}, &sdk.StreamableHTTPOptions{Stateless: true})
}

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: text}},
	}
}

// ── about_humanmcp ───────────────────────────────────────────────────────────

func registerAboutHumanmcp(s *sdk.Server, cfg *config.Config) {
	s.AddTool(&sdk.Tool{
		Name:        "about_humanmcp",
		Description: "Self-description of this humanMCP server. Deterministic, no bootstrap required — call this first to decide whether the rest of the API is relevant.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult(renderAbout(cfg)), nil
	})
}

func renderAbout(cfg *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "humanMCP — personal MCP server for %s\n\n", cfg.AuthorName)
	if cfg.AuthorBio != "" {
		fmt.Fprintf(&b, "%s\n\n", cfg.AuthorBio)
	}
	fmt.Fprintf(&b, "MCP endpoint: https://%s/mcp\n", cfg.Domain)
	fmt.Fprintf(&b, "Web home:     https://%s/\n", cfg.Domain)
	fmt.Fprintf(&b, "For agents:   https://%s/for-agents\n", cfg.Domain)
	fmt.Fprintf(&b, "Connect:      https://%s/connect\n", cfg.Domain)
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
	return b.String()
}

// ── list_personas ────────────────────────────────────────────────────────────

func registerListPersonas(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_personas",
		Description: "List every persona (slug, title, role). Full prompts unlock after bootstrap_session.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult(renderListPersonas(src.LoadPersonas())), nil
	})
}

func renderListPersonas(personas []mcp.Persona) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TEAM — %d personas available:\n\n", len(personas)))
	for _, p := range personas {
		sb.WriteString(fmt.Sprintf("  %-20s %s — %s\n", p.Slug, p.Title, p.Role))
	}
	sb.WriteString("\nFull prompts available after bootstrap_session (ask user for session code).")
	return sb.String()
}

// ── list_skills ──────────────────────────────────────────────────────────────

func registerListSkills(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_skills",
		Description: "List skills (slug, category, title, tags). Optional category / tag filters.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"category":{"type":"string"},"tag":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Category string `json:"category"`
			Tag      string `json:"tag"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		return textResult(renderListSkills(src.LoadSkills(), a.Category, a.Tag)), nil
	})
}

func renderListSkills(skills []mcp.Skill, category, tag string) string {
	match := func(s mcp.Skill) bool {
		if category != "" && !strings.EqualFold(s.Category, category) {
			return false
		}
		if tag != "" && !skillHasTag(s, tag) {
			return false
		}
		return true
	}
	count := 0
	for _, s := range skills {
		if match(s) {
			count++
		}
	}
	var sb strings.Builder
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
	return sb.String()
}

func skillHasTag(s mcp.Skill, tag string) bool {
	for _, t := range s.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}
