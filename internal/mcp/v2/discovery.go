package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
)

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
	fmt.Fprintln(&b, "Tool families (40 tools total — call tools/list for full schema):")
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
	fmt.Fprintln(&b, "  - vault:      mysloodsiewnia_status / _search / _get / _list — owner-only bridge")
	fmt.Fprintln(&b, "                into kapoost's home vault (SQLite FTS5, 9k+ docs). Gated by")
	fmt.Fprintln(&b, "                liveness heartbeat: `{status:\"offline\"}` (HTTP 200) when vault")
	fmt.Fprintln(&b, "                is unreachable — retry later, don't escalate. Full skill:")
	fmt.Fprintln(&b, "                get_skill(slug=\"mysloodsiewnia-bridge\").")
	return b.String()
}

// ── get_author_profile ──────────────────────────────────────────────────────

func registerGetAuthorProfile(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "get_author_profile",
		Description: "Author identity, bio, and a browsing cheatsheet. Records a profile-view event.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		src.StatStore().Record(content.Event{Type: content.EventProfile, Caller: content.CallerAgent})
		pieces := src.Store().List(false)
		publicCount, lockedCount := 0, 0
		for _, p := range pieces {
			if p.Access == content.AccessPublic {
				publicCount++
			} else {
				lockedCount++
			}
		}
		return textResult(renderAuthorProfile(src.Config(), publicCount, lockedCount)), nil
	})
}

func renderAuthorProfile(cfg *config.Config, publicCount, lockedCount int) string {
	return fmt.Sprintf(`AUTHOR: %s
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
`, cfg.AuthorName, cfg.AuthorName, cfg.Domain, publicCount, lockedCount, cfg.Domain)
}

// ── list_content ────────────────────────────────────────────────────────────

func registerListContent(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_content",
		Description: "List published pieces (slug, title, type, access, tags). Optional type/tag filters.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"tag":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Type string `json:"type"`
			Tag  string `json:"tag"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		src.StatStore().Record(content.Event{Type: content.EventList, Caller: content.CallerAgent})
		return textResult(renderListContent(src.Store().List(false), a.Type, a.Tag)), nil
	})
}

func renderListContent(pieces []*content.Piece, typeFilter, tagFilter string) string {
	var filtered []*content.Piece
	for _, p := range pieces {
		if typeFilter != "" && p.Type != typeFilter {
			continue
		}
		if tagFilter != "" && !contentHasTag(p.Tags, tagFilter) {
			continue
		}
		filtered = append(filtered, p)
	}
	if len(filtered) == 0 {
		return "No content found matching your filter."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "kapoost — %d piece(s):\n\n", len(filtered))
	for _, p := range filtered {
		fmt.Fprintf(&sb, "slug:   %s\n", p.Slug)
		fmt.Fprintf(&sb, "title:  %s\n", p.Title)
		fmt.Fprintf(&sb, "type:   %s\n", p.Type)
		fmt.Fprintf(&sb, "access: %s\n", p.Access)
		if p.Description != "" {
			fmt.Fprintf(&sb, "about:  %s\n", p.Description)
		}
		if len(p.Tags) > 0 {
			fmt.Fprintf(&sb, "tags:   %s\n", strings.Join(p.Tags, ", "))
		}
		fmt.Fprintf(&sb, "date:   %s\n", p.Published.Format("2 January 2006"))
		sb.WriteString("\n")
	}
	sb.WriteString("— read_content <slug> for public pieces\n")
	sb.WriteString("— request_access <slug> for locked pieces\n")
	return sb.String()
}

func contentHasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
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
