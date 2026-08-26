package content

import "testing"

// The four naradas that prompted this rewrite (nar-b9a725ae769a,
// nar-e2c3081f1f73, nar-810a09bfc4f1, nar-548f32e4c682) returned the same
// five personas across security, legal/billing and two UI/UX contexts.
// Two independent causes, both pinned below: routes were ranked by their
// position in narada.json rather than by evidence, and keywords matched
// anywhere inside a word rather than at its start.
//
// These assert first place, not membership. A domain persona that lands
// fourth behind two irrelevant ones is the failure being fixed — the panel
// is capped, so rank is what decides who actually speaks.

func firstIs(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("router returned nothing, wanted %s first", want)
	}
	if got[0] != want {
		t.Errorf("wanted %s first, got %v", want, got)
	}
}

func TestRouteUXContextSeatsUXPersonaFirst(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")

	// nar-810a09bfc4f1 — Polish UI question. Used to route to
	// [mira-chen maruda axel-brandt eleanor-voss lukasz-mazur]: "architektura"
	// (from "architektura informacji") outranked UX on manifest order, and
	// "ci" inside "obciążenie"/"dostępności" pulled in the QA persona.
	firstIs(t, m.RoutePersonas(`Redesign UI dyktafonu PWA. Typografia i hierarchia na liście `+
		`nagrań, dostępność (WCAG, ARIA, rozmiary celów dotykowych), architektura informacji, `+
		`obciążenie poznawcze podczas nagrywania, mobile-first i punkty załamania.`), "eleanor-voss")

	// nar-548f32e4c682 — the same question in English. Used to return
	// [mira-chen maruda hermes]: zero UX personas on a context that is
	// nothing but UX, because the UX route's keywords were almost all
	// English terms the text happened not to use.
	firstIs(t, m.RoutePersonas(`typography, WCAG, information architecture, cognitive load, `+
		`mobile-first, breakpoints, touch targets, ARIA, F-pattern eye tracking`), "eleanor-voss")
}

func TestRouteLegalContextSeatsLawyerFirst(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	// nar-e2c3081f1f73. RODO and the inflected "licencyjna" matched nothing
	// at all, so the lawyer lost to whatever technical route fired first.
	firstIs(t, m.RoutePersonas(`RODO i prawa autorskie przy transkrypcjach, umowa licencyjna `+
		`z klientem, billing i faktury za subskrypcje.`), "harvey")
}

// Anchoring is the half of the fix that removes false evidence. Each of
// these words used to route somewhere absurd.
func TestRouteIgnoresKeywordsBuriedInsideWords(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	for _, c := range []struct{ word, mustNot string }{
		{"obciążenie", "axel-brandt"},  // "ci" from CI/CD
		{"dostępności", "axel-brandt"}, // "ci" again — accessibility routed to QA
		{"html", "tomas-reyes"},        // "ml"
		{"build", "eleanor-voss"},      // "ui"
		{"guide", "eleanor-voss"},      // "ui"
		{"dialogu", "yuki-tanaka"},     // "log"
	} {
		got := m.RoutePersonas(c.word)
		for _, p := range got {
			if p == c.mustNot {
				t.Errorf("%q still routes to %s: %v", c.word, c.mustNot, got)
			}
		}
	}
}

// The other half must survive: several manifest keywords are stems that
// only work by matching the head of a longer word. Anchoring is deliberately
// one-sided so these keep firing.
func TestRouteStillMatchesInflectedStems(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	for _, c := range []struct{ text, want string }{
		{"rotacja kluczy w keychainie", "ghost"},
		{"weryfikacja źródeł", "julka"},
		{"dostępność ekranu", "eleanor-voss"},
		{"umowy licencyjne", "harvey"},
	} {
		if !containsSlug(m.RoutePersonas(c.text), c.want) {
			t.Errorf("%q lost %s: %v", c.text, c.want, m.RoutePersonas(c.text))
		}
	}
}

// Evidence beats position. The architecture route sits at index 3 and UX at
// index 9, so under the old order-is-priority rule a single architecture hit
// buried a context that is otherwise entirely about interface design.
func TestRouteRanksByEvidenceNotManifestOrder(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	got := m.RoutePersonas(`architektura informacji, typografia, kontrast, WCAG, ARIA, ` +
		`breakpoint, makieta, użyteczność`)
	firstIs(t, got, "eleanor-voss")
}

// Security must not regress — it is the one domain where a missed persona
// is worse than a spurious one, and it was the accidental winner before.
func TestRouteSecurityContextUnchanged(t *testing.T) {
	m, _ := LoadRitualManifest("../../content", "narada")
	got := m.RoutePersonas("wyciek EDIT_TOKEN do logów — rotacja klucza, audyt uprawnień")
	if !containsSlug(got, "ghost") || !containsSlug(got, "hodor") {
		t.Errorf("security context lost its guardians: %v", got)
	}
	firstIs(t, got, "ghost")
}

func containsSlug(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
