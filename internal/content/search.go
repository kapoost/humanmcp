package content

import (
	"sort"
	"strings"
)

// FoldDiacritics sprowadza polskie (i czesko-podobne) znaki do ASCII, żeby
// „człowieczeństwa" i „czlowieczenstwa" trafiały w to samo. Agent pisze
// zapytanie z klawiatury, o której nic nie wiemy.
func FoldDiacritics(s string) string {
	return diacriticFolder.Replace(s)
}

var diacriticFolder = strings.NewReplacer(
	"ę", "e", "ł", "l", "ą", "a", "ć", "c", "ś", "s",
	"ń", "n", "ó", "o", "ź", "z", "ż", "z",
	"Ę", "E", "Ł", "L", "Ą", "A", "Ć", "C", "Ś", "S",
	"Ń", "N", "Ó", "O", "Ź", "Z", "Ż", "Z",
	"ě", "e", "č", "c", "š", "s", "ř", "r", "ž", "z",
)

// SearchHit to jedno trafienie. Where mówi, GDZIE trafiło — agent inaczej
// traktuje zgodność tytułu niż przypadkowe słowo w środku wiersza.
type SearchHit struct {
	Slug    string
	Title   string
	Type    string
	Access  AccessLevel
	Where   string // "title" | "tags" | "body"
	Snippet string
	Score   int
}

const (
	scoreTitle = 100
	scoreTags  = 50
	scoreBody  = 10
)

func normalizeForSearch(s string) string {
	return strings.ToLower(FoldDiacritics(s))
}

// Search przeszukuje tytuły, znaczniki i treść po wszystkich podanych
// słowach (koniunkcja — wynik musi zawierać każde z nich).
//
// Powstało, bo serwer nie miał ŻADNEGO wyszukiwania po treści publicznych
// utworów: `list_content` zwraca same slugi i tytuły, `read_content` jeden
// utwór naraz. Agent szukający „wiersza o miłości z metaforami
// matematycznymi" musiał pobrać wszystko albo zapytać człowieka — i pytał.
// We wrześniu 2026 trzy z ośmiu oczekujących pytań były właśnie prośbami
// o wyszukanie.
//
// DOSTĘP: treść utworu zablokowanego nie jest przeszukiwana ani cytowana.
// Taki utwór może trafić do wyników wyłącznie przez tytuł lub znacznik,
// z Access="locked", żeby agent wiedział, że ma sięgnąć po request_access.
func (s *Store) Search(query string, limit int) []SearchHit {
	terms := strings.Fields(normalizeForSearch(query))
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	var hits []SearchHit
	for _, p := range s.List(true) {
		title := normalizeForSearch(p.Title)
		tags := normalizeForSearch(strings.Join(p.Tags, " "))
		desc := normalizeForSearch(p.Description)

		body := ""
		if p.Access == AccessPublic {
			body = normalizeForSearch(p.Body)
		}

		score, where := 0, ""
		matchedAll := true
		for _, t := range terms {
			switch {
			case strings.Contains(title, t):
				score += scoreTitle
				if where == "" {
					where = "title"
				}
			case strings.Contains(tags, t), strings.Contains(desc, t):
				score += scoreTags
				if where == "" {
					where = "tags"
				}
			case body != "" && strings.Contains(body, t):
				score += scoreBody
				if where == "" {
					where = "body"
				}
			default:
				matchedAll = false
			}
			if !matchedAll {
				break
			}
		}
		if !matchedAll {
			continue
		}

		hit := SearchHit{
			Slug: p.Slug, Title: p.Title, Type: p.Type,
			Access: p.Access, Where: where, Score: score,
		}
		if where == "body" {
			hit.Snippet = snippetAround(p.Body, terms[0])
		}
		hits = append(hits, hit)
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Slug < hits[j].Slug
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// snippetAround wycina fragment wokół pierwszego trafienia. Zwraca tekst
// ORYGINALNY (z diakrytykami) — składanie znaków służy dopasowaniu, nie
// prezentacji.
func snippetAround(body, term string) string {
	const window = 60
	folded := normalizeForSearch(body)
	idx := strings.Index(folded, term)
	if idx < 0 {
		return ""
	}
	runes := []rune(body)
	// Indeks liczony na złożonym tekście; składanie jest 1:1 na runę, więc
	// pozycja runy się zgadza.
	start := len([]rune(folded[:idx]))
	from := start - window
	if from < 0 {
		from = 0
	}
	to := start + len([]rune(term)) + window
	if to > len(runes) {
		to = len(runes)
	}
	out := strings.Join(strings.Fields(string(runes[from:to])), " ")
	if from > 0 {
		out = "…" + out
	}
	if to < len(runes) {
		out += "…"
	}
	return out
}
