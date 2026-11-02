package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

const kragPhrase = "a w sercu wciąż ta sama cisza"

func kragFixture(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"personas", "skills"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	persona := "---\nslug: mira\ntitle: Mira\nrole: architektura\n---\nJestem Mira. Pytam o założenia.\n"
	if err := os.WriteFile(filepath.Join(dir, "personas", "mira.md"), []byte(persona), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		ContentDir: dir,
		AuthorName: "kapoost",
		Domain:     "kapoost.humanmcp.net",
		FriendTokens: map[string]*config.FriendTokenSpec{
			"antek": {
				Token:     kragPhrase,
				Scopes:    []string{"persona:mira"},
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
			},
		},
	}
	store := content.NewStore(dir)
	_ = store.Load()
	h := NewHandler(cfg, store, auth.New(""))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	// Distinct IP per call site unless the test overrides it — the bucket is
	// per-IP and would otherwise leak between subtests.
	req.Header.Set("Fly-Client-IP", "198.51.100."+path[len(path)-1:])
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestKragServesBundleForKnownPhrase(t *testing.T) {
	mux := kragFixture(t)
	rec := get(t, mux, "/krag/a-w-sercu-wciaz-ta-sama-cisza")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, chcę 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Jestem Mira") {
		t.Error("odpowiedź nie zawiera promptu persony")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("brak no-store — fraza nie może osiąść w cache")
	}
	if !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
		t.Error("brak noindex")
	}
}

// Każda porażka musi być nieodróżnialna. Inny kod odpowiedzi albo inna długość
// ciała zamieniłyby endpoint w wyrocznię potwierdzającą trafioną frazę.
func TestKragFailuresAreIndistinguishable(t *testing.T) {
	mux := kragFixture(t)

	unknown := get(t, mux, "/krag/nic-dwa-razy-sie-nie-zdarza1")
	empty := get(t, mux, "/krag/2")
	nested := get(t, mux, "/krag/a/b3")

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"nieznana fraza": unknown,
		"pusta fraza":    empty,
		"zagnieżdżona":   nested,
	} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, chcę 404", name, rec.Code)
		}
	}
	if unknown.Body.String() != empty.Body.String() || unknown.Body.String() != nested.Body.String() {
		t.Errorf("ciała odpowiedzi się różnią: %q / %q / %q",
			unknown.Body.String(), empty.Body.String(), nested.Body.String())
	}
}

// Zgadywacz wyczerpuje budżet, zanim dojdzie do porównania frazy, a po
// wyczerpaniu dostaje dokładnie to samo 404 — nie 429, bo inny kod
// powiedziałby mu, że w ogóle trafił na działający endpoint.
func TestKragRateLimitsPerIPAndStaysShapeless(t *testing.T) {
	mux := kragFixture(t)
	const ip = "203.0.113.7"

	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Fly-Client-IP", ip)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 5; i++ {
		call("/krag/zla-fraza")
	}
	// Szósta próba w godzinie — nawet poprawna fraza nie przechodzi.
	blocked := call("/krag/a-w-sercu-wciaz-ta-sama-cisza")
	if blocked.Code != http.StatusNotFound {
		t.Fatalf("po limicie status = %d, chcę 404", blocked.Code)
	}
	if strings.Contains(blocked.Body.String(), "Mira") {
		t.Fatal("limit nie zadziałał — paczka wyszła mimo wyczerpanego budżetu")
	}
}

func TestRobotsDisallowsKrag(t *testing.T) {
	mux := kragFixture(t)
	rec := get(t, mux, "/robots.txt")
	if !strings.Contains(rec.Body.String(), "Disallow: /krag/") {
		t.Errorf("robots.txt nie wyklucza /krag/: %q", rec.Body.String())
	}
}

// ── narada u użytkownika ────────────────────────────────────────────────────
//
// Krąg istnieje po to, żeby przyjaciel przeprowadził naradę na swoim
// komputerze: wkleja paczkę, rozdaje prompty swoim subagentom, serwer nie
// bierze w tym udziału. Kontrakt jest ten sam co w
// TestPrepareNaradaReturnsRunnablePrompts — paczka bez pełnych ciał promptów
// jest gorsza niż żadna, bo agent zacznie improwizować persony z pamięci,
// czyli robić dokładnie to, czemu narada ma zapobiegać.

const (
	miraBody  = "Jestem Mira. Pytam o założenia zanim o rozwiązania. OSTATNIE ZDANIE MIRY."
	ghostBody = "Jestem Ghost. Myślę jak atakujący. OSTATNIE ZDANIE GHOSTA."
	tomasBody = "Jestem Tomas. Pytam jak duże, jak często, kto czyta. OSTATNIE ZDANIE TOMASA."
	// Na dysku, poza zakresem kręgu — nie ma prawa wejść do paczki.
	hermesBody = "Jestem Hermes. ZDANIE KTORE NIE MOZE WYCIEC."
)

func naradaFixture(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"personas", "skills"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(slug, role, body string) {
		md := "---\nslug: " + slug + "\ntitle: " + strings.ToUpper(slug[:1]) + slug[1:] +
			"\nrole: " + role + "\n---\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "personas", slug+".md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mira", "architektura", miraBody)
	write("ghost", "red team", ghostBody)
	write("tomas", "dane", tomasBody)
	write("hermes", "proces", hermesBody) // celowo poza zakresem

	cfg := &config.Config{
		ContentDir: dir,
		AuthorName: "kapoost",
		Domain:     "kapoost.humanmcp.net",
		FriendTokens: map[string]*config.FriendTokenSpec{
			"antek": {
				Token:     kragPhrase,
				Scopes:    []string{"persona:mira", "persona:ghost", "persona:tomas"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
			},
		},
	}
	store := content.NewStore(dir)
	_ = store.Load()
	h := NewHandler(cfg, store, auth.New(""))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

// Panel dociera w całości: każda persona z zakresu ma pełne ciało promptu,
// łącznie z ostatnim zdaniem — obcięcie w połowie jest cichą awarią, bo
// paczka nadal wygląda sensownie.
func TestKragDeliversWholeNaradaPanel(t *testing.T) {
	mux := naradaFixture(t)
	rec := get(t, mux, "/krag/a-w-sercu-wciaz-ta-sama-cisza")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, chcę 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{miraBody, ghostBody, tomasBody} {
		if !strings.Contains(body, want) {
			t.Errorf("paczka nie zawiera pełnego promptu: %q", want)
		}
	}
	for _, want := range []string{"Mira", "Ghost", "Tomas", "architektura", "red team", "dane"} {
		if !strings.Contains(body, want) {
			t.Errorf("paczka nie zawiera %q — bez tytułu i roli nie wiadomo, kto mówi", want)
		}
	}
}

// Zakres jest całą kontrolą dostępu kręgu. Persona leżąca obok na dysku, ale
// nieprzyznana, nie może wejść do paczki ani nazwą, ani treścią.
func TestKragPanelExcludesUnscopedPersona(t *testing.T) {
	mux := naradaFixture(t)
	body := get(t, mux, "/krag/a-w-sercu-wciaz-ta-sama-cisza").Body.String()

	if strings.Contains(body, hermesBody) {
		t.Fatal("persona spoza zakresu wyciekła treścią")
	}
	if strings.Contains(body, "Hermes") {
		t.Error("persona spoza zakresu wyciekła nazwą")
	}
}

// Narada ma się odbyć u użytkownika, więc paczka musi być samowystarczalna:
// żadnego odsyłania do kolejnego wywołania, żadnej bramki sesji, żadnego
// żargonu MCP, którego przyjaciel bez podłączonego serwera nie wykona.
func TestKragPanelIsSelfContained(t *testing.T) {
	mux := naradaFixture(t)
	body := get(t, mux, "/krag/a-w-sercu-wciaz-ta-sama-cisza").Body.String()

	for _, forbidden := range []string{
		"bootstrap_session",
		"get_persona",
		"prepare_narada",
		"session_token",
		"Authorization",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("paczka odsyła do %q — przyjaciel bez MCP tego nie wykona", forbidden)
		}
	}
}
