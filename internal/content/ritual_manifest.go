package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RitualManifest defines how a ritual routes context to personas. One
// manifest per ritual type, loaded from content/rituals/<type>.json.
type RitualManifest struct {
	Type            string         `json:"type"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	DefaultPersonas []string       `json:"default_personas"`
	MinPersonas     int            `json:"min_personas"`
	MaxPersonas     int            `json:"max_personas"`
	KeywordRoutes   []KeywordRoute `json:"keyword_routes"`
}

// KeywordRoute maps a set of keywords to a set of personas. A keyword
// counts as a hit when it appears at the start of a word in the context
// (case-insensitive); the number of distinct hits is what ranks the route
// against the others. See RoutePersonas.
type KeywordRoute struct {
	Keywords []string `json:"keywords"`
	Personas []string `json:"personas"`
	// Pinned seats this route's personas ahead of every ranked route the
	// moment it matches at all, regardless of how few keywords it hit.
	// Same doctrine as the guardian in suggest_skills: the context that
	// mentions a credential in passing is exactly the one where nobody
	// remembered to ask for Hodor, and evidence-ranking would let a
	// keyword-dense UX question take all five seats.
	Pinned bool `json:"pinned"`
}

// LoadRitualManifest reads a manifest from content/rituals/<type>.json.
func LoadRitualManifest(contentDir, typ string) (*RitualManifest, error) {
	path := filepath.Join(contentDir, "rituals", typ+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", typ, err)
	}
	var m RitualManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", typ, err)
	}
	if m.MinPersonas <= 0 {
		m.MinPersonas = 3
	}
	if m.MaxPersonas <= 0 {
		m.MaxPersonas = 5
	}
	return &m, nil
}

// RoutePersonas returns the deduplicated ordered list of persona slugs to
// consult for the given context.
//
// Routes are ranked by how many distinct keywords they match, not by their
// position in the manifest. Order used to be priority: the first routes in
// narada.json won every contest and the loop stopped once max_personas was
// full, so the security routes (positions 1-3) crowded out UX (10) and
// legal (11) on every context that mentioned a key in passing. Manifest
// order now only breaks ties between routes with equal evidence.
//
// One exception, ahead of ranking: a route marked "pinned" is seated on any
// hit at all. Ranking alone would let five keyword-dense routes take every
// seat in a context whose single security keyword is the one that matters —
// "czy token sesji wolno logować?" at the end of a product review returned a
// panel with neither ghost nor hodor. Evidence decides who else sits; it does
// not get to decide whether the guardians sit.
//
// Matching is anchored to the start of a word. It stays open-ended on the
// right so stem keywords keep catching inflections — "klucz" still matches
// "kluczy", "weryfik" still matches "weryfikacja" — but a keyword can no
// longer be found buried inside an unrelated word. That was not a marginal
// bug: "ci" (from CI/CD) matched "obciążenie" and "dostępności", so the two
// most UX-central words in a design question routed to the QA persona,
// while "log" matched "dialog" and "ml" matched "html".
func (m *RitualManifest) RoutePersonas(context string) []string {
	ctx := strings.ToLower(context)

	type rankedRoute struct {
		personas []string
		hits     int
		order    int
		pinned   bool
	}
	var ranked []rankedRoute
	for i, route := range m.KeywordRoutes {
		hits := 0
		counted := map[string]bool{}
		for _, kw := range route.Keywords {
			kw = strings.ToLower(strings.TrimSpace(kw))
			if kw == "" || counted[kw] {
				continue
			}
			counted[kw] = true
			if containsAtWordStart(ctx, kw) {
				hits++
			}
		}
		if hits > 0 {
			ranked = append(ranked, rankedRoute{personas: route.Personas, hits: hits, order: i, pinned: route.Pinned})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].pinned != ranked[b].pinned {
			return ranked[a].pinned
		}
		if ranked[a].hits != ranked[b].hits {
			return ranked[a].hits > ranked[b].hits
		}
		return ranked[a].order < ranked[b].order
	})

	seen := map[string]bool{}
	var out []string
	add := func(slug string) {
		slug = strings.TrimSpace(strings.ToLower(slug))
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		out = append(out, slug)
	}

	for _, r := range ranked {
		if len(out) >= m.MaxPersonas {
			break
		}
		for _, p := range r.personas {
			add(p)
			if len(out) >= m.MaxPersonas {
				break
			}
		}
	}

	// Fill from defaults up to min_personas.
	for _, p := range m.DefaultPersonas {
		if len(out) >= m.MinPersonas {
			break
		}
		add(p)
	}

	if len(out) > m.MaxPersonas {
		out = out[:m.MaxPersonas]
	}
	return out
}

// containsAtWordStart reports whether kw occurs in ctx at the start of a
// word — i.e. preceded by something that is not a letter or a digit.
//
// Deliberately not a full word-boundary check on both ends: several
// manifest keywords are stems ("weryfik", "medycz", "metaboli", "aranż")
// that only work because they match the head of a longer word.
//
// A keyword that does not itself begin with a letter or digit is matched
// anywhere: the anchor is a test on the character before the keyword, and
// for ".env" that character is the dot, so requiring a word start would
// mean ".env" matches " .env" but not "config.env" — silently dropping the
// evidence in the route where a miss costs most.
func containsAtWordStart(ctx, kw string) bool {
	if kw == "" {
		return false
	}
	if first, _ := utf8.DecodeRuneInString(kw); !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		return strings.Contains(ctx, kw)
	}
	for from := 0; from < len(ctx); {
		i := strings.Index(ctx[from:], kw)
		if i < 0 {
			return false
		}
		at := from + i
		if at == 0 {
			return true
		}
		prev, _ := utf8.DecodeLastRuneInString(ctx[:at])
		if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
			return true
		}
		from = at + 1
	}
	return false
}
