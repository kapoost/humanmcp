package rituals

import (
	"strings"
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// The offline pack has to be worth running. What separates it from "here
// are some persona files" is that each SYSTEM prompt carries the persona's
// journal — the same material the server-side pipeline feeds Sonnet. If
// that silently drops out, the pack still looks fine and the voices come
// back shallower, with nothing to point at.
func TestNaradaPackCarriesSynthesisedPatterns(t *testing.T) {
	w := naradaFixture(t)
	const patternText = "**Przedwczesna abstrakcja** — sprawdzasz drugi przypadek, nie trzeci."
	err := w.PersonaJournalStore().WritePatterns("harvey", content.PersonaPatterns{
		SynthesisedAt:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		EntriesAtSynthesis: 7,
		Patterns:           patternText,
	})
	if err != nil {
		t.Fatalf("write patterns: %v", err)
	}

	pack, err := w.BuildNaradaPack("kontekst testowy", []string{"harvey"})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if len(pack.Personas) != 1 {
		t.Fatalf("expected 1 persona, got %d", len(pack.Personas))
	}
	p := pack.Personas[0]
	if p.JournalSource != "patterns" {
		t.Errorf("journal source: got %q, want patterns", p.JournalSource)
	}
	if !strings.Contains(p.System, patternText) {
		t.Errorf("SYSTEM prompt dropped the patterns:\n%s", p.System)
	}
	// Same framing the online path uses (buildSystemPrompt), so a subagent
	// reads the journal in the position the persona expects it.
	if !strings.Contains(p.System, "Rekapitulacja twojego dziennika przed tą naradą") {
		t.Errorf("SYSTEM prompt lost the journal framing:\n%s", p.System)
	}
	if !strings.Contains(p.System, "Fixture body for harvey") {
		t.Errorf("SYSTEM prompt lost the persona body:\n%s", p.System)
	}
	if !strings.Contains(p.User, "kontekst testowy") {
		t.Errorf("USER prompt lost the narada context:\n%s", p.User)
	}
}

// Personas with no journal must still produce a usable prompt, and must be
// distinguishable from ones whose journal failed to load.
func TestNaradaPackWithoutJournal(t *testing.T) {
	w := naradaFixture(t)
	pack, err := w.BuildNaradaPack("kontekst", []string{"harvey"})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	p := pack.Personas[0]
	if p.JournalSource != "" {
		t.Errorf("journal source: got %q, want empty", p.JournalSource)
	}
	if strings.Contains(p.System, "Rekapitulacja") {
		t.Errorf("journal framing present with no journal:\n%s", p.System)
	}
	if !strings.Contains(p.System, "Fixture body for harvey") {
		t.Errorf("persona body missing:\n%s", p.System)
	}
}

// Raw reflections are the fallback for personas whose patterns have not
// been synthesised yet — without it, a persona that has made mistakes but
// not yet been synthesised ships as if it had a clean record.
func TestNaradaPackFallsBackToRawJournal(t *testing.T) {
	w := naradaFixture(t)
	err := w.PersonaJournalStore().Append("harvey", content.PersonaJournalEntry{
		At:             time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		NaradaID:       "nar-abc123abc123",
		Recommendation: "podpisz bez czytania",
		Reflection:     "Nie ufaj terminom, których nie przeczytałeś.",
	})
	if err != nil {
		t.Fatalf("append journal: %v", err)
	}

	pack, err := w.BuildNaradaPack("kontekst", []string{"harvey"})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	p := pack.Personas[0]
	if p.JournalSource != "journal" {
		t.Errorf("journal source: got %q, want journal", p.JournalSource)
	}
	if !strings.Contains(p.System, "Nie ufaj terminom") {
		t.Errorf("SYSTEM prompt dropped the raw reflection:\n%s", p.System)
	}
}

// Panel selection is shared with CreateNaradaJob — same override, same
// routing, same rejections — and the pack reports which one fired.
func TestNaradaPackSharesPanelSelection(t *testing.T) {
	w := naradaFixture(t)

	explicit, err := w.BuildNaradaPack("token security design", []string{"eleanor-voss", "harvey"})
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if explicit.Routed {
		t.Error("explicit pack reported itself as routed")
	}
	if len(explicit.Personas) != 2 ||
		explicit.Personas[0].Slug != "eleanor-voss" || explicit.Personas[1].Slug != "harvey" {
		t.Errorf("explicit panel wrong or reordered: %+v", explicit.Personas)
	}

	routed, err := w.BuildNaradaPack("accessibility audit", nil)
	if err != nil {
		t.Fatalf("routed: %v", err)
	}
	if !routed.Routed {
		t.Error("routed pack reported itself as explicit")
	}
	if len(routed.Personas) == 0 {
		t.Error("routed pack came back empty")
	}

	if _, err := w.BuildNaradaPack("x", []string{"contrarian"}); err == nil ||
		!strings.Contains(err.Error(), "contrarian") {
		t.Errorf("unknown slug not rejected like CreateNaradaJob: %v", err)
	}
}

// A manifest entry pointing at a deleted persona costs one voice, not the
// whole sitting — matching how the online pipeline degrades. The fixture's
// default_personas names hermes and george-carlin, neither of which has a
// file, so the routed path exercises this for real.
func TestNaradaPackReportsMissingPersonas(t *testing.T) {
	w := naradaFixture(t)
	pack, err := w.BuildNaradaPack("accessibility audit", nil)
	if err != nil {
		t.Fatalf("a missing persona took down the whole pack: %v", err)
	}
	if len(pack.Missing) == 0 {
		t.Fatal("missing personas were dropped silently")
	}
	if !contains(pack.Missing, "hermes") {
		t.Errorf("expected hermes in Missing, got %v", pack.Missing)
	}
	for _, p := range pack.Personas {
		if p.Slug == "hermes" {
			t.Error("hermes was both missing and seated")
		}
	}

	// Nothing loadable at all is still an error — an empty pack would let
	// the caller believe a narada ran with no voices.
	if _, err := w.BuildNaradaPack("x", []string{"hermes"}); err == nil {
		t.Error("expected an error when no persona in the panel could be loaded")
	}
}

// The pack must never create a job. Its whole contract is statelessness;
// a stray Create here would leave jobs that the worker picks up and bills
// for, defeating the reason to use offline mode at all.
func TestNaradaPackWritesNoJob(t *testing.T) {
	w := naradaFixture(t)
	if _, err := w.BuildNaradaPack("kontekst", []string{"harvey"}); err != nil {
		t.Fatalf("build pack: %v", err)
	}
	recent, err := w.RitualStore().ListRecent(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recent) != 0 {
		t.Errorf("offline pack wrote %d ritual job(s): %+v", len(recent), recent)
	}
}
