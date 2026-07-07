package content

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PersonaJournalEntry is one reflection a persona wrote after a ritual where
// their recommendation turned out wrong (rollback / fix commit within the
// tracking window). Journals only capture mistakes — success needs no
// self-correction.
type PersonaJournalEntry struct {
	At             time.Time `json:"at"`
	NaradaID       string    `json:"narada_id"`
	Context        string    `json:"context"`
	Recommendation string    `json:"recommendation"`
	ErrorSignal    string    `json:"error_signal"`
	Reflection     string    `json:"reflection"`
}

// PersonaJournalStore keeps one markdown file per persona slug under
// /data/persona-journals/. Append-only. A rewrite is only allowed as part of
// a full rewrite (test cleanup or admin ops).
type PersonaJournalStore struct {
	dir string
	mu  sync.Mutex
}

func NewPersonaJournalStore(contentDir string) *PersonaJournalStore {
	dataDir := filepath.Dir(contentDir)
	dir := filepath.Join(dataDir, "persona-journals")
	_ = os.MkdirAll(dir, 0o755)
	return &PersonaJournalStore{dir: dir}
}

// Append writes one entry to the persona's journal. Concurrent-safe.
// Rejects entries with empty slug, narada_id, or reflection — those three
// are the minimum useful record.
func (s *PersonaJournalStore) Append(slug string, e PersonaJournalEntry) error {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return fmt.Errorf("persona slug required")
	}
	if !isSafeSlug(slug) {
		return fmt.Errorf("invalid persona slug")
	}
	if strings.TrimSpace(e.NaradaID) == "" {
		return fmt.Errorf("narada_id required")
	}
	if strings.TrimSpace(e.Reflection) == "" {
		return fmt.Errorf("reflection required")
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathFor(slug)
	existing, _ := os.ReadFile(path)

	var buf strings.Builder
	if len(existing) == 0 {
		fmt.Fprintf(&buf, "# Dziennik persony: %s\n\n", slug)
		buf.WriteString("_Wpisy dopisywane po naradach, w których rekomendacja tej persony została odwrócona (rollback / poprawka)._\n\n")
	} else {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	renderEntry(&buf, e)
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// List returns all entries for a persona, newest-first. Empty slice when
// the persona has no journal yet.
func (s *PersonaJournalStore) List(slug string) ([]PersonaJournalEntry, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !isSafeSlug(slug) {
		return nil, fmt.Errorf("invalid persona slug")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.pathFor(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	entries := parseJournal(string(data))
	sort.Slice(entries, func(i, j int) bool { return entries[i].At.After(entries[j].At) })
	return entries, nil
}

// Render returns the raw markdown content for a persona's journal, or the
// empty string when there is no journal yet. Owner-facing display uses this.
func (s *PersonaJournalStore) Render(slug string) (string, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !isSafeSlug(slug) {
		return "", fmt.Errorf("invalid persona slug")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.pathFor(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (s *PersonaJournalStore) pathFor(slug string) string {
	return filepath.Join(s.dir, slug+".md")
}

// isSafeSlug allows only [a-z0-9-] to keep path traversal impossible.
func isSafeSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func renderEntry(buf *strings.Builder, e PersonaJournalEntry) {
	fmt.Fprintf(buf, "## %s · %s\n\n", e.At.Format("2006-01-02 15:04 UTC"), e.NaradaID)
	if s := strings.TrimSpace(e.Context); s != "" {
		fmt.Fprintf(buf, "**Kontekst:** %s\n\n", s)
	}
	if s := strings.TrimSpace(e.Recommendation); s != "" {
		fmt.Fprintf(buf, "**Rekomendacja:** %s\n\n", s)
	}
	if s := strings.TrimSpace(e.ErrorSignal); s != "" {
		fmt.Fprintf(buf, "**Sygnał błędu:** %s\n\n", s)
	}
	buf.WriteString("**Wniosek:**\n\n")
	for _, line := range strings.Split(strings.TrimSpace(e.Reflection), "\n") {
		fmt.Fprintf(buf, "> %s\n", line)
	}
	buf.WriteString("\n")
}

var entryHeaderRE = regexp.MustCompile(`(?m)^## (\S+ \S+ \S+) · (\S+)\s*$`)

func parseJournal(raw string) []PersonaJournalEntry {
	locs := entryHeaderRE.FindAllStringSubmatchIndex(raw, -1)
	if len(locs) == 0 {
		return nil
	}
	var out []PersonaJournalEntry
	for i, loc := range locs {
		start := loc[0]
		end := len(raw)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := raw[start:end]
		e := parseEntry(block)
		if e.NaradaID != "" {
			out = append(out, e)
		}
	}
	return out
}

func parseEntry(block string) PersonaJournalEntry {
	e := PersonaJournalEntry{}
	m := entryHeaderRE.FindStringSubmatch(block)
	if len(m) < 3 {
		return e
	}
	e.At, _ = time.Parse("2006-01-02 15:04 UTC", m[1])
	e.NaradaID = m[2]

	var reflection []string
	inReflection := false
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "**Kontekst:**"):
			e.Context = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Kontekst:**"))
			inReflection = false
		case strings.HasPrefix(trimmed, "**Rekomendacja:**"):
			e.Recommendation = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Rekomendacja:**"))
			inReflection = false
		case strings.HasPrefix(trimmed, "**Sygnał błędu:**"):
			e.ErrorSignal = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Sygnał błędu:**"))
			inReflection = false
		case strings.HasPrefix(trimmed, "**Wniosek:**"):
			inReflection = true
		case inReflection && strings.HasPrefix(trimmed, ">"):
			reflection = append(reflection, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
		}
	}
	e.Reflection = strings.TrimSpace(strings.Join(reflection, "\n"))
	return e
}
