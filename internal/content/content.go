package content

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AccessLevel string

const (
	AccessPublic  AccessLevel = "public"
	AccessMembers AccessLevel = "members"
	AccessLocked  AccessLevel = "locked"
)

type GateType string

const (
	GatePayment   GateType = "payment"
	GateChallenge GateType = "challenge"
)

// Piece is a single content item (poem, essay, note, audio, etc.)
type Piece struct {
	Slug        string      `json:"Slug"`
	Title       string      `json:"Title"`
	Type        string      `json:"Type"`
	Access      AccessLevel `json:"Access"`
	Gate        GateType    `json:"Gate"`
	Challenge   string      `json:"Challenge"`
	Answer      string      `json:"Answer"`
	PriceSats   int         `json:"PriceSats"`
	Tags        []string    `json:"Tags"`
	Published   time.Time   `json:"Published"`
	Description string      `json:"Description"`
	Body        string      `json:"Body"`
	FilePath    string      `json:"-"`
}

// Store manages all content pieces loaded from a directory of .md files.
type Store struct {
	dir    string
	pieces map[string]*Piece
}

func NewStore(dir string) *Store {
	return &Store{dir: dir, pieces: make(map[string]*Piece)}
}

func (s *Store) Load() error {
	s.pieces = make(map[string]*Piece)
	return filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		p, err := parsePiece(path)
		if err != nil {
			return nil
		}
		if p.Slug == "" {
			p.Slug = strings.TrimSuffix(filepath.Base(path), ".md")
		}
		if p.Type == "" {
			p.Type = "poem"
		}
		if p.Access == "" {
			p.Access = AccessPublic
		}
		if p.Published.IsZero() {
			p.Published = time.Now()
		}
		s.pieces[p.Slug] = p
		return nil
	})
}

// List returns all pieces sorted by published date, newest first.
// Body is included only for public content or when includeBody=true.
func (s *Store) List(includeBody bool) []*Piece {
	out := make([]*Piece, 0, len(s.pieces))
	for _, p := range s.pieces {
		cp := *p
		if !includeBody && cp.Access != AccessPublic {
			cp.Body = ""
		}
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Published.After(out[j].Published)
	})
	return out
}

// Get returns a piece. If locked and not unlocked, body is redacted.
func (s *Store) Get(slug string, unlocked bool) (*Piece, error) {
	p, ok := s.pieces[slug]
	if !ok {
		return nil, fmt.Errorf("not found: %s", slug)
	}
	if p.Access == AccessPublic || unlocked {
		return p, nil
	}
	cp := *p
	cp.Body = ""
	return &cp, nil
}

// GetForEdit returns the full piece including answer (owner only).
func (s *Store) GetForEdit(slug string) (*Piece, error) {
	p, ok := s.pieces[slug]
	if !ok {
		return nil, fmt.Errorf("not found: %s", slug)
	}
	return p, nil
}

// CheckAnswer returns true if the answer matches the challenge (case-insensitive).
func (s *Store) CheckAnswer(slug, answer string) bool {
	p, ok := s.pieces[slug]
	if !ok || p.Gate != GateChallenge {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Answer), strings.TrimSpace(answer))
}

// Save writes (or overwrites) a piece to disk as a .md file.
func (s *Store) Save(p *Piece) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	if p.Published.IsZero() {
		p.Published = time.Now()
	}
	path := filepath.Join(s.dir, p.Slug+".md")
	p.FilePath = path

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.WriteString(marshalFrontmatter(p))
	buf.WriteString("---\n\n")
	buf.WriteString(p.Body)

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return err
	}
	s.pieces[p.Slug] = p
	return nil
}

// Delete removes a piece from disk and the in-memory store.
func (s *Store) Delete(slug string) error {
	p, ok := s.pieces[slug]
	if !ok {
		return fmt.Errorf("not found: %s", slug)
	}
	if err := os.Remove(p.FilePath); err != nil {
		return err
	}
	delete(s.pieces, slug)
	return nil
}

// parsePiece reads a .md file with --- frontmatter ---
func parsePiece(path string) (*Piece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p := &Piece{FilePath: path}
	var fmLines []string
	var bodyLines []string
	inFM := false
	fmDone := false
	lineNum := 0

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++
		if lineNum == 1 && line == "---" {
			inFM = true
			continue
		}
		if inFM && line == "---" {
			inFM = false
			fmDone = true
			continue
		}
		if inFM {
			fmLines = append(fmLines, line)
		} else if fmDone {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	parseFrontmatter(fmLines, p)

	body := strings.Join(bodyLines, "\n")
	p.Body = strings.TrimPrefix(body, "\n")
	return p, nil
}
