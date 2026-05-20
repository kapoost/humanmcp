package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Listing struct {
	Slug      string    `json:"slug"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      []string  `json:"tags,omitempty"`
	Price     string    `json:"price,omitempty"`
	PriceSats string    `json:"price_sats,omitempty"`
	Status    string    `json:"status"`
	Access    string    `json:"access"`
	Published time.Time `json:"published"`
	ExpiresAt time.Time `json:"expires_at"`
	ImageRef  string    `json:"image_ref,omitempty"`
	Signature string    `json:"signature,omitempty"`
	Lang      string    `json:"lang,omitempty"`
}

type ListingStore struct {
	path string
	mu   sync.RWMutex
}

func NewListingStore(contentDir string) *ListingStore {
	dataDir := filepath.Dir(contentDir)
	return &ListingStore{path: filepath.Join(dataDir, "listings.json")}
}

func (s *ListingStore) List() []Listing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var listings []Listing
	if err := json.Unmarshal(data, &listings); err != nil {
		return nil
	}
	sort.Slice(listings, func(i, j int) bool {
		return listings[i].Published.After(listings[j].Published)
	})
	return listings
}

func (s *ListingStore) Get(slug string) (*Listing, error) {
	for _, l := range s.List() {
		if l.Slug == slug {
			return &l, nil
		}
	}
	return nil, fmt.Errorf("listing %q not found", slug)
}

func (s *ListingStore) Save(l Listing) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	listings := []Listing{}
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &listings)
	}
	replaced := false
	for i := range listings {
		if listings[i].Slug == l.Slug {
			listings[i] = l
			replaced = true
			break
		}
	}
	if !replaced {
		listings = append(listings, l)
	}
	data, err := json.MarshalIndent(listings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *ListingStore) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var listings []Listing
	if err := json.Unmarshal(data, &listings); err != nil {
		return err
	}
	kept := listings[:0]
	for _, l := range listings {
		if l.Slug != slug {
			kept = append(kept, l)
		}
	}
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0o644)
}
