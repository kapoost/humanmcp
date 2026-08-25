package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// ── list_skill_groups ───────────────────────────────────────────────────────

func registerListSkillGroups(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_skill_groups",
		Description: "Index of every skill tag in use, with slugs per group. Public — no bootstrap required.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult(renderSkillGroups(src.LoadSkills())), nil
	})
}

func renderSkillGroups(skills []mcp.Skill) string {
	groups := map[string][]string{}
	for _, s := range skills {
		for _, t := range s.Tags {
			key := strings.ToLower(strings.TrimSpace(t))
			if key == "" {
				continue
			}
			groups[key] = append(groups[key], s.Slug)
		}
	}
	if len(groups) == 0 {
		return "No skill groups defined yet. Skills need `tags` field populated (e.g. tags: [humanmcp, dev]) — see upsert_skill."
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	fmt.Fprintf(&sb, "SKILL GROUPS — %d in use:\n\n", len(names))
	for _, name := range names {
		slugs := groups[name]
		sort.Strings(slugs)
		fmt.Fprintf(&sb, "  %-20s (%d) %s\n", name, len(slugs), strings.Join(slugs, ", "))
	}
	sb.WriteString("\nCall load_skill_group(name) for a bulk fetch of every skill in a group.")
	return sb.String()
}

// ── load_skill_group ────────────────────────────────────────────────────────

func registerLoadSkillGroup(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "load_skill_group",
		Description: "Bulk-fetch every skill tagged with the given group name. Respects the bootstrap gate per-skill: -public suffix bypasses, everything else needs session.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` + sessionTokenSchemaProp + `},"required":["name"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var params struct {
			Name string `json:"name"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		if strings.TrimSpace(params.Name) == "" {
			return textResult("Podaj nazwę grupy. Użyj list_skill_groups żeby zobaczyć dostępne."), nil
		}
		name := strings.TrimSpace(params.Name)
		authenticated := sessionActiveOrToken(src, req)
		return textResult(renderLoadSkillGroup(src.LoadSkills(), name, authenticated)), nil
	})
}

func renderLoadSkillGroup(skills []mcp.Skill, name string, authenticated bool) string {
	var matched []mcp.Skill
	for _, s := range skills {
		if skillHasTag(s, name) {
			matched = append(matched, s)
		}
	}
	if len(matched) == 0 {
		seen := map[string]struct{}{}
		for _, s := range skills {
			for _, t := range s.Tags {
				key := strings.ToLower(strings.TrimSpace(t))
				if key != "" {
					seen[key] = struct{}{}
				}
			}
		}
		names := make([]string, 0, len(seen))
		for n := range seen {
			names = append(names, n)
		}
		sort.Strings(names)
		hint := "no groups defined yet"
		if len(names) > 0 {
			hint = "available: " + strings.Join(names, ", ")
		}
		return fmt.Sprintf("Skill group '%s' is empty. Hint — %s.", name, hint)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "SKILL GROUP: %s — %d skills\n\n", name, len(matched))
	locked := 0
	for _, s := range matched {
		publiclyAccessible := strings.HasSuffix(s.Slug, "-public")
		fmt.Fprintf(&sb, "## %s [%s]\n", s.Title, s.Category)
		if authenticated || publiclyAccessible {
			sb.WriteString(s.Body + "\n\n")
		} else {
			sb.WriteString("(body locked — call bootstrap_session with the session code to unlock)\n\n")
			locked++
		}
	}
	if locked > 0 {
		fmt.Fprintf(&sb, "---\n%d/%d skills locked. Ask user for the Polish poetry session code and call bootstrap_session.\n", locked, len(matched))
	}
	return sb.String()
}

// ── suggest_skills ──────────────────────────────────────────────────────────

func registerSuggestSkills(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "suggest_skills",
		Description: "Deterministic manifest→tag mapping. Given files + languages + git_origin, returns up to 8 skill slugs and up to 5 personas, each with the reason it fired. No LLM classification.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"string"}},"languages":{"type":"array","items":{"type":"string"}},"git_origin":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var params struct {
			Files     []string `json:"files"`
			Languages []string `json:"languages"`
			GitOrigin string   `json:"git_origin"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		return textResult(renderSuggestSkills(src.LoadSkills(), src.LoadPersonas(), params.Files, params.Languages, params.GitOrigin)), nil
	})
}

// groupPersonas maps a matched skill group to the personas that own that
// problem domain. Personas are derived from GROUPS, not from skills —
// humanmcp's Skill schema is {slug,category,title,body,tags} with no persona
// field, and adding one would mean schema drift plus a hand backfill of every
// skill. nar-993aae928f22 explicitly chose "reuse existing tags, no schema
// drift"; this honours that. A group is a problem domain, which is a better
// signal anyway: `safety` firing means the repo touches secrets, so it wants
// the guardian and the red team — not whoever happened to author a skill.
//
// Keep every slug in sync with content/personas/*.md. Unknown slugs are not
// silently dropped — renderSuggestSkills reports them (see missing below), so
// a renamed persona file surfaces as a visible gap instead of a dead suggestion.
var groupPersonas = map[string][]string{
	"always":         {"hodor", "hermiona"},
	"safety":         {"hodor", "ghost", "yuki-tanaka"},
	"dev":            {"mira-chen", "axel-brandt"},
	"engineering":    {"hermes", "axel-brandt"},
	"mcp":            {"zara"},
	"ritual":         {"hermiona"},
	"writing":        {"sophia-marchetti"},
	"humanmcp":       {"mira-chen", "conductor"},
	"mysloodsiewnia": {"hermiona", "tomas-reyes"},
	"adcp":           {"maruda", "harvey"},
	"bookkido":       {"eleanor-voss"},
	"onaudience":     {"sophia-marchetti", "ela"},
	"mx5":            {"kenji-mori"},
	"s2000":          {"kenji-mori"},
}

// guardianPersona is seated for every scaffold regardless of the cap. Same
// doctrine as the dobranoc ritual: Hodor is always loaded, because the moment
// a project needs him is the moment nobody remembered to ask for him.
const guardianPersona = "hodor"

// renderSuggestSkills is a straight port of the v1 toolSuggestSkills logic.
// Kept as one function on purpose: reading it top-to-bottom is easier than
// jumping between 5 helper functions for a rule engine this small.
func renderSuggestSkills(skills []mcp.Skill, personas []mcp.Persona, files, languages []string, gitOrigin string) string {
	fileSet := map[string]struct{}{}
	for _, f := range files {
		fileSet[strings.ToLower(strings.TrimSpace(f))] = struct{}{}
	}
	langSet := map[string]struct{}{}
	for _, l := range languages {
		langSet[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
	}
	origin := strings.ToLower(gitOrigin)

	type rule struct {
		group  string
		reason string
		match  bool
	}
	fileHas := func(name string) bool { _, ok := fileSet[strings.ToLower(name)]; return ok }
	langHas := func(name string) bool { _, ok := langSet[strings.ToLower(name)]; return ok }
	originContains := func(substr string) bool { return strings.Contains(origin, strings.ToLower(substr)) }

	rules := []rule{
		{"dev", "go.mod present", fileHas("go.mod")},
		{"dev", "package.json present", fileHas("package.json")},
		{"dev", "pyproject.toml present", fileHas("pyproject.toml")},
		{"dev", "Dockerfile present", fileHas("dockerfile") || fileHas("dockerfile.")},
		{"dev", "language=go", langHas("go")},
		{"dev", "language=typescript", langHas("typescript") || langHas("ts")},
		{"dev", "language=python", langHas("python") || langHas("py")},
		{"engineering", "storyboards/ directory present", fileHas("storyboards/") || fileHas("storyboards")},
		{"safety", ".env present (secrets nearby)", fileHas(".env") || fileHas(".env.example")},
		{"humanmcp", "git origin matches kapoost/humanmcp*", originContains("kapoost/humanmcp")},
		{"mysloodsiewnia", "git origin matches mysloodsiewnia", originContains("mysloodsiewnia")},
		{"adcp", "git origin matches adcp / abzu / purrsonality", originContains("adcp") || originContains("abzu") || originContains("purrsonality")},
		{"bookkido", "git origin matches bookkido", originContains("bookkido")},
		{"onaudience", "git origin matches onaudience", originContains("onaudience")},
	}

	groupReasons := map[string][]string{}
	for _, r := range rules {
		if !r.match {
			continue
		}
		groupReasons[r.group] = append(groupReasons[r.group], r.reason)
	}
	groupReasons["always"] = append(groupReasons["always"], "default (guardian + style)")

	type suggestion struct {
		Slug, Group, Explanation string
	}
	seen := map[string]struct{}{}
	var out []suggestion

	priorityOrder := []string{
		"humanmcp", "adcp", "bookkido", "mysloodsiewnia", "onaudience",
		"mx5", "s2000",
		"always",
		"engineering", "safety", "mcp", "ritual", "writing",
		"dev",
	}
	inPriority := map[string]struct{}{}
	for _, g := range priorityOrder {
		inPriority[g] = struct{}{}
	}
	extras := make([]string, 0)
	for g := range groupReasons {
		if _, known := inPriority[g]; !known {
			extras = append(extras, g)
		}
	}
	sort.Strings(extras)
	orderedGroups := append(append([]string{}, priorityOrder...), extras...)

	const maxSuggested = 8
	const perGroupCap = 3
	for _, g := range orderedGroups {
		if _, matched := groupReasons[g]; !matched {
			continue
		}
		groupTaken := 0
		for _, s := range skills {
			if len(out) >= maxSuggested {
				break
			}
			if groupTaken >= perGroupCap {
				break
			}
			if _, dup := seen[s.Slug]; dup {
				continue
			}
			if !skillHasTag(s, g) {
				continue
			}
			seen[s.Slug] = struct{}{}
			groupTaken++
			out = append(out, suggestion{
				Slug:        s.Slug,
				Group:       g,
				Explanation: strings.Join(groupReasons[g], "; "),
			})
		}
		if len(out) >= maxSuggested {
			break
		}
	}
	groupNames := make([]string, 0, len(groupReasons))
	for _, g := range orderedGroups {
		if _, matched := groupReasons[g]; matched {
			groupNames = append(groupNames, g)
		}
	}

	var sb strings.Builder
	sb.WriteString("SUGGESTED SKILLS (manifest-driven, deterministic)\n\n")
	sb.WriteString("Matched groups:\n")
	for _, g := range groupNames {
		fmt.Fprintf(&sb, "  %-20s %s\n", g, strings.Join(groupReasons[g], "; "))
	}
	fmt.Fprintf(&sb, "\nSuggested slugs — capped at %d (Axel + Conductor from nar-993aae928f22):\n", maxSuggested)
	if len(out) == 0 {
		sb.WriteString("  (none — no skills tagged with the matched groups; try upsert_skill with tags to populate)\n")
	}
	for _, s := range out {
		fmt.Fprintf(&sb, "  %-30s via %s — %s\n", s.Slug, s.Group, s.Explanation)
	}
	// ── personas, derived from the same matched groups ───────────────────
	// Walked in the same priority order as skills, so a project-specific
	// persona (maruda for adcp) outranks a generic one (mira for dev) when
	// the cap bites.
	roster := make(map[string]string, len(personas))
	for _, p := range personas {
		roster[strings.ToLower(p.Slug)] = p.Role
	}

	const maxPersonas = 5
	type personaPick struct{ Slug, Role, Group, Explanation string }
	var picks []personaPick
	var missing []string
	seenPersona := map[string]struct{}{}

	addPersona := func(slug, group, reason string) {
		slug = strings.ToLower(slug)
		if _, dup := seenPersona[slug]; dup {
			return
		}
		seenPersona[slug] = struct{}{}
		role, known := roster[slug]
		if !known {
			missing = append(missing, slug)
			return
		}
		picks = append(picks, personaPick{slug, role, group, reason})
	}

	// Guardian is seated before the cap can crowd him out.
	if _, matched := groupReasons["safety"]; matched {
		addPersona(guardianPersona, "safety", strings.Join(groupReasons["safety"], "; "))
	} else {
		addPersona(guardianPersona, "always", "guardian is always seated")
	}
	for _, g := range orderedGroups {
		if _, matched := groupReasons[g]; !matched {
			continue
		}
		for _, slug := range groupPersonas[g] {
			if len(picks) >= maxPersonas {
				break
			}
			addPersona(slug, g, strings.Join(groupReasons[g], "; "))
		}
		if len(picks) >= maxPersonas {
			break
		}
	}

	fmt.Fprintf(&sb, "\nSuggested personas — capped at %d (%s always seated):\n", maxPersonas, guardianPersona)
	if len(picks) == 0 {
		sb.WriteString("  (none — persona roster is empty; content/personas/*.md not loaded)\n")
	}
	for _, p := range picks {
		fmt.Fprintf(&sb, "  %-18s %-46s via %s — %s\n", p.Slug, truncateRole(p.Role), p.Group, p.Explanation)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(&sb, "  (mapped but not in roster: %s — groupPersonas drifted from content/personas/)\n",
			strings.Join(missing, ", "))
	}

	sb.WriteString("\nTo load them: call load_skill_group(name=<group>) for each matched group, OR get_skill(slug) individually.")
	sb.WriteString("\nFor personas: get_persona(slug) per suggestion.")
	return sb.String()
}

// truncateRole keeps the persona table one line per row. Roles like Zara's
// run past 60 chars and wrap in a terminal, which turns the table into mush.
// Counts runes, not bytes — roles carry Polish diacritics and em-dashes, and
// a byte slice would cut one in half and emit a replacement char.
func truncateRole(role string) string {
	const max = 46
	r := []rune(role)
	if len(r) <= max {
		return role
	}
	return string(r[:max-1]) + "…"
}
