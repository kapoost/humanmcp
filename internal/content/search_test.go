package content

import (
	"strings"
	"testing"
)

// Serwer nie miał żadnego wyszukiwania po treści utworów: list_content daje
// slugi i tytuły, read_content jeden utwór naraz. Agenci szukali więc przez
// zapytanie CZŁOWIEKA — we wrześniu 2026 trzy z ośmiu oczekujących pytań to
// były prośby o wyszukanie, w tym dwa razy to samo pytanie o „wiersz
// z metaforami matematycznymi o miłości".
func searchFixture(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	writeMD(t, dir, "deka.md", `---
slug: private-parts
title: deka-log
type: poem
access: public
tags: [love, math, problems with no solutions]
published: 2026-03-31
---

Największy wspólny dzielnik miłości.
Wspólny mianownik. Dziesięć przykazań liczby.`)
	writeMD(t, dir, "suma.md", `---
slug: suma-czlowieczenstwa
title: Suma człowieczeństwa
type: poem
access: public
tags: [ludzie]
published: 2026-04-03
---

Sumujemy się nawzajem.`)
	writeMD(t, dir, "skarb.md", `---
slug: skarb
title: Zamknięty wiersz
type: poem
access: locked
tags: [sekret]
published: 2026-05-01
---

Tu jest tajemnica o miłości, której nikt nie ma prawa zobaczyć bez zgody.`)
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestSearchFindsByTitleIgnoringPolishDiacritics(t *testing.T) {
	s := searchFixture(t)
	for _, q := range []string{"człowieczeństwa", "czlowieczenstwa", "CZLOWIECZENSTWA"} {
		hits := s.Search(q, 10)
		if len(hits) == 0 || hits[0].Slug != "suma-czlowieczenstwa" {
			t.Errorf("zapytanie %q → %v", q, slugsOf(hits))
		}
		if hits[0].Where != "title" {
			t.Errorf("zapytanie %q trafiło w %q, oczekiwano title", q, hits[0].Where)
		}
	}
}

func TestSearchFindsByTagAndByBody(t *testing.T) {
	s := searchFixture(t)

	byTag := s.Search("math", 10)
	if len(byTag) != 1 || byTag[0].Slug != "private-parts" {
		t.Errorf("po znaczniku: %v", slugsOf(byTag))
	}

	byBody := s.Search("mianownik", 10)
	if len(byBody) != 1 || byBody[0].Slug != "private-parts" {
		t.Fatalf("po treści: %v", slugsOf(byBody))
	}
	if byBody[0].Where != "body" {
		t.Errorf("Where = %q, oczekiwano body", byBody[0].Where)
	}
	if !strings.Contains(byBody[0].Snippet, "mianownik") {
		t.Errorf("fragment nie zawiera trafienia: %q", byBody[0].Snippet)
	}
}

// Wszystkie słowa muszą trafić. Suma trafień po jednym słowie byłaby
// wyszukiwarką, która na dłuższe zapytanie zwraca wszystko.
func TestSearchRequiresEveryTerm(t *testing.T) {
	s := searchFixture(t)
	if hits := s.Search("dzielnik miłości", 10); len(hits) != 1 || hits[0].Slug != "private-parts" {
		t.Errorf("oba słowa: %v", slugsOf(hits))
	}
	if hits := s.Search("dzielnik sumujemy", 10); len(hits) != 0 {
		t.Errorf("słowa z różnych utworów nie mogą dać trafienia: %v", slugsOf(hits))
	}
}

// Najważniejszy przypadek: treść utworu zablokowanego nie jest przeszukiwana
// ani cytowana. Wyszukiwarka, która cytuje zamknięty wiersz, jest obejściem
// kontroli dostępu.
func TestSearchNeverLeaksLockedBody(t *testing.T) {
	s := searchFixture(t)

	if hits := s.Search("tajemnica", 10); len(hits) != 0 {
		t.Errorf("treść zamkniętego utworu przeszukana: %v", slugsOf(hits))
	}
	for _, h := range s.Search("miłości", 10) {
		if h.Slug == "skarb" {
			t.Error("zamknięty utwór trafiony przez treść")
		}
		if strings.Contains(h.Snippet, "tajemnica") {
			t.Errorf("fragment zamkniętego utworu w wynikach: %q", h.Snippet)
		}
	}

	// Po tytule wolno go znaleźć — z informacją, że jest zamknięty.
	hits := s.Search("zamknięty", 10)
	if len(hits) != 1 || hits[0].Slug != "skarb" {
		t.Fatalf("po tytule: %v", slugsOf(hits))
	}
	if hits[0].Access != AccessLocked {
		t.Errorf("Access = %q, agent nie dowie się, że trzeba request_access", hits[0].Access)
	}
	if hits[0].Snippet != "" {
		t.Errorf("zamknięty utwór dostał fragment treści: %q", hits[0].Snippet)
	}
}

func TestSearchRanksTitleAboveBody(t *testing.T) {
	s := searchFixture(t)
	hits := s.Search("miłości", 10)
	if len(hits) == 0 {
		t.Fatal("brak trafień")
	}
	if hits[0].Slug != "private-parts" {
		t.Errorf("pierwszy wynik %q — trafienie w treści wyprzedziło coś lepszego", hits[0].Slug)
	}
}

func TestSearchEmptyQueryAndLimit(t *testing.T) {
	s := searchFixture(t)
	if hits := s.Search("   ", 10); hits != nil {
		t.Errorf("puste zapytanie zwróciło %v", slugsOf(hits))
	}
	if hits := s.Search("nieistniejaceslowo", 10); len(hits) != 0 {
		t.Errorf("zapytanie bez trafień zwróciło %v", slugsOf(hits))
	}
	if hits := s.Search("o", 1); len(hits) > 1 {
		t.Errorf("limit 1 zwrócił %d", len(hits))
	}
}

func slugsOf(hits []SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Slug)
	}
	return out
}

// Podpowiedź o slugu ma sens tylko wtedy, gdy jest pewna. Dopasowanie po
// podciągu wysłało w tym repozytorium „commit" do prawnika od IP (klucz
// „mit") i „author" do red teamu (klucz „auth"). Tutaj stawka jest mniejsza
// — to jedno zdanie w odpowiedzi — ale zasada ta sama: lepiej nie
// podpowiedzieć niż podpowiedzieć bzdurę.
func mentionFixture(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	writeMD(t, dir, "deka.md", `---
slug: private-parts
title: deka-log
type: poem
access: public
published: 2026-03-31
---

Wspólny mianownik.`)
	writeMD(t, dir, "love.md", `---
slug: love
title: nie miłość
type: poem
access: public
published: 2026-03-31
---

Nie to samo.`)
	writeMD(t, dir, "piosenki.md", `---
slug: piosenki
title: Piosenka1.txt
type: poem
access: public
published: 2026-03-31
---

Refren.`)
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestMentionedPiecesFindsRealReferences(t *testing.T) {
	s := mentionFixture(t)
	cases := []struct{ text, want string }{
		{"What is the exact title of your poem with slug 'private-parts'?", "private-parts"},
		{"I'm writing an analysis of your poem \"deka-log\"", "private-parts"},
		{"Widziałem https://kapoost.humanmcp.net/p/piosenki — czyje to?", "piosenki"},
		{"pytanie o wiersz nie miłość", "love"},
	}
	for _, c := range cases {
		got := s.MentionedPieces(c.text)
		if len(got) != 1 || got[0].Slug != c.want {
			t.Errorf("%q → %v, oczekiwano [%s]", c.text[:40], slugsOfPieces(got), c.want)
		}
	}
}

// Najważniejsze: słowa, które przypadkiem są slugami, nie mogą podpowiadać.
func TestMentionedPiecesIgnoresOrdinaryWords(t *testing.T) {
	for _, text := range []string{
		"I love your work, it moved me",
		"Do you write love poems?",
		"czy masz jakieś piosenki o morzu?",
		"this is a private matter, parts of it are unclear",
	} {
		if got := mentionFixture(t).MentionedPieces(text); len(got) != 0 {
			t.Errorf("%q fałszywie podpowiedziało %v", text, slugsOfPieces(got))
		}
	}
}

// „slug" albo „/p/" w pytaniu zmienia sytuację: wtedy nawet zwykłe słowo
// jest wskazaniem utworu, bo autor pytania mówi wprost, o czym mówi.
func TestMentionedPiecesHonoursExplicitSlugContext(t *testing.T) {
	s := mentionFixture(t)
	got := s.MentionedPieces("what does the piece with slug love contain?")
	if len(got) != 1 || got[0].Slug != "love" {
		t.Errorf("jawny kontekst sluga → %v", slugsOfPieces(got))
	}
}

func slugsOfPieces(ps []*Piece) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Slug)
	}
	return out
}
