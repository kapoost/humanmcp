package v2_test

import (
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// prepare_narada hands the caller full persona bodies for a whole panel.
// get_persona gives those out only to bootstrapped callers, so an open
// prepare_narada would be a five-at-a-time bypass of that gate. This is
// the assertion that keeps the two tools' access rules aligned.
func TestPrepareNaradaIsSessionGated(t *testing.T) {
	h, cfg := gateFixture(t)

	anon := callV2Tool(t, h, "prepare_narada", map[string]any{"context": "accessibility review"}, nil)
	if strings.Contains(anon, gateWitnessBody) {
		t.Fatalf("anonymous caller received persona bodies: %q", anon)
	}
	if !strings.Contains(anon, "requires an active session") {
		t.Errorf("gate message drift: %q", anon)
	}

	token := mcp.GenerateSessionToken(cfg.SessionSecret)
	ok := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "accessibility review", "session_token": token,
	}, nil)
	if !strings.Contains(ok, gateWitnessBody) {
		t.Errorf("session_token argument did not unlock the pack: %q", ok)
	}
}

// The pack is the deliverable: one SYSTEM/USER pair per persona, plus the
// instruction to fan them out. A pack that arrives without the prompts is
// worse than useless — the agent will fall back to improvising the
// personas from memory, which is the one thing both narada tools exist to
// prevent.
func TestPrepareNaradaReturnsRunnablePrompts(t *testing.T) {
	h, cfg := gateFixture(t)
	token := mcp.GenerateSessionToken(cfg.SessionSecret)
	got := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "czy przepisać router na scoring", "session_token": token,
	}, nil)

	for _, want := range []string{
		"NARADA PACK (offline)",
		"--- SYSTEM ---",
		"--- USER ---",
		"Spawn ONE subagent per persona",
		gateWitnessBody,        // the persona's own prompt body
		"Kontekst narady:",     // buildUserPrompt framing, same as the online path
		"czy przepisać router", // the caller's context reached the USER block
	} {
		if !strings.Contains(got, want) {
			t.Errorf("pack missing %q\n\ngot:\n%s", want, got)
		}
	}
}

// Stateless by decision: prepare_narada must not mint a job. If it ever
// starts creating one, the reply would carry an ID and agents would tag
// commits with a narada that has no voices and no journal entries.
func TestPrepareNaradaRecordsNothing(t *testing.T) {
	h, cfg := gateFixture(t)
	token := mcp.GenerateSessionToken(cfg.SessionSecret)
	got := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "accessibility review", "session_token": token,
	}, nil)

	if strings.Contains(got, "ID: nar-") {
		t.Errorf("offline narada minted a job ID: %q", got)
	}
	if strings.Contains(got, "fetch_narada_result(id=") {
		t.Errorf("offline narada told the agent to poll a job it never created: %q", got)
	}
	// It must say so out loud — an agent that assumes symmetry with
	// run_narada will otherwise invent a [narada:<id>] tag at commit time.
	if !strings.Contains(got, "NOT RECORDED") {
		t.Errorf("pack does not state that nothing was recorded: %q", got)
	}
}

// Panel selection is shared with run_narada, so the explicit override and
// its error paths must behave identically. A caller who learns personas=
// on one tool will expect it on the other.
func TestPrepareNaradaSharesPanelSelection(t *testing.T) {
	h, cfg := gateFixture(t)
	token := mcp.GenerateSessionToken(cfg.SessionSecret)

	explicit := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "accessibility review", "personas": []string{"gate-witness"}, "session_token": token,
	}, nil)
	if !strings.Contains(explicit, "Panel: taken from your `personas` argument.") {
		t.Errorf("explicit panel not reported: %q", explicit)
	}

	routed := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "accessibility review", "session_token": token,
	}, nil)
	if !strings.Contains(routed, "chosen by the keyword manifest") {
		t.Errorf("routed panel not reported: %q", routed)
	}

	bad := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "x", "personas": []string{"contrarian"}, "session_token": token,
	}, nil)
	if !strings.Contains(bad, "Unknown persona slug(s): contrarian") {
		t.Errorf("unknown slug not rejected the same way as run_narada: %q", bad)
	}
}

// A persona with no journal must still ship a usable prompt, and the pack
// must say which of the three journal states it is in — otherwise a caller
// cannot tell "this persona has learned nothing yet" from "the journal
// silently failed to load".
func TestPrepareNaradaReportsJournalState(t *testing.T) {
	h, cfg := gateFixture(t)
	token := mcp.GenerateSessionToken(cfg.SessionSecret)
	got := callV2Tool(t, h, "prepare_narada", map[string]any{
		"context": "accessibility review", "session_token": token,
	}, nil)
	if !strings.Contains(got, "Journal: none — this persona has no recorded mistakes yet.") {
		t.Errorf("journal state not reported for a journal-less persona: %q", got)
	}
}
