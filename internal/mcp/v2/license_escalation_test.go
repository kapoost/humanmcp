package v2_test

import (
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// Wnioski licencyjne lądowały wyłącznie w skrzynce wiadomości, która nie ma
// kanału odpowiedzi. Ana Adams złożyła ten sam wniosek o cytat trzy razy
// w sześć dni. Wniosek wymagający decyzji należy do kolejki pytań — tam są
// odpowiedzi i tam działa odbiór przez powtórne pytanie.
func TestLicenseRequestOnAllRightsCreatesQuestion(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t,
		[5]string{"o-ludziach", "O ludziach", "public", "Treść.", "all-rights"})
	qs := content.NewQuestionStore(cfg.ContentDir)

	out := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "o-ludziach", "intended_use": "reprint in full in an anthology",
		"caller_id": "anthology-editor",
	}, nil)

	if !strings.Contains(out, "needs kapoost's decision") {
		t.Errorf("wniosek nie został zgłoszony jako wymagający decyzji:\n%s", out)
	}
	if !strings.Contains(out, "fetch_answer") {
		t.Errorf("odpowiedź nie mówi, jak odebrać decyzję:\n%s", out)
	}
	if got := len(qs.List()); got != 1 {
		t.Fatalf("pytań w kolejce: %d, oczekiwano 1", got)
	}
	if qs.List()[0].From != "anthology-editor" {
		t.Errorf("From = %q", qs.List()[0].From)
	}
}

// Odpowiedź dla CC-BY jest ostateczna — nie wolno zawracać nią głowy
// człowiekowi. To ten przypadek, w którym Ana Adams pytała trzy razy.
func TestLicenseRequestOnCCBYDoesNotCreateQuestion(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t,
		[5]string{"deka", "deka-log", "public", "Wspólny mianownik.", "cc-by"})
	qs := content.NewQuestionStore(cfg.ContentDir)

	out := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "deka", "intended_use": "quote in the Harbor Point Refresh newsletter",
		"caller_id": "ana-adams",
	}, nil)

	if strings.Contains(out, "needs kapoost's decision") {
		t.Errorf("CC-BY niepotrzebnie trafiło do kolejki:\n%s", out)
	}
	if got := len(qs.List()); got != 0 {
		t.Errorf("utworzono %d pytań dla licencji, która odpowiada sama", got)
	}
}

// Powtórny wniosek nie mnoży bliźniaków, a gdy decyzja już zapadła — oddaje
// ją na miejscu.
func TestRepeatedLicenseRequestDoesNotDuplicate(t *testing.T) {
	h, cfg := gateFixtureWithPieces(t,
		[5]string{"love", "nie miłość", "public", "Nie to samo.", "all-rights"})
	qs := content.NewQuestionStore(cfg.ContentDir)

	args := map[string]any{
		"slug": "love", "intended_use": "reprint in a paid newsletter",
		"caller_id": "newsletter",
	}
	callV2Tool(t, h, "request_license", args, nil)
	if got := len(qs.List()); got != 1 {
		t.Fatalf("po pierwszym wniosku: %d pytań", got)
	}

	second := callV2Tool(t, h, "request_license", args, nil)
	if got := len(qs.List()); got != 1 {
		t.Errorf("powtórny wniosek utworzył bliźniaka: %d pytań", got)
	}
	if !strings.Contains(second, "already asked") {
		t.Errorf("powtórka nie rozpoznana:\n%s", second)
	}

	// Po odpowiedzi kolejny wniosek oddaje ją od razu.
	if err := qs.Answer(qs.List()[0].ID, "Tak, na ten jeden numer."); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	third := callV2Tool(t, h, "request_license", args, nil)
	if !strings.Contains(third, "Tak, na ten jeden numer.") {
		t.Errorf("decyzja nie wróciła do wnioskodawcy:\n%s", third)
	}
	if got := len(qs.List()); got != 1 {
		t.Errorf("trzeci wniosek utworzył pytanie: %d", got)
	}
}
