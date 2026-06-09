package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CollectionItem is one entry in kapoost's archive of works he OWNS but
// did not create — paintings, drawings, photographs, prints, etc. The
// crucial difference from a Piece is: no Ed25519 signature, no OpenTimestamps,
// no License field. The IP belongs to the original creator. What humanmcp
// stores here is the custody record + the document dossier that goes with it.
type CollectionItem struct {
	Slug             string    `json:"slug"`
	Title            string    `json:"title"`
	OriginalCreator  string    `json:"original_creator"`
	Medium           string    `json:"medium,omitempty"`
	Year             int       `json:"year,omitempty"`
	Dimensions       string    `json:"dimensions,omitempty"`
	AcquiredAt       time.Time `json:"acquired_at,omitempty"`
	AcquiredFrom     string    `json:"acquired_from,omitempty"`
	AcquisitionPrice string    `json:"acquisition_price,omitempty"`
	Notes            string    `json:"notes,omitempty"`
	ImageRef         string    `json:"image_ref,omitempty"`
	Access           string    `json:"access"` // private (default) | members | public
	AddedAt          time.Time `json:"added_at"`
}

// CollectionStore persists all items in a single JSON file. Schema is
// small enough that per-item files would be over-engineering at this scale.
type CollectionStore struct {
	path string
	mu   sync.RWMutex
}

func NewCollectionStore(contentDir string) *CollectionStore {
	dataDir := filepath.Dir(contentDir)
	return &CollectionStore{path: filepath.Join(dataDir, "collection.json")}
}

// List returns every item sorted by AddedAt desc. Caller filters by Access.
func (s *CollectionStore) List() []CollectionItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var items []CollectionItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].AddedAt.After(items[j].AddedAt)
	})
	return items
}

// PublicList returns items with Access == "public" — what anonymous
// visitors see.
func (s *CollectionStore) PublicList() []CollectionItem {
	all := s.List()
	out := all[:0:0]
	for _, it := range all {
		if it.Access == "public" {
			out = append(out, it)
		}
	}
	return out
}

func (s *CollectionStore) Get(slug string) (*CollectionItem, error) {
	for _, it := range s.List() {
		if it.Slug == slug {
			return &it, nil
		}
	}
	return nil, fmt.Errorf("collection item %q not found", slug)
}

// Save adds a new item or replaces an existing one with the same slug.
func (s *CollectionStore) Save(item CollectionItem) (CollectionItem, error) {
	if strings.TrimSpace(item.Slug) == "" {
		return CollectionItem{}, fmt.Errorf("slug required")
	}
	if strings.TrimSpace(item.Title) == "" {
		return CollectionItem{}, fmt.Errorf("title required")
	}
	if strings.TrimSpace(item.OriginalCreator) == "" {
		return CollectionItem{}, fmt.Errorf("original_creator required — collection items belong to someone else")
	}
	if item.Access == "" {
		item.Access = "private"
	}
	if item.AddedAt.IsZero() {
		item.AddedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	items, _ := s.loadLocked()
	replaced := false
	for i := range items {
		if items[i].Slug == item.Slug {
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	if err := s.saveLocked(items); err != nil {
		return CollectionItem{}, err
	}
	return item, nil
}

func (s *CollectionStore) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	kept := items[:0]
	for _, it := range items {
		if it.Slug != slug {
			kept = append(kept, it)
		}
	}
	return s.saveLocked(kept)
}

func (s *CollectionStore) loadLocked() ([]CollectionItem, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []CollectionItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *CollectionStore) saveLocked(items []CollectionItem) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
