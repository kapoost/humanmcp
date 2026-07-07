package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RitualManifest defines how a ritual routes context to personas. One
// manifest per ritual type, loaded from content/rituals/<type>.json.
type RitualManifest struct {
	Type            string          `json:"type"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	DefaultPersonas []string        `json:"default_personas"`
	MinPersonas     int             `json:"min_personas"`
	MaxPersonas     int             `json:"max_personas"`
	KeywordRoutes   []KeywordRoute  `json:"keyword_routes"`
}

// KeywordRoute maps a set of keywords to a set of personas. Any keyword
// substring-matching the context (case-insensitive) triggers all personas.
type KeywordRoute struct {
	Keywords []string `json:"keywords"`
	Personas []string `json:"personas"`
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
// consult for the given context. Order: keyword-matched routes in manifest
// order, then default_personas as filler up to min_personas. Capped at
// max_personas.
func (m *RitualManifest) RoutePersonas(context string) []string {
	ctx := strings.ToLower(context)
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

	for _, route := range m.KeywordRoutes {
		if len(out) >= m.MaxPersonas {
			break
		}
		for _, kw := range route.Keywords {
			kw = strings.ToLower(strings.TrimSpace(kw))
			if kw == "" {
				continue
			}
			if strings.Contains(ctx, kw) {
				for _, p := range route.Personas {
					add(p)
					if len(out) >= m.MaxPersonas {
						break
					}
				}
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
