package krag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/config"
)

const phrase = "a w sercu wciąż ta sama cisza"

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "personas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	persona := "---\nslug: mira\ntitle: Mira\nrole: architektura\ntags: [system, review]\n---\nJestem Mira. Pytam o założenia zanim o rozwiązania.\n"
	if err := os.WriteFile(filepath.Join(dir, "personas", "mira.md"), []byte(persona), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := `{"slug":"code-review","category":"inzynieria","title":"Code review","body":"Czytaj diff, nie pliki.","tags":["review"]}`
	if err := os.WriteFile(filepath.Join(dir, "skills", "code-review.json"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cfgWith(dir string, scopes []string, expires time.Time) *config.Config {
	return &config.Config{
		ContentDir: dir,
		FriendTokens: map[string]*config.FriendTokenSpec{
			"antek": {Token: phrase, Scopes: scopes, ExpiresAt: expires},
		},
	}
}

func fullScopes() []string {
	return []string{"persona:mira", "skill:code-review", "literatura"}
}

func TestNormalizeFoldsDiacriticsHyphensAndCase(t *testing.T) {
	want := "a w sercu wciaz ta sama cisza"
	for _, in := range []string{
		phrase,
		"A-W-SERCU-Wciąż-Ta-Sama-Cisza",
		"  a  w sercu   wciąż ta sama cisza  ",
		"a_w_sercu_wciaz_ta_sama_cisza",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, chcę %q", in, got, want)
		}
	}
}

func TestResolveReturnsScopedBundle(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, fullScopes(), time.Now().Add(24*time.Hour))

	b, ok := Resolve(cfg, "A-W-Sercu-Wciaz-Ta-Sama-Cisza")
	if !ok {
		t.Fatal("Resolve odrzuciło poprawną frazę")
	}
	if b.TokenID != "antek" {
		t.Errorf("TokenID = %q, chcę antek", b.TokenID)
	}
	if len(b.Personas) != 1 || b.Personas[0].Slug != "mira" {
		t.Errorf("persony = %+v, chcę jedną mirę", b.Personas)
	}
	if len(b.Skills) != 1 || b.Skills[0].Slug != "code-review" {
		t.Errorf("skille = %+v, chcę jeden code-review", b.Skills)
	}
	if len(b.Missing) != 0 {
		t.Errorf("Missing = %v, chcę puste", b.Missing)
	}
}

// Zakres vaultowy ("literatura") nie może sam z siebie zbudować paczki —
// inaczej token mysłoodsiewni otwierałby /krag z pustą zawartością i
// potwierdzałby trafioną frazę.
func TestResolveRejectsVaultOnlyScopes(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, []string{"literatura", "note"}, time.Now().Add(24*time.Hour))
	if _, ok := Resolve(cfg, phrase); ok {
		t.Fatal("token wyłącznie vaultowy otworzył krąg")
	}
}

func TestResolveRejectsExpired(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, fullScopes(), time.Now().Add(-time.Minute))
	if _, ok := Resolve(cfg, phrase); ok {
		t.Fatal("wygasły token otworzył krąg")
	}
}

// ADR-0001 Z6: brak expires_at to horcrux. config odsiewa takie wpisy przy
// ładowaniu, ale Resolve nie może na tym polegać — gdyby spec trafił tu inną
// drogą, zerowa data musi znaczyć odmowę, nie wieczność.
func TestResolveRejectsZeroExpiry(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, fullScopes(), time.Time{})
	if _, ok := Resolve(cfg, phrase); ok {
		t.Fatal("token bez expires_at otworzył krąg")
	}
}

func TestResolveRejectsUnknownPhraseAndEmptyConfig(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, fullScopes(), time.Now().Add(24*time.Hour))
	for _, in := range []string{"nic dwa razy sie nie zdarza", "", "   "} {
		if _, ok := Resolve(cfg, in); ok {
			t.Errorf("fraza %q otworzyła krąg", in)
		}
	}
	if _, ok := Resolve(&config.Config{ContentDir: dir}, phrase); ok {
		t.Error("pusta mapa tokenów otworzyła krąg")
	}
	if _, ok := Resolve(nil, phrase); ok {
		t.Error("nil config otworzył krąg")
	}
}

func TestResolveRecordsMissingScopes(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, []string{"persona:mira", "persona:nie-ma-mnie", "skill:tez-nie"}, time.Now().Add(time.Hour))
	b, ok := Resolve(cfg, phrase)
	if !ok {
		t.Fatal("Resolve odrzuciło paczkę, w której jedna persona istnieje")
	}
	if len(b.Missing) != 2 {
		t.Errorf("Missing = %v, chcę dwa wpisy", b.Missing)
	}
}

func TestRenderCarriesBodiesAndExpiry(t *testing.T) {
	dir := fixture(t)
	cfg := cfgWith(dir, fullScopes(), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))
	b, ok := Resolve(cfg, phrase)
	if !ok {
		t.Fatal("Resolve odrzuciło poprawną frazę")
	}
	out := Render(b, "kapoost", "kapoost.humanmcp.net")

	for _, want := range []string{
		"Jestem Mira",             // treść promptu persony
		"Czytaj diff, nie pliki.", // treść skilla
		"1 March 2027",            // data wygaśnięcia
		"kapoost",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render nie zawiera %q", want)
		}
	}
	// Slug przyjaciela służy do audytu po stronie serwera i nie ma prawa
	// pojawić się w tekście, który przyjaciel wkleja gdzie indziej.
	if strings.Contains(out, "antek") {
		t.Error("render ujawnia slug tokenu przyjaciela")
	}
}

func TestVaultScopesDropsNamespacedEntries(t *testing.T) {
	got := config.VaultScopes([]string{"literatura", "persona:mira", "note", "skill:code-review"})
	if len(got) != 2 || got[0] != "literatura" || got[1] != "note" {
		t.Errorf("VaultScopes = %v, chcę [literatura note]", got)
	}
	if config.VaultScopes(nil) != nil {
		t.Error("VaultScopes(nil) musi zwrócić nil — nil znaczy właściciel, bez limitu zakresu")
	}
}

func TestScopesWithPrefixStripsAndFilters(t *testing.T) {
	in := []string{"persona:mira", "skill:code-review", "literatura", "persona:  ", "persona:tomas"}
	gotP := config.ScopesWithPrefix(in, config.ScopePrefixPersona)
	if len(gotP) != 2 || gotP[0] != "mira" || gotP[1] != "tomas" {
		t.Errorf("persony = %v, chcę [mira tomas]", gotP)
	}
	gotS := config.ScopesWithPrefix(in, config.ScopePrefixSkill)
	if len(gotS) != 1 || gotS[0] != "code-review" {
		t.Errorf("skille = %v, chcę [code-review]", gotS)
	}
}
