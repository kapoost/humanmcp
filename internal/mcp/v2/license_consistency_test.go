package v2_test

import (
	"fmt"
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

// Certyfikat i request_license opisują TĘ SAMĄ licencję. 21 września 2026
// rozjechały się: certyfikat przestał zapraszać do kupna pełni praw, a
// request_license nadal kazał „negotiate rights". Agent dostawał sprzeczne
// odpowiedzi zależnie od tego, które narzędzie zawołał.
func TestAllRightsSaysTheSameInBothTools(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"o-ludziach", "O ludziach", "public", "Treść.", "all-rights"})

	cert := callV2Tool(t, h, "get_certificate", map[string]any{"slug": "o-ludziach"}, nil)
	lic := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "o-ludziach", "intended_use": "quote in a newsletter", "caller_id": "ana",
	}, nil)

	for name, out := range map[string]string{"certyfikat": cert, "request_license": lic} {
		for _, forbidden := range []string{"negotiate rights", "open to selling", "Full IP transfer"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s nadal mówi %q:\n%s", name, forbidden, out)
			}
		}
		if !strings.Contains(out, "NOT for sale") {
			t.Errorf("%s nie mówi, że pełnia praw nie jest na sprzedaż:\n%s", name, out)
		}
	}
	if !strings.Contains(lic, "ask_human") {
		t.Errorf("request_license nie kieruje do kanału z odpowiedzią:\n%s", lic)
	}
}

// Odpowiedź na licencję CC-BY jest ostateczna. „Logged for audit purposes"
// brzmiało jak sprawa w toku — Ana Adams złożyła ten sam wniosek na utwór
// CC-BY trzy razy w ciągu sześciu dni, za każdym razem dostawszy „Permitted".
func TestCCBYLicenseAnswerSaysItIsFinal(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"deka", "deka-log", "public", "Wspólny mianownik.", "cc-by"})

	out := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "deka", "intended_use": "quote in the Harbor Point Refresh newsletter",
		"caller_id": "ana-adams",
	}, nil)

	if !strings.Contains(out, "Permitted with attribution") {
		t.Fatalf("CC-BY nie zostało od razu dozwolone:\n%s", out)
	}
	if !strings.Contains(out, "final") || !strings.Contains(out, "no human sign-off") {
		t.Errorf("odpowiedź nie mówi, że nic nie jest w toku:\n%s", out)
	}
}

// Do 23 września 2026 pod KAŻDYM utworem stało „You may share, quote, and
// reference this piece freely with attribution" — również pod all-rights.
// To najgłośniejsza powierzchnia serwera: zdanie stoi bezpośrednio pod
// wierszem, przy każdym odczycie. Agent działający w dobrej wierze dostawał
// pisemną zgodę na rozpowszechnianie czegoś, czego autor rozpowszechniać
// nie chce.
func TestReadContentStatesTheRealLicence(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"o-ludziach", "O ludziach", "public", "Treść.", "all-rights"},
		[5]string{"deka", "deka-log", "public", "Wspólny mianownik.", "cc-by"},
		[5]string{"venus", "Venus test", "public", "Szkic.", "cc-by-nc"})

	allRights := callV2Tool(t, h, "read_content", map[string]any{"slug": "o-ludziach"}, nil)
	if strings.Contains(allRights, "freely with attribution") {
		t.Errorf("utwór all-rights nadal zaprasza do swobodnego udostępniania:\n%s", allRights)
	}
	if !strings.Contains(allRights, "All rights reserved") ||
		!strings.Contains(allRights, "needs permission") {
		t.Errorf("utwór all-rights nie podaje swoich warunków:\n%s", allRights)
	}

	ccby := callV2Tool(t, h, "read_content", map[string]any{"slug": "deka"}, nil)
	if !strings.Contains(ccby, "CC BY 4.0") {
		t.Errorf("CC-BY nie nazwane po imieniu:\n%s", ccby)
	}

	nc := callV2Tool(t, h, "read_content", map[string]any{"slug": "venus"}, nil)
	if !strings.Contains(nc, "non-commercial") {
		t.Errorf("CC-BY-NC nie ostrzega przed użyciem komercyjnym:\n%s", nc)
	}
	if strings.Contains(nc, "including commercially") {
		t.Errorf("CC-BY-NC zaprasza do użycia komercyjnego:\n%s", nc)
	}
}

// Stopka kierowała wyłącznie do leave_comment. Krakowska grupa wpisała więc
// pytania w komentarz, bo nic nie wskazało im kanału z odpowiedzią.
func TestReadContentPointsAtTheChannelThatAnswers(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"piosenki", "Piosenka1.txt", "public", "Ten bóg to prąd.", "cc-by"})

	out := callV2Tool(t, h, "read_content", map[string]any{"slug": "piosenki"}, nil)
	if !strings.Contains(out, "ask_human") {
		t.Errorf("czytelnik nie dowiaduje się, jak zadać pytanie:\n%s", out)
	}
	if !strings.Contains(out, "one-way") {
		t.Errorf("nie powiedziano, że komentarz nie wraca:\n%s", out)
	}
}

// about_humanmcp mówił „42 tools total", gdy tools/list zwracał 43 — wpisana
// liczba rozjechała się w dniu dodania search_content. Ten sam rodzaj dryfu
// łapie już strażnik przy docs/index.html; tu brakowało odpowiednika.
func TestAboutHumanmcpToolCountMatchesReality(t *testing.T) {
	h, _ := gateFixtureWithPieces(t)
	about := callV2Tool(t, h, "about_humanmcp", map[string]any{}, nil)
	listed := callV2ToolsList(t, h)

	want := fmt.Sprintf("(%d tools total", len(listed))
	if !strings.Contains(about, want) {
		t.Errorf("about_humanmcp nie deklaruje %q; tools/list zwraca %d narzędzi",
			want, len(listed))
	}
}

// Pole Description pokazywała dotąd wyłącznie strona /p/. Czytelnik przez
// przeglądarkę widział notę autorską, a agent czytający ten sam utwór przez
// read_content — nie. Skoro komentarze są jednokierunkowe, odpowiedź autora
// musi umieć dotrzeć sama.
func TestReadContentShowsAuthorNote(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nslug: piosenki\ntitle: Piosenka1.txt\ntype: poem\naccess: public\n" +
		"license: cc-by\npublished: 2026-03-31\ndescription: Brama i napis w pierwszym wersie są zamierzone.\n---\n\nTen bóg to prąd."
	if err := os.WriteFile(filepath.Join(dir, "piosenki.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	store := content.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := &config.Config{AuthorName: "kapoost", Domain: "test.example", ContentDir: dir,
		EditToken: "testtoken", SessionSecret: "s"}
	h := v2.New(cfg, mcp.NewBackend(cfg, store, auth.New("testtoken"), rituals.New(cfg)))

	out := callV2Tool(t, h, "read_content", map[string]any{"slug": "piosenki"}, nil)
	if !strings.Contains(out, "Od autora:") {
		t.Errorf("nota autorska nie dociera przez read_content:\n%s", out)
	}
	if !strings.Contains(out, "są zamierzone") {
		t.Errorf("treść noty zgubiona:\n%s", out)
	}
}

// Utwór bez noty nie może dostać pustego nagłówka.
func TestReadContentWithoutNoteHasNoEmptyHeading(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"bez-noty", "Bez noty", "public", "Treść.", "cc-by"})
	out := callV2Tool(t, h, "read_content", map[string]any{"slug": "bez-noty"}, nil)
	if strings.Contains(out, "Od autora:") {
		t.Errorf("pusta nota wyrenderowana:\n%s", out)
	}
}
