package content

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Question struct {
	ID         string
	AskedAt    time.Time
	From       string
	Context    string
	Question   string
	Answer     string
	AnsweredAt time.Time
	FetchedAt  time.Time
	FetchedBy  string
}

func (q Question) IsAnswered() bool   { return q.Answer != "" }
func (q Question) IsFetched() bool    { return !q.FetchedAt.IsZero() }
func (q Question) IsPicked() bool     { return q.IsAnswered() && !q.IsFetched() }
func (q Question) IsAwaiting() bool   { return !q.IsAnswered() }
func (q Question) Answered() time.Time { return q.AnsweredAt }

type QuestionStore struct {
	dir string
	mu  sync.RWMutex
}

func NewQuestionStore(contentDir string) *QuestionStore {
	dataDir := filepath.Dir(contentDir)
	dir := filepath.Join(dataDir, "questions")
	_ = os.MkdirAll(dir, 0o755)
	return &QuestionStore{dir: dir}
}

func (s *QuestionStore) List() []Question {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Question
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		q, err := s.load(strings.TrimSuffix(e.Name(), ".txt"))
		if err != nil {
			continue
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].AskedAt.After(out[j].AskedAt)
	})
	return out
}

func (s *QuestionStore) Get(id string) (Question, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load(id)
}

func (s *QuestionStore) load(id string) (Question, error) {
	path := filepath.Join(s.dir, id+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return Question{}, err
	}
	return parseQuestion(string(data))
}

func parseQuestion(raw string) (Question, error) {
	q := Question{}
	parts := strings.SplitN(raw, "\n\n", 2)
	headers := parts[0]
	body := ""
	if len(parts) > 1 {
		body = parts[1]
	}
	for _, line := range strings.Split(headers, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.ToLower(k))
		v = strings.TrimSpace(v)
		switch k {
		case "id":
			q.ID = v
		case "asked_at":
			q.AskedAt = parseTimestamp(v)
		case "from":
			q.From = v
		case "context":
			q.Context = v
		case "answered_at":
			q.AnsweredAt = parseTimestamp(v)
		case "fetched_at":
			q.FetchedAt = parseTimestamp(v)
		case "fetched_by":
			q.FetchedBy = v
		}
	}
	if idx := strings.Index(body, "\n---answer---\n"); idx >= 0 {
		q.Question = strings.TrimSpace(body[:idx])
		q.Answer = strings.TrimSpace(body[idx+len("\n---answer---\n"):])
	} else {
		q.Question = strings.TrimSpace(body)
	}
	return q, nil
}

func parseTimestamp(s string) time.Time {
	layouts := []string{
		"2006-01-02 15:04 UTC",
		"2006-01-02 15:04:05 UTC",
		time.RFC3339,
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *QuestionStore) Answer(id, answer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.load(id)
	if err != nil {
		return err
	}
	q.Answer = answer
	q.AnsweredAt = time.Now().UTC()
	return s.save(q)
}

func (s *QuestionStore) MarkFetched(id, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.load(id)
	if err != nil {
		return err
	}
	q.FetchedAt = time.Now().UTC()
	q.FetchedBy = by
	return s.save(q)
}

func (s *QuestionStore) save(q Question) error {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\n", q.ID)
	if !q.AskedAt.IsZero() {
		fmt.Fprintf(&b, "asked_at: %s\n", q.AskedAt.Format("2006-01-02 15:04 UTC"))
	}
	if q.From != "" {
		fmt.Fprintf(&b, "from: %s\n", q.From)
	}
	if q.Context != "" {
		fmt.Fprintf(&b, "context: %s\n", q.Context)
	}
	if !q.AnsweredAt.IsZero() {
		fmt.Fprintf(&b, "answered_at: %s\n", q.AnsweredAt.Format("2006-01-02 15:04 UTC"))
	}
	if !q.FetchedAt.IsZero() {
		fmt.Fprintf(&b, "fetched_at: %s\n", q.FetchedAt.Format("2006-01-02 15:04 UTC"))
	}
	if q.FetchedBy != "" {
		fmt.Fprintf(&b, "fetched_by: %s\n", q.FetchedBy)
	}
	b.WriteString("\n")
	b.WriteString(q.Question)
	if q.Answer != "" {
		b.WriteString("\n\n---answer---\n")
		b.WriteString(q.Answer)
	}
	return os.WriteFile(filepath.Join(s.dir, q.ID+".txt"), []byte(b.String()), 0o644)
}
