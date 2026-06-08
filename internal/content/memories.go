package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is a small text artifact stored per-session-code so an agent can
// pick up where it left off across conversations.
type Memory struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`              // session code that owns this memory
	From      string    `json:"from,omitempty"`    // optional agent identity
	Tags      []string  `json:"tags,omitempty"`    // optional tags for grouping
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	// MaxMemoryBytes — per-record cap (Ghost's threat model — storage
	// exhaustion via large blobs).
	MaxMemoryBytes = 8 * 1024
	// MaxMemoriesPerCode — per-code count cap.
	MaxMemoriesPerCode = 10000
)

// MemoryStore persists memories grouped by session code. Each code gets its
// own JSON file under /data/memories/ — keeps codes mutually invisible and
// makes purges trivial.
type MemoryStore struct {
	dir string
	mu  sync.RWMutex
}

func NewMemoryStore(contentDir string) *MemoryStore {
	dataDir := filepath.Dir(contentDir)
	dir := filepath.Join(dataDir, "memories")
	_ = os.MkdirAll(dir, 0o755)
	return &MemoryStore{dir: dir}
}

// Save persists a new Memory under the given code. Rejects oversize bodies
// (>8KB) and refuses if the code already has 10000 records on file.
func (s *MemoryStore) Save(code, from, body string, tags []string) (Memory, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Memory{}, fmt.Errorf("memory body required")
	}
	if len(body) > MaxMemoryBytes {
		return Memory{}, fmt.Errorf("memory body exceeds %d bytes (got %d)", MaxMemoryBytes, len(body))
	}
	if strings.TrimSpace(code) == "" {
		return Memory{}, fmt.Errorf("session code required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	mems, _ := s.loadLocked(code)
	if len(mems) >= MaxMemoriesPerCode {
		return Memory{}, fmt.Errorf("memory limit reached for this code (%d records)", MaxMemoriesPerCode)
	}

	m := Memory{
		ID:        generateMemoryID(code, body),
		Code:      code,
		From:      strings.TrimSpace(from),
		Tags:      cleanTags(tags),
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	mems = append(mems, m)
	if err := s.saveLocked(code, mems); err != nil {
		return Memory{}, err
	}
	return m, nil
}

// Recall returns memories for the given code, optionally filtered by a
// case-insensitive substring query. Newest first.
func (s *MemoryStore) Recall(code, query string, limit int) ([]Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mems, err := s.loadLocked(code)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if q := strings.ToLower(strings.TrimSpace(query)); q != "" {
		filtered := mems[:0:0]
		for _, m := range mems {
			hay := strings.ToLower(m.Body + " " + strings.Join(m.Tags, " "))
			if strings.Contains(hay, q) {
				filtered = append(filtered, m)
			}
		}
		mems = filtered
	}
	sort.Slice(mems, func(i, j int) bool {
		return mems[i].CreatedAt.After(mems[j].CreatedAt)
	})
	if len(mems) > limit {
		mems = mems[:limit]
	}
	return mems, nil
}

// Delete removes a single memory by id (within a code).
func (s *MemoryStore) Delete(code, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mems, err := s.loadLocked(code)
	if err != nil {
		return err
	}
	kept := mems[:0]
	for _, m := range mems {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	return s.saveLocked(code, kept)
}

func (s *MemoryStore) loadLocked(code string) ([]Memory, error) {
	path := filepath.Join(s.dir, codeFilename(code))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var mems []Memory
	if err := json.Unmarshal(data, &mems); err != nil {
		return nil, err
	}
	return mems, nil
}

func (s *MemoryStore) saveLocked(code string, mems []Memory) error {
	path := filepath.Join(s.dir, codeFilename(code))
	data, err := json.MarshalIndent(mems, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func codeFilename(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:8]) + ".json"
}

func generateMemoryID(code, body string) string {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write([]byte{0})
	h.Write([]byte(body))
	h.Write([]byte{0})
	fmt.Fprintf(h, "%d", time.Now().UnixNano())
	return "mem-" + hex.EncodeToString(h.Sum(nil)[:8])
}

func cleanTags(in []string) []string {
	out := in[:0]
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
