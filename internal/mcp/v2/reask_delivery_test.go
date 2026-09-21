package v2_test

import (
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// Re-asking is the delivery channel for agents that cannot keep an ID.
//
// Measured on the live queue in September 2026: kapoost had written 16
// answers, agents had collected 3, and nobody had collected one since June.
// Meanwhile four separate agents asked the same question twice — that is the
// signal a stateless caller actually produces, so ask_human treats it as the
// pickup.

func TestReAskReturnsExistingAnswer(t *testing.T) {
	h, cfg := gateFixture(t)
	store := content.NewQuestionStore(cfg.ContentDir)

	q, err := store.Create("literary-analysis-agent", "", "How do divisors become love?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Answer(q.ID, "Bo reszta z dzielenia jest tym, czego nie da się oddać."); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	before := len(store.List())

	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from":     "literary-analysis-agent",
		"question": "How do divisors become love?",
	}, nil)

	if !strings.Contains(out, "reszta z dzielenia") {
		t.Errorf("re-ask did not return the answer:\n%s", out)
	}
	if strings.Contains(out, "Question submitted") {
		t.Errorf("re-ask created a fresh question instead of delivering:\n%s", out)
	}
	if got := len(store.List()); got != before {
		t.Errorf("question count %d → %d; a duplicate was created", before, got)
	}
	reloaded, err := store.Get(q.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reloaded.IsFetched() {
		t.Error("delivered answer was not marked fetched — it would sit in 'awaiting pickup' forever")
	}
}

func TestReAskOfUnansweredDoesNotDuplicate(t *testing.T) {
	h, cfg := gateFixture(t)
	store := content.NewQuestionStore(cfg.ContentDir)

	q, err := store.Create("researcher", "", "What is the exact title of the poem at 'private-parts'?")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := len(store.List())

	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from":     "researcher",
		"question": "What is the exact title of the poem at 'private-parts'?",
	}, nil)

	if !strings.Contains(out, q.ID) {
		t.Errorf("reply did not carry the original ID %s:\n%s", q.ID, out)
	}
	if !strings.Contains(out, "already asked") {
		t.Errorf("reply did not say the question was a repeat:\n%s", out)
	}
	if got := len(store.List()); got != before {
		t.Errorf("question count %d → %d; the twin that orphans kapoost's answer was created anyway", before, got)
	}
}

func TestAnonymousReAskStillCreatesQuestion(t *testing.T) {
	h, cfg := gateFixture(t)
	store := content.NewQuestionStore(cfg.ContentDir)

	if _, err := store.Create("", "", "Who are you?"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := len(store.List())

	out := callV2Tool(t, h, "ask_human", map[string]any{"question": "Who are you?"}, nil)

	// Without a `from` we cannot tell two callers apart, and handing one
	// caller an answer written for another is worse than a duplicate.
	if !strings.Contains(out, "Question submitted") {
		t.Errorf("anonymous caller was matched against someone else's question:\n%s", out)
	}
	if got := len(store.List()); got != before+1 {
		t.Errorf("question count %d → %d, wanted %d", before, got, before+1)
	}
}

func TestEmailEscapeHatchOnlyWhenConfigured(t *testing.T) {
	h, cfg := gateFixture(t)

	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from": "one-shot-agent", "question": "Silent by default?",
	}, nil)
	if strings.Contains(out, "IF YOU KNOW YOU CANNOT COME BACK") {
		t.Error("escape hatch offered with no CONTACT_EMAIL set")
	}

	cfg.ContactEmail = "kontakt@example.test"
	out = callV2Tool(t, h, "ask_human", map[string]any{
		"from": "one-shot-agent-2", "question": "And once configured?",
	}, nil)
	if !strings.Contains(out, "kontakt@example.test") {
		t.Errorf("escape hatch missing although CONTACT_EMAIL is set:\n%s", out)
	}
	if !strings.Contains(out, "tell YOUR user") {
		t.Errorf("escape hatch does not tell the agent to hand off to its human:\n%s", out)
	}
}

func TestAnonymousCallerIsNotSentToSessionGatedMemory(t *testing.T) {
	h, _ := gateFixture(t)
	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from": "drive-by", "question": "Where do I keep the id?",
	}, nil)

	// remember/recall are session-gated; ask_human is open. Advertising them
	// to anonymous callers — and with a remember(key=,value=) signature that
	// never existed — sent every drive-by agent down a dead end.
	if strings.Contains(out, `remember(key=`) || strings.Contains(out, `recall(key=`) {
		t.Errorf("reply still shows the non-existent remember/recall signature:\n%s", out)
	}
	if !strings.Contains(out, "SESSION-GATED") {
		t.Errorf("anonymous caller is not told the memory option is closed to them:\n%s", out)
	}
}
