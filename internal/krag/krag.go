// Package krag ("krąg" — a circle of friends) turns one line of poetry into a
// fixed, paste-ready bundle of persona prompts and skills.
//
// WHY THIS EXISTS: a friend should be able to borrow kapoost's team without
// registering an MCP server, editing a client config, or holding a token that
// looks like a password. One curl, one poem, markdown back.
//
// WHY THE POEM IS NOT FROM session.go: that pool of ~100 fragments ships in a
// public repository. A fixed fragment drawn from it would be a label, not a
// secret — ~6.6 bits, enumerable in minutes. Phrases here are the owner's own
// unpublished lines, and their strength is exactly that nobody has read them.
// The HTTP layer adds a per-IP rate limit on top; see internal/web/krag_http.go.
//
// Resolution reuses config.FriendTokens, so a krąg phrase IS a friend token:
// same expiry rule (ADR-0001 Z6 — no unbounded TTL), same revocation path
// (drop it from FRIEND_TOKENS_JSON), same audit slug.
package krag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"encoding/json"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/personas"
)

// Normalize folds a phrase to its comparison form. A friend types the line
// into a URL, so it arrives without Polish diacritics, possibly hyphenated,
// possibly capitalised. "W-Malinowym-Chrusniaku" and "w malinowym chruśniaku"
// must land on the same key.
func Normalize(phrase string) string {
	s := content.FoldDiacritics(strings.ToLower(strings.TrimSpace(phrase)))
	s = strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '+', '.', ',', ';', '!', '?', '"', '\'':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Skill mirrors the JSON written to content/skills/<slug>.json. Kept local so
// this package does not import internal/mcp (which would pull the whole MCP
// backend into the web layer).
type Skill struct {
	Slug     string   `json:"slug"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Tags     []string `json:"tags,omitempty"`
}

// Bundle is one resolved krąg: who it belongs to, what it grants, when it dies.
type Bundle struct {
	TokenID   string // friend slug — what gets logged, never shown to the friend
	Personas  []personas.Persona
	Skills    []Skill
	Missing   []string // scopes naming something absent from disk (owner's problem)
	ExpiresAt time.Time
}

// Resolve matches a phrase against the configured friend tokens and loads
// everything its scopes name.
//
// Failure is deliberately shapeless: unknown phrase, expired token, disabled
// feature and empty bundle all return (nil, false). The caller must render one
// identical response for all of them — the same discipline as
// mcp.AuthorizeRequestByHeaders, for the same reason (ADR-0001 W4/Z3).
func Resolve(cfg *config.Config, phrase string) (*Bundle, bool) {
	if cfg == nil || len(cfg.FriendTokens) == 0 {
		return nil, false
	}
	want := Normalize(phrase)
	if want == "" {
		return nil, false
	}
	now := time.Now()
	for slug, spec := range cfg.FriendTokens {
		if spec == nil || spec.Token == "" {
			continue
		}
		if Normalize(spec.Token) != want {
			continue
		}
		if spec.ExpiresAt.IsZero() || now.After(spec.ExpiresAt) {
			return nil, false
		}
		b := load(cfg.ContentDir, slug, spec)
		if len(b.Personas) == 0 && len(b.Skills) == 0 {
			// A phrase that resolves to nothing readable is treated as no
			// phrase at all — otherwise an empty 200 confirms the guess.
			return nil, false
		}
		return b, true
	}
	return nil, false
}

func load(contentDir, slug string, spec *config.FriendTokenSpec) *Bundle {
	b := &Bundle{TokenID: slug, ExpiresAt: spec.ExpiresAt}

	for _, ps := range config.ScopesWithPrefix(spec.Scopes, config.ScopePrefixPersona) {
		p, err := personas.Load(contentDir, ps)
		if err != nil || strings.TrimSpace(p.Body) == "" {
			b.Missing = append(b.Missing, config.ScopePrefixPersona+ps)
			continue
		}
		b.Personas = append(b.Personas, p)
	}

	skills := loadSkills(contentDir)
	for _, ss := range config.ScopesWithPrefix(spec.Scopes, config.ScopePrefixSkill) {
		s, found := skills[ss]
		if !found || strings.TrimSpace(s.Body) == "" {
			b.Missing = append(b.Missing, config.ScopePrefixSkill+ss)
			continue
		}
		b.Skills = append(b.Skills, s)
	}
	return b
}

func loadSkills(contentDir string) map[string]Skill {
	out := map[string]Skill{}
	dir := filepath.Join(contentDir, "skills")
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
		out[s.Slug] = s
	}
	return out
}

// Render writes the bundle as markdown meant to be pasted whole into any chat.
// It is deliberately self-contained: no links back that the reader must follow,
// no MCP vocabulary, nothing that only works inside one client.
func Render(b *Bundle, author, domain string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Zespół %s — paczka dla przyjaciela\n\n", author))
	sb.WriteString("Wklej całość na początek rozmowy z dowolnym asystentem.\n")
	sb.WriteString("Poniżej są prompty postaci i umiejętności — nie instrukcje dla Ciebie,\n")
	sb.WriteString("tylko materiał, z którego asystent ma korzystać.\n\n")
	sb.WriteString(fmt.Sprintf("Paczka traci ważność %s.\n", b.ExpiresAt.Format("2 January 2006")))
	sb.WriteString(fmt.Sprintf("Źródło: %s · pytania: ask_human na tym serwerze.\n\n", domain))
	sb.WriteString("---\n\n")

	if len(b.Personas) > 0 {
		sb.WriteString("## Postacie\n\n")
		for _, p := range b.Personas {
			title := p.Title
			if title == "" {
				title = p.Slug
			}
			sb.WriteString(fmt.Sprintf("### %s", title))
			if p.Role != "" {
				sb.WriteString(fmt.Sprintf(" — %s", p.Role))
			}
			sb.WriteString("\n\n")
			if len(p.Tags) > 0 {
				sb.WriteString(fmt.Sprintf("_%s_\n\n", strings.Join(p.Tags, ", ")))
			}
			sb.WriteString(strings.TrimSpace(p.Body))
			sb.WriteString("\n\n")
		}
	}

	if len(b.Skills) > 0 {
		sb.WriteString("## Umiejętności\n\n")
		sort.SliceStable(b.Skills, func(i, j int) bool {
			return b.Skills[i].Category < b.Skills[j].Category
		})
		for _, s := range b.Skills {
			title := s.Title
			if title == "" {
				title = s.Slug
			}
			sb.WriteString(fmt.Sprintf("### %s", title))
			if s.Category != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", s.Category))
			}
			sb.WriteString("\n\n")
			sb.WriteString(strings.TrimSpace(s.Body))
			sb.WriteString("\n\n")
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("Materiał pochodzi od %s i jest użyczony, nie oddany.\n", author))
	sb.WriteString("Nie publikuj go dalej bez pytania.\n")
	return sb.String()
}
