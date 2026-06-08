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

// Subscription captures a listings-notification opt-in. Channel = "webhook"
// means push to CallbackURL; "mcp" means pull via the MCP transport (the
// channel exists for marketing — implementation lives in the MCP layer).
//
// Filters narrow which new listings trigger a notification. Empty filters
// match everything.
type Subscription struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	Channel     string    `json:"channel"`                 // webhook | mcp
	CallbackURL string    `json:"callback_url,omitempty"`  // webhook only
	FilterTypes []string  `json:"filter_types,omitempty"`  // sell/buy/offer/request/trade
	FilterTags  []string  `json:"filter_tags,omitempty"`   // tag names without #
}

type SubscriptionStore struct {
	path string
	mu   sync.RWMutex
}

func NewSubscriptionStore(contentDir string) *SubscriptionStore {
	dataDir := filepath.Dir(contentDir)
	return &SubscriptionStore{path: filepath.Join(dataDir, "subscriptions.json")}
}

func (s *SubscriptionStore) List() []Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil
	}
	sort.Slice(subs, func(i, j int) bool {
		return subs[i].CreatedAt.After(subs[j].CreatedAt)
	})
	return subs
}

// Save appends a new subscription (or replaces an existing one with the
// same ID). Returns the persisted record.
func (s *SubscriptionStore) Save(sub Subscription) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = time.Now().UTC()
	}
	if sub.ID == "" {
		sub.ID = generateSubscriptionID(sub)
	}
	if err := validateSubscription(&sub); err != nil {
		return Subscription{}, err
	}

	subs := []Subscription{}
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &subs)
	}
	replaced := false
	for i := range subs {
		if subs[i].ID == sub.ID {
			subs[i] = sub
			replaced = true
			break
		}
	}
	if !replaced {
		subs = append(subs, sub)
	}
	out, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return Subscription{}, err
	}
	if err := os.WriteFile(s.path, out, 0o644); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

func (s *SubscriptionStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return err
	}
	kept := subs[:0]
	for _, x := range subs {
		if x.ID != id {
			kept = append(kept, x)
		}
	}
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0o644)
}

func validateSubscription(s *Subscription) error {
	switch s.Channel {
	case "webhook":
		if s.CallbackURL == "" {
			return fmt.Errorf("webhook channel requires callback_url")
		}
	case "mcp":
		// ok — no URL needed
	default:
		return fmt.Errorf("channel must be 'webhook' or 'mcp', got %q", s.Channel)
	}
	return nil
}

// generateSubscriptionID derives a deterministic-ish ID from channel + URL
// so a re-submit of the same opt-in is idempotent (Save replaces in place).
func generateSubscriptionID(s Subscription) string {
	h := sha256.New()
	h.Write([]byte(s.Channel))
	h.Write([]byte{0})
	h.Write([]byte(s.CallbackURL))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(s.FilterTypes, ",")))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(s.FilterTags, ",")))
	return "sub-" + hex.EncodeToString(h.Sum(nil)[:8])
}
