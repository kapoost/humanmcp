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

// Formularz edycji miał DWA pola name="license". Pierwsze, w ozdobnym bloku
// „artwork", nie miało żadnej logiki zaznaczania i zaczynało się od opcji
// „all rights reserved" — a FormValue bierze pierwszą wartość. Zapisanie
// edycji ustawiało więc all-rights KAŻDEMU utworowi, w tym pięciu bez
// zapisanej licencji, działającym na domyślnym CC-BY: okładce chapbooka,
// „Piosenka1.txt" i „Sumie człowieczeństwa", na które w tym tygodniu
// wysłaliśmy pisemne potwierdzenia, że są CC-BY.
func editFormFixture(t *testing.T, license string) string {
	t.Helper()
	dir := t.TempDir()
	lic := ""
	if license != "" {
		lic = "license: " + license + "\n"
	}
	md := "---\nslug: probny\ntitle: Próbny\ntype: poem\naccess: public\n" + lic +
		"published: 2026-03-31\n---\n\nTreść."
	if err := os.WriteFile(filepath.Join(dir, "probny.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	store := content.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&config.Config{AuthorName: "kapoost", Domain: "t.example",
		ContentDir: dir, EditToken: "tok"}, store, auth.New("tok"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/edit/probny", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /edit/probny → %d", rec.Code)
	}
	return rec.Body.String()
}

func TestEditFormHasExactlyOneLicenseField(t *testing.T) {
	body := editFormFixture(t, "cc-by")
	if n := strings.Count(body, `name="license"`); n != 1 {
		t.Errorf(`formularz ma %d pól name="license", oczekiwano 1 — pierwsze wygrywa w FormValue`, n)
	}
}

func TestEditFormPreselectsStoredLicense(t *testing.T) {
	body := editFormFixture(t, "cc-by")
	// Patrzymy WYŁĄCZNIE w pole, które wysyła dane. Ozdobny select stoi
	// wyżej w dokumencie i ma te same wartości, więc szukanie od początku
	// trafiłoby w niego — na tym właśnie polegała pomyłka w pierwszej wersji
	// tego testu.
	sel := licenseSelect(t, body)
	i := strings.Index(sel, `value="cc-by"`)
	if i < 0 {
		t.Fatal("brak opcji cc-by w polu licencji")
	}
	if !strings.Contains(sel[i:min(i+80, len(sel))], "selected") {
		t.Errorf("zapisana licencja cc-by nie jest zaznaczona — zapis zmieniłby ją po cichu")
	}
}

// Utwór bez zapisanej licencji działa na domyślnym CC-BY. Formularz nie może
// go po cichu przestawić na all-rights.
func TestEditFormDoesNotDefaultToAllRights(t *testing.T) {
	sel := licenseSelect(t, editFormFixture(t, ""))
	i := strings.Index(sel, `value="all-rights"`)
	if i >= 0 && strings.Contains(sel[i:min(i+80, len(sel))], "selected") {
		t.Error("utwór bez licencji ma zaznaczone all-rights")
	}
}

// licenseSelect wycina zawartość pola, które naprawdę wysyła licencję.
func licenseSelect(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `name="license"`)
	if i < 0 {
		t.Fatal(`brak pola name="license"`)
	}
	j := strings.Index(body[i:], "</select>")
	if j < 0 {
		t.Fatal("niedomknięty select licencji")
	}
	return body[i : i+j]
}
