package content

import (
	"path/filepath"
	"testing"
)

func TestLoadRitualManifest(t *testing.T) {
	// Real manifest in repo — verifies syntax + defaults.
	m, err := LoadRitualManifest("../../content", "narada")
	if err != nil {
		t.Fatalf("load narada.json: %v", err)
	}
	if m.Type != "narada" {
		t.Errorf("type: %q", m.Type)
	}
	if len(m.KeywordRoutes) == 0 {
		t.Error("no routes loaded")
	}
	if m.MinPersonas < 1 || m.MaxPersonas < m.MinPersonas {
		t.Errorf("bad min/max: %d/%d", m.MinPersonas, m.MaxPersonas)
	}
}

func TestLoadRitualManifestMissing(t *testing.T) {
	_, err := LoadRitualManifest(filepath.Join(t.TempDir(), "content"), "nope")
	if err == nil {
		t.Error("expected error for missing manifest")
	}
}

func TestRoutePersonasSecurityContext(t *testing.T) {
	m, err := LoadRitualManifest("../../content", "narada")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := m.RoutePersonas("wyciek EDIT_TOKEN do logów — jak zabezpieczyć na przyszłość")
	if len(got) < m.MinPersonas {
		t.Errorf("expected at least %d personas, got %d: %v", m.MinPersonas, len(got), got)
	}
	// Ghost or Hodor should be routed by "token" / "wyciek".
	if !containsAny(got, []string{"ghost", "hodor"}) {
		t.Errorf("security context missed ghost/hodor: %v", got)
	}
}

func TestRoutePersonasArchitectureContext(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	got := m.RoutePersonas("architektura async queue vs webhooks — tradeoff")
	if !containsAny(got, []string{"mira-chen", "maruda"}) {
		t.Errorf("architecture context missed mira/maruda: %v", got)
	}
}

func TestRoutePersonasDefaultsWhenNoMatch(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	got := m.RoutePersonas("random context with no matching keywords at all whatsoever")
	if len(got) < m.MinPersonas {
		t.Errorf("expected at least %d defaults, got %d: %v", m.MinPersonas, len(got), got)
	}
}

func TestRoutePersonasCappedAtMax(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	// Concatenate many trigger words to hit lots of routes.
	got := m.RoutePersonas("secret token security architecture deploy test decision docs ux legal prompt data voice contrarian")
	if len(got) > m.MaxPersonas {
		t.Errorf("expected at most %d personas, got %d: %v", m.MaxPersonas, len(got), got)
	}
}

func TestRoutePersonasDedup(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	got := m.RoutePersonas("secret token secret token")
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Errorf("dup persona: %s", p)
		}
		seen[p] = true
	}
}

func containsAny(haystack []string, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n {
				return true
			}
		}
	}
	return false
}
