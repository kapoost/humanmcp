package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

// llms.txt jest wyłącznie generowany. Wcześniej ręcznie zapisany
// /data/llms.txt zwracał się w CAŁOŚCI i pomijał wszystko generowane — więc
// jedno zapisanie przez /llms-edit cicho kasowało warunki dla agentów,
// limity, rozkład licencji i podpowiedź o wersji protokołu. Edytor usunięty;
// ten test pilnuje, że stary plik na wolumenie już niczego nie przesłoni.
func TestStrayCustomLLMSTxtDoesNotShadowGenerated(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plik zostawiony przez dawny edytor albo przez pomyłkę.
	if err := os.WriteFile(filepath.Join(dir, "llms.txt"),
		[]byte("# stara wersja\n\nnic więcej\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := content.NewStore(contentDir)
	_ = store.Load()
	h := NewHandler(&config.Config{AuthorName: "kapoost", Domain: "test.example",
		ContentDir: contentDir}, store, auth.New("t"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()

	if strings.Contains(body, "nic więcej") {
		t.Errorf("stary plik nadal przesłania wersję generowaną:\n%s", body)
	}
	for _, must := range []string{"## Terms for automated agents", "Rate limits", "2025-06-18"} {
		if !strings.Contains(body, must) {
			t.Errorf("brak %q w wygenerowanym llms.txt", must)
		}
	}
}

// Edytor został usunięty — trasa nie może wrócić niezauważona.
func TestLLMSEditRouteIsGone(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := content.NewStore(contentDir)
	_ = store.Load()
	h := NewHandler(&config.Config{AuthorName: "kapoost", Domain: "test.example",
		ContentDir: contentDir}, store, auth.New("t"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/llms-edit", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/llms-edit odpowiada kodem %d, oczekiwano 404", rec.Code)
	}
}
