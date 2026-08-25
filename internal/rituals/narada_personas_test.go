package rituals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/config"
)

// naradaFixture builds a content dir with a known persona roster and a
// narada manifest whose FIRST keyword route is the security one — the
// real manifest's shape, and the reason every context containing an
// incidental "token" / "log" / "design" seated the same five voices.
func naradaFixture(t *testing.T) *Worker {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"personas", "rituals", "journals"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	for _, slug := range []string{"ghost", "hodor", "yuki-tanaka", "mira-chen", "maruda", "eleanor-voss", "harvey"} {
		body := "---\nslug: " + slug + "\ntitle: " + slug + "\nrole: fixture\ntags: [test]\n---\nFixture body for " + slug + "."
		if err := os.WriteFile(filepath.Join(dir, "personas", slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatalf("write persona %s: %v", slug, err)
		}
	}
	manifest := `{
	  "type": "narada",
	  "default_personas": ["mira-chen", "hermes", "george-carlin"],
	  "min_personas": 3,
	  "max_personas": 5,
	  "keyword_routes": [
	    {"keywords": ["token", "secret"], "personas": ["ghost", "hodor"]},
	    {"keywords": ["security"], "personas": ["ghost", "yuki-tanaka"]},
	    {"keywords": ["log"], "personas": ["yuki-tanaka"]},
	    {"keywords": ["design", "system"], "personas": ["mira-chen", "maruda"]},
	    {"keywords": ["ux", "accessibility"], "personas": ["eleanor-voss"]}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(dir, "rituals", "narada.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return New(&config.Config{ContentDir: dir})
}

// TestNaradaExplicitPersonasOverrideRouter is the regression this whole
// change exists for: four consecutive naradas returned the identical five
// personas because the router substring-matches keywords in manifest
// order, with no notion of how central a term is and no reading of an
// explicit request. The context below is deliberately loaded with the
// keywords that used to win — an explicit list must beat all of them.
func TestNaradaExplicitPersonasOverrideRouter(t *testing.T) {
	w := naradaFixture(t)
	ctx := "redesign UI systemu — token sesji, logi, security review"

	routed, _, err := w.CreateNaradaJob(ctx, "", nil)
	if err != nil {
		t.Fatalf("routed narada: %v", err)
	}
	if len(routed.Personas) != 5 {
		t.Fatalf("fixture no longer reproduces the crowd-out: %v", routed.Personas)
	}
	if !contains(routed.Personas, "ghost") || contains(routed.Personas, "eleanor-voss") {
		t.Fatalf("fixture drifted — expected security voices in, UX voice crowded out: %v", routed.Personas)
	}

	_, got, err := w.CreateNaradaJob(ctx, "", []string{"eleanor-voss", "harvey"})
	if err != nil {
		t.Fatalf("explicit narada: %v", err)
	}
	want := []string{"eleanor-voss", "harvey"}
	if !equal(got, want) {
		t.Errorf("explicit personas ignored: got %v, want %v", got, want)
	}
}

// The explicit list is also what lands on disk — the worker generates
// voices from job.Personas, so an override that returned the right list
// to the caller but persisted the routed one would produce the exact
// symptom being fixed, just one step later.
func TestNaradaExplicitPersonasPersisted(t *testing.T) {
	w := naradaFixture(t)
	job, _, err := w.CreateNaradaJob("token security design", "", []string{"harvey"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, err := w.RitualStore().Get(job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !equal(stored.Personas, []string{"harvey"}) {
		t.Errorf("on-disk personas: got %v, want [harvey]", stored.Personas)
	}
}

// An explicit list bypasses min_personas. Padding a one-voice request up
// to three from default_personas would reseat the very voices the caller
// was trying to avoid.
func TestNaradaExplicitPersonasBypassMinimum(t *testing.T) {
	w := naradaFixture(t)
	_, got, err := w.CreateNaradaJob("cokolwiek", "", []string{"harvey"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !equal(got, []string{"harvey"}) {
		t.Errorf("min_personas padded an explicit list: %v", got)
	}
}

func TestNaradaExplicitPersonasNormalized(t *testing.T) {
	w := naradaFixture(t)
	_, got, err := w.CreateNaradaJob("cokolwiek", "", []string{" Harvey ", "harvey", "ELEANOR-VOSS", ""})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !equal(got, []string{"harvey", "eleanor-voss"}) {
		t.Errorf("normalization: got %v, want [harvey eleanor-voss]", got)
	}
}

// Unknown slugs fail loudly. A silent drop would leave the caller unable
// to tell a typo from a router disagreement — the same invisible failure
// mode that made the original routing bug hard to see.
func TestNaradaUnknownPersonaRejected(t *testing.T) {
	w := naradaFixture(t)
	_, _, err := w.CreateNaradaJob("cokolwiek", "", []string{"harvey", "contrarian"})
	if err == nil {
		t.Fatal("expected error for unknown slug")
	}
	if !strings.Contains(err.Error(), "contrarian") {
		t.Errorf("error must name the bad slug: %v", err)
	}
	if !strings.Contains(err.Error(), "harvey") {
		t.Errorf("error must list available slugs: %v", err)
	}
}

func TestNaradaTooManyPersonasRejected(t *testing.T) {
	w := naradaFixture(t)
	_, _, err := w.CreateNaradaJob("cokolwiek", "",
		[]string{"ghost", "hodor", "yuki-tanaka", "mira-chen", "maruda", "harvey"})
	if err == nil {
		t.Fatal("expected error when exceeding max_personas")
	}
	if !strings.Contains(err.Error(), "max is 5") {
		t.Errorf("error should name the cap: %v", err)
	}
}

// Omitting the argument keeps today's behavior — this is the backward-compat
// pin, since every existing caller passes nil.
func TestNaradaNoExplicitFallsBackToRouter(t *testing.T) {
	w := naradaFixture(t)
	for _, explicit := range [][]string{nil, {}, {"  "}} {
		_, got, err := w.CreateNaradaJob("accessibility audit", "", explicit)
		if err != nil {
			t.Fatalf("create(%v): %v", explicit, err)
		}
		if !contains(got, "eleanor-voss") {
			t.Errorf("router did not run for explicit=%v: %v", explicit, got)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
