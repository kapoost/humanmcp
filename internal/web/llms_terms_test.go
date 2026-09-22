package web

import (
	"strings"
	"testing"
)

// describeLicenseMix zasila zdanie o licencjach w llms.txt. Wpisany na sztywno
// licznik zdryfowałby przy pierwszym nowym utworze — dokładnie tak, jak
// „42 MCP tools" w docs/index.html rozjechało się z rzeczywistością.
func TestDescribeLicenseMix(t *testing.T) {
	got := describeLicenseMix(map[string]int{"cc-by": 9, "all-rights": 3, "cc-by-nc": 1})
	if got != "9 × cc-by, 3 × all-rights, 1 × cc-by-nc" {
		t.Errorf("opis = %q", got)
	}
}

// Pusty serwer nie może twierdzić, że cokolwiek opublikował.
func TestDescribeLicenseMixEmpty(t *testing.T) {
	if got := describeLicenseMix(map[string]int{}); got != "none published yet" {
		t.Errorf("pusty zbiór = %q", got)
	}
	if got := describeLicenseMix(nil); got != "none published yet" {
		t.Errorf("nil = %q", got)
	}
}

// Remis rozstrzygany alfabetycznie, żeby plik nie zmieniał się przy każdym
// odświeżeniu — inaczej diff llms.txt szumi bez powodu.
func TestDescribeLicenseMixIsStable(t *testing.T) {
	m := map[string]int{"cc-by": 2, "all-rights": 2}
	first := describeLicenseMix(m)
	for i := 0; i < 20; i++ {
		if got := describeLicenseMix(m); got != first {
			t.Fatalf("niestabilna kolejność: %q vs %q", got, first)
		}
	}
	if !strings.HasPrefix(first, "2 × all-rights") {
		t.Errorf("remis nie rozstrzygnięty alfabetycznie: %q", first)
	}
}
