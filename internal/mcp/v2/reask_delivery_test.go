package v2_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	v2 "github.com/kapoost/humanmcp-go/internal/mcp/v2"
	"github.com/kapoost/humanmcp-go/internal/rituals"
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

// Dwa z ośmiu pytań oczekujących 21 września 2026 pytały o tytuł utworu
// spod znanego sluga — czyli o coś, co list_content zwraca od ręki. Jeżeli
// pytanie wskazuje konkretny utwór, odpowiedź ask_human ma to powiedzieć
// od razu, zamiast kazać czekać na człowieka.
func TestAskHumanPointsAtThePieceItMentions(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"private-parts", "deka-log", "public", "Wspólny mianownik.", ""})

	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from":     "researcher",
		"question": "What is the exact title of your poem with slug 'private-parts'?",
	}, nil)

	if !strings.Contains(out, "YOU MAY ALREADY HAVE THIS") {
		t.Errorf("brak podpowiedzi o utworze:\n%s", out)
	}
	if !strings.Contains(out, `read_content(slug="private-parts")`) {
		t.Errorf("podpowiedź nie wskazuje konkretnego wywołania:\n%s", out)
	}
	// Pytanie i tak trafia do kolejki — to podpowiedź, nie bramka.
	if !strings.Contains(out, "Question submitted") {
		t.Errorf("podpowiedź zablokowała zadanie pytania:\n%s", out)
	}
}

// Zwykłe słowo, które przypadkiem jest slugiem, nie może wywoływać
// podpowiedzi. To ta sama pułapka, przez którą „commit" trafiał do prawnika
// od własności intelektualnej.
func TestAskHumanDoesNotHintOnOrdinaryWords(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"love", "nie miłość", "public", "Nie to samo.", ""})

	out := callV2Tool(t, h, "ask_human", map[string]any{
		"from":     "reader",
		"question": "I love your work — what got you started writing?",
	}, nil)

	if strings.Contains(out, "YOU MAY ALREADY HAVE THIS") {
		t.Errorf("fałszywa podpowiedź na zwykłym słowie:\n%s", out)
	}
}

// gateFixture buduje magazyn treści, ale go NIE wczytuje — żaden inny test
// v2 nie dotyka utworów, więc nikomu to nie przeszkadzało. Podpowiedź
// o slugu musi widzieć prawdziwą treść, więc tutaj zasiewamy pliki i
// wołamy Load() PRZED zbudowaniem serwera.
func gateFixtureWithPieces(t *testing.T, pieces ...[5]string) (http.Handler, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"personas", "skills", "blobs", "collections",
		"provenance", "messages", "questions", "memory", "journals", "rituals", "stats"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	for _, p := range pieces { // slug, title, access, body, license (opcjonalna)
		md := "---\nslug: " + p[0] + "\ntitle: " + p[1] + "\ntype: poem\naccess: " + p[2] +
			"\npublished: 2026-03-31\n"
		if p[4] != "" {
			md += "license: " + p[4] + "\n"
		}
		md += "---\n\n" + p[3]
		if err := os.WriteFile(filepath.Join(dir, p[0]+".md"), []byte(md), 0o644); err != nil {
			t.Fatalf("write piece: %v", err)
		}
	}
	cfg := &config.Config{
		AuthorName: "test", Domain: "test.example", ContentDir: dir,
		EditToken: "testtoken", SessionSecret: "gate-fixture-secret",
	}
	store := content.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	backend := mcp.NewBackend(cfg, store, auth.New("testtoken"), rituals.New(cfg))
	return v2.New(cfg, backend), cfg
}

// MCP przysyła clientInfo przy każdym połączeniu, a kod nie czytał go ani
// razu — przy dziesięciu pytaniach na trzydzieści bez nadawcy. Teraz służy
// za zastępcze `from`, dzięki czemu takie pytanie da się rozpoznać jako
// powtórne i posortować w kolejce.
func TestClientInfoFillsMissingFrom(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t)
	store := content.NewQuestionStore(cfg.ContentDir)

	// callV2Tool przedstawia się jako clientInfo name "shape_test".
	out := callV2Tool(t, h, "ask_human", map[string]any{
		"question": "Kto pyta, jeśli nikt się nie przedstawił?",
	}, nil)
	if !strings.Contains(out, "Question submitted") {
		t.Fatalf("pytanie nie powstało:\n%s", out)
	}

	qs := store.List()
	if len(qs) != 1 {
		t.Fatalf("oczekiwano 1 pytania, jest %d", len(qs))
	}
	if qs[0].From == "" {
		t.Error("pole From nadal puste — clientInfo nie zostało użyte")
	}
	if !strings.Contains(qs[0].From, "shape_test") {
		t.Errorf("From = %q, oczekiwano nazwy klienta z clientInfo", qs[0].From)
	}
}

// Jawne `from` ma pierwszeństwo: agent, który się nazwał, wie lepiej niż
// jego środowisko uruchomieniowe.
func TestExplicitFromWinsOverClientInfo(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t)
	store := content.NewQuestionStore(cfg.ContentDir)

	callV2Tool(t, h, "ask_human", map[string]any{
		"from": "chapbook-editor", "question": "Czy jawne from wygrywa?",
	}, nil)

	qs := store.List()
	if len(qs) != 1 || qs[0].From != "chapbook-editor" {
		t.Errorf("From = %q, oczekiwano chapbook-editor", qs[0].From)
	}
}

// search_content zapisywało się jako EventList, czyli nie do odróżnienia od
// list_content. Bez własnego typu zdarzenia nie da się sprawdzić, czy
// wyszukiwarka faktycznie zdjęła pytania z kolejki — a właśnie po to
// powstała.
func TestSearchContentRecordsSearchEventWithQuery(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t,
		[5]string{"private-parts", "deka-log", "public", "Wspólny mianownik.", ""})

	callV2Tool(t, h, "search_content", map[string]any{"query": "mianownik"}, nil)

	stats, err := content.NewStatStore(cfg.ContentDir).Compute()
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if stats.TotalSearches < 1 {
		t.Errorf("TotalSearches = %d — wyszukiwanie nie policzyło się jako wyszukiwanie", stats.TotalSearches)
	}
	if stats.TopSearches["mianownik"] < 1 {
		t.Errorf("zapytania nie ma w TopSearches: %v — nie dowiemy się, czego agenci szukają", stats.TopSearches)
	}
}
