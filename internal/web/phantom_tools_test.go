package web_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// TestNoPhantomToolsInAgentDocs scans every agent-facing surface for tool
// names — internal/web/templates/for-agents.html, docs/index.html, README.md,
// the bundled skills — and asserts each name is registered in the actual
// MCP handler's tool list.
//
// Born from the 2026-06-08 audit that found recall/remember/query_vault/
// about_humanmcp documented but unimplemented. The fix shipped about_humanmcp
// in the same commit; this test exists so the next agent reading the page
// can't be lied to.
func TestNoPhantomToolsInAgentDocs(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	implemented := implementedToolSet(t)
	mentions := scanAgentDocsForToolNames(t, repoRoot)

	// allowedPhantoms — tools intentionally documented as roadmap items
	// that aren't yet implemented. Empty by default. Each entry MUST have
	// a comment with the date and roadmap doc link.
	allowedPhantoms := map[string]string{
		// none — recall/remember landed alongside D1 (MemoryStore).
	}

	var phantoms []string
	for name, sources := range mentions {
		if implemented[name] {
			continue
		}
		if _, ok := allowedPhantoms[name]; ok {
			continue
		}
		phantoms = append(phantoms, fmt.Sprintf("  %-24s  mentioned in: %s", name, strings.Join(sources, ", ")))
	}
	if len(phantoms) > 0 {
		sort.Strings(phantoms)
		t.Errorf("found %d phantom tool(s) — documented but NOT in mcp.Handler.ToolNames():\n%s",
			len(phantoms), strings.Join(phantoms, "\n"))
	}
}

func implementedToolSet(t *testing.T) map[string]bool {
	t.Helper()
	cfg := &config.Config{
		AuthorName: "test",
		Domain:     "test.example",
		ContentDir: t.TempDir(),
	}
	h := mcp.NewHandler(cfg, nil, auth.New("test"))
	out := map[string]bool{}
	for _, n := range h.ToolNames() {
		out[n] = true
	}
	return out
}

// toolNameRE matches plausible tool-name tokens. MCP tools are lowercase
// snake_case (underscores allowed, no hyphens) and must be 4-32 chars to
// avoid grabbing common identifiers like "type" / "name".
var toolNameRE = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\b`)

// scanAgentDocsForToolNames returns a map from tool-name → list of files it
// appears in. Scope is intentionally narrow: only files we control and
// publish to agents/users as "what tools we offer". Skill bodies are scanned
// too because they get returned via list_skills / get_skill.
func scanAgentDocsForToolNames(t *testing.T, repoRoot string) map[string][]string {
	t.Helper()

	// Anchor: only candidates that appear inside a clear "tool reference"
	// shape — backticks in markdown, <code> in HTML, or directly named in
	// a JSON skill body. Reduces false positives from prose.
	patterns := []string{
		"`([a-z][a-z0-9_]+)`",
		`<code>([a-z][a-z0-9_]+)</code>`,
		`tool-name">([a-z][a-z0-9_]+)`,
	}
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		res = append(res, regexp.MustCompile(p))
	}

	roots := []string{
		filepath.Join(repoRoot, "internal/web/templates/for-agents.html"),
		filepath.Join(repoRoot, "internal/web/templates/connect.html"),
		filepath.Join(repoRoot, "docs/index.html"),
		filepath.Join(repoRoot, "README.md"),
	}

	// Also scan all skill bodies — agents read them after list_skills /
	// get_skill, and a phantom tool there is the same lie.
	skillsDir := filepath.Join(repoRoot, "content/skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				roots = append(roots, filepath.Join(skillsDir, e.Name()))
			}
		}
	}

	// Known false positives — tokens that look tool-shaped but aren't.
	// Verb-noun-like nouns from skill descriptions or technical jargon.
	isLikelyFalsePositive := func(s string) bool {
		switch s {
		case "click_log", "lukasz_kapusniak", "go_test", "fly_deploy",
			"docs_storyboards", "fly_logs", "lex_fri", "go_build",
			"git_init", "data_content", "personas_skills", "fly_ssh",
			"add_generic", "find_generic", "static_compliance",
			"pre_commit", "force_with", "description_agents":
			return true
		}
		// IDs ending in _id, _at — these are field names, not tools.
		if strings.HasSuffix(s, "_id") || strings.HasSuffix(s, "_at") {
			return true
		}
		return false
	}

	out := map[string][]string{}
	for _, path := range roots {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(repoRoot, path)
		seenInFile := map[string]bool{}
		for _, re := range res {
			for _, m := range re.FindAllStringSubmatch(string(data), -1) {
				name := m[1]
				if isLikelyFalsePositive(name) {
					continue
				}
				// Must contain at least one underscore — that's our
				// signal it's a tool-style name, not a CSS class.
				if !strings.Contains(name, "_") {
					continue
				}
				if seenInFile[name] {
					continue
				}
				seenInFile[name] = true
				out[name] = append(out[name], rel)
			}
		}
	}
	return out
}
