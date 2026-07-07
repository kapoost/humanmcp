package content

import (
	"crypto/rand"
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

// RitualStatus tracks a background ritual job through its lifecycle.
type RitualStatus string

const (
	RitualPending RitualStatus = "pending"
	RitualRunning RitualStatus = "running"
	RitualDone    RitualStatus = "done"
	RitualFailed  RitualStatus = "failed"
)

// PersonaVoice is one persona's contribution to a ritual result. Recommendation
// is the persona-authored text; ModelUsed records which LLM produced it (for
// audit and per-persona cost accounting).
type PersonaVoice struct {
	Slug           string `json:"slug"`
	Recommendation string `json:"recommendation"`
	ModelUsed      string `json:"model_used,omitempty"`
}

// RitualJob is the on-disk record of a single ritual invocation. One file
// per job under /data/rituals/<id>.json.
type RitualJob struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"` // "narada", ...
	Status      RitualStatus   `json:"status"`
	Context     string         `json:"context"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	CompletedAt time.Time      `json:"completed_at,omitempty"`
	Personas    []string       `json:"personas,omitempty"` // slugs selected by the router
	Voices      []PersonaVoice `json:"voices,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// RitualStore persists ritual jobs on disk and tracks in-flight jobs so a
// running worker can update state without racing with readers.
type RitualStore struct {
	dir string
	mu  sync.Mutex
}

func NewRitualStore(contentDir string) *RitualStore {
	dataDir := filepath.Dir(contentDir)
	dir := filepath.Join(dataDir, "rituals")
	_ = os.MkdirAll(dir, 0o755)
	return &RitualStore{dir: dir}
}

// Create writes a new ritual job in pending status. ID is a random 12-hex
// prefix so a same-second replay does NOT collide — the questions bug where
// slug+minute overwrote silently informed this choice.
func (s *RitualStore) Create(typ, context string, personas []string) (RitualJob, error) {
	typ = strings.TrimSpace(strings.ToLower(typ))
	if typ == "" {
		return RitualJob{}, fmt.Errorf("ritual type required")
	}
	context = strings.TrimSpace(context)
	if context == "" {
		return RitualJob{}, fmt.Errorf("ritual context required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.uniqueID(typ)
	if err != nil {
		return RitualJob{}, err
	}
	job := RitualJob{
		ID:        id,
		Type:      typ,
		Status:    RitualPending,
		Context:   context,
		CreatedAt: time.Now().UTC(),
		Personas:  personas,
	}
	if err := s.writeLocked(job); err != nil {
		return RitualJob{}, err
	}
	return job, nil
}

// Get returns the current on-disk state of a job.
func (s *RitualStore) Get(id string) (RitualJob, error) {
	if !isSafeRitualID(id) {
		return RitualJob{}, fmt.Errorf("invalid ritual id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id)
}

// MarkRunning flips a pending job to running and stamps StartedAt.
func (s *RitualStore) MarkRunning(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.readLocked(id)
	if err != nil {
		return err
	}
	if job.Status != RitualPending {
		return fmt.Errorf("job not pending (status=%s)", job.Status)
	}
	job.Status = RitualRunning
	job.StartedAt = time.Now().UTC()
	return s.writeLocked(job)
}

// Complete stores voices and stamps CompletedAt. Idempotent-friendly: if a
// job is already done, we overwrite with the newer result to keep manual
// retries simple.
func (s *RitualStore) Complete(id string, voices []PersonaVoice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.readLocked(id)
	if err != nil {
		return err
	}
	job.Status = RitualDone
	job.CompletedAt = time.Now().UTC()
	job.Voices = voices
	job.Error = ""
	return s.writeLocked(job)
}

// Fail records the error message and moves the job to failed.
func (s *RitualStore) Fail(id, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.readLocked(id)
	if err != nil {
		return err
	}
	job.Status = RitualFailed
	job.CompletedAt = time.Now().UTC()
	job.Error = msg
	return s.writeLocked(job)
}

// ListPending returns pending jobs (used by workers on startup / poll).
func (s *RitualStore) ListPending() ([]RitualJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filterLocked(func(j RitualJob) bool { return j.Status == RitualPending })
}

// ListRecent returns jobs newest-first, capped at limit. Used for /dobranoc
// to iterate over today's narady.
func (s *RitualStore) ListRecent(limit int) ([]RitualJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.filterLocked(func(j RitualJob) bool { return true })
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (s *RitualStore) filterLocked(keep func(RitualJob) bool) ([]RitualJob, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RitualJob
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		job, err := s.readLocked(id)
		if err != nil {
			continue
		}
		if keep(job) {
			out = append(out, job)
		}
	}
	return out, nil
}

func (s *RitualStore) uniqueID(typ string) (string, error) {
	prefix := ritualPrefix(typ)
	for attempt := 0; attempt < 5; attempt++ {
		buf := make([]byte, 6)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		id := prefix + "-" + hex.EncodeToString(buf)
		if _, err := os.Stat(filepath.Join(s.dir, id+".json")); os.IsNotExist(err) {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not allocate unique ritual id")
}

func ritualPrefix(typ string) string {
	switch typ {
	case "narada":
		return "nar"
	case "dobranoc":
		return "dbn"
	default:
		if len(typ) >= 3 {
			return typ[:3]
		}
		return typ
	}
}

func isSafeRitualID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
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

func (s *RitualStore) pathFor(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *RitualStore) readLocked(id string) (RitualJob, error) {
	data, err := os.ReadFile(s.pathFor(id))
	if err != nil {
		return RitualJob{}, err
	}
	var job RitualJob
	if err := json.Unmarshal(data, &job); err != nil {
		return RitualJob{}, err
	}
	return job, nil
}

func (s *RitualStore) writeLocked(job RitualJob) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	// Write to temp then rename — a partially-written file could confuse the
	// reader in ListPending, and rename is atomic on POSIX.
	tmp := s.pathFor(job.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.pathFor(job.ID))
}
