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

type EventType string

const (
	EventRead       EventType = "read"
	EventList       EventType = "list"
	EventUnlock     EventType = "unlock"
	EventUnlockFail EventType = "unlock_fail"
	EventMessage    EventType = "message"
	EventComment    EventType = "comment"
	EventProfile    EventType = "profile"
	EventAccess     EventType = "access"
	EventSearch     EventType = "search"
	EventLicense    EventType = "license"
)

type CallerType string

const (
	CallerAgent   CallerType = "agent"
	CallerHuman   CallerType = "human"
	CallerUnknown CallerType = "unknown"
)

type Event struct {
	At      time.Time  `json:"at"`
	Type    EventType  `json:"type"`
	Caller  CallerType `json:"caller"`
	Slug    string     `json:"slug,omitempty"`
	UA      string     `json:"ua,omitempty"`
	From    string     `json:"from,omitempty"`
	Ref     string     `json:"ref,omitempty"`
	Country string     `json:"country,omitempty"`
	VisitorHash string `json:"vh,omitempty"`
	Query   string     `json:"query,omitempty"`
	Kind    string     `json:"kind,omitempty"`
}

type HourBucket struct {
	Hour  int `json:"hour"`
	Count int `json:"count"`
}

type Stats struct {
	// Counters
	TotalReads    int `json:"total_reads"`
	TotalSearches int `json:"total_searches"`
	TotalMessages int `json:"total_messages"`
	TotalComments int `json:"total_comments"`
	TotalUnlocks  int `json:"total_unlocks"`
	TotalInterest int `json:"total_interest"`
	TotalLicenses int `json:"total_licenses"`
	AgentCalls    int `json:"agent_calls"`
	HumanVisits   int `json:"human_visits"`
	UniqueVisitors int `json:"unique_visitors"`

	// Breakdowns
	ReadsBySlug    map[string]int `json:"reads_by_slug"`
	InterestBySlug map[string]int `json:"interest_by_slug"`
	TagReads       map[string]int `json:"tag_reads"`
	TopAgents      map[string]int `json:"top_agents"`
	TopReferrers   map[string]int `json:"top_referrers"`
	TopSearches    map[string]int `json:"top_searches"`
	Countries      map[string]int `json:"countries"`

	// Challenge funnel per slug: [checked, attempted, succeeded]
	ChallengeFunnel map[string][3]int `json:"challenge_funnel"`

	// Unlock attempts per slug — newest first. Each event has Query
	// (the answer that was tried) and Type (EventUnlock or EventUnlockFail).
	AttemptsBySlug map[string][]Event `json:"attempts_by_slug,omitempty"`

	// Hour-of-day distribution (0-23)
	HourlyReads [24]int `json:"hourly_reads"`

	// Recent events
	RecentEvents []Event `json:"recent_events"`
}

type StatStore struct {
	path    string
	tagPath string
	mu      sync.Mutex
	cache   *Cache[*Stats] // 10s TTL — dashboard doesn't need live data
}

func NewStatStore(contentDir string) *StatStore {
	base := filepath.Dir(contentDir)
	return &StatStore{
		path:    filepath.Join(base, "stats.ndjson"),
		tagPath: filepath.Join(base, "slug-tags.json"),
		cache:   NewCache[*Stats](10 * time.Second),
	}
}

func (ss *StatStore) Record(e Event) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	e.At = time.Now().UTC()
	ss.cache.Invalidate()
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(ss.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", data)
}

// UpdateSlugTags keeps a slug→tags index so stats can show tag breakdowns
func (ss *StatStore) UpdateSlugTags(slugTags map[string][]string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	data, _ := json.Marshal(slugTags)
	os.WriteFile(ss.tagPath, data, 0644)
}

func (ss *StatStore) loadSlugTags() map[string][]string {
	data, err := os.ReadFile(ss.tagPath)
	if err != nil {
		return nil
	}
	var m map[string][]string
	json.Unmarshal(data, &m)
	return m
}

func (ss *StatStore) Compute() (*Stats, error) {
	if cached, ok := ss.cache.Get(); ok {
		return cached, nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()

	s := &Stats{
		ReadsBySlug:     make(map[string]int),
		InterestBySlug:  make(map[string]int),
		TagReads:        make(map[string]int),
		TopAgents:       make(map[string]int),
		TopReferrers:    make(map[string]int),
		TopSearches:     make(map[string]int),
		Countries:       make(map[string]int),
		ChallengeFunnel: make(map[string][3]int),
		AttemptsBySlug:  make(map[string][]Event),
	}

	data, err := os.ReadFile(ss.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	slugTags := ss.loadSlugTags()
	uniqueVH := make(map[string]bool)

	var all []Event
	for _, line := range splitLines(string(data)) {
		var e Event
		if json.Unmarshal([]byte(line), &e) == nil {
			all = append(all, e)
		}
	}

	for _, e := range all {
		// caller counts
		switch e.Caller {
		case CallerAgent:
			s.AgentCalls++
		case CallerHuman:
			s.HumanVisits++
		}

		// unique visitors (hashed)
		if e.VisitorHash != "" {
			uniqueVH[e.VisitorHash] = true
		}

		// agent identity
		if e.From != "" {
			s.TopAgents[e.From]++
		}

		// country
		if e.Country != "" {
			s.Countries[e.Country]++
		}

		// referrer — strip query strings, keep domain only
		if e.Ref != "" {
			domain := cleanReferrer(e.Ref)
			if domain != "" {
				s.TopReferrers[domain]++
			}
		}

		// event type
		switch e.Type {
		case EventRead:
			s.TotalReads++
			if e.Slug != "" {
				s.ReadsBySlug[e.Slug]++
				// hour of day
				s.HourlyReads[e.At.Hour()]++
				// tag analytics
				if slugTags != nil {
					for _, tag := range slugTags[e.Slug] {
						s.TagReads[tag]++
					}
				}
			}
		case EventMessage:
			s.TotalMessages++
		case EventComment:
			s.TotalComments++
		case EventUnlock:
			s.TotalUnlocks++
			if e.Slug != "" {
				f := s.ChallengeFunnel[e.Slug]
				f[2]++
				s.ChallengeFunnel[e.Slug] = f
				if e.Query != "" {
					s.AttemptsBySlug[e.Slug] = append(s.AttemptsBySlug[e.Slug], e)
				}
			}
		case EventUnlockFail:
			if e.Slug != "" {
				f := s.ChallengeFunnel[e.Slug]
				f[1]++
				s.ChallengeFunnel[e.Slug] = f
				if e.Query != "" {
					s.AttemptsBySlug[e.Slug] = append(s.AttemptsBySlug[e.Slug], e)
				}
			}
		case EventSearch:
			// Lower-case + trim — "Niebo", "niebo ", "NIEBO" are
			// one search. Drops blanks and 1-char queries; those are
			// keystrokes, not searches.
			q := strings.ToLower(strings.TrimSpace(e.Query))
			if len(q) >= 2 {
				s.TotalSearches++
				s.TopSearches[q]++
			}
		case EventAccess:
			s.TotalInterest++
			if e.Slug != "" {
				s.InterestBySlug[e.Slug]++
				// count as funnel entry
				f := s.ChallengeFunnel[e.Slug]
				f[0]++
				s.ChallengeFunnel[e.Slug] = f
			}
		case EventLicense:
			s.TotalLicenses++
		}
	}

	s.UniqueVisitors = len(uniqueVH)

	// All events from the last 7 days (newest first), with a 500-event
	// safety cap for high-traffic windows. 7d is the right span for a
	// low-traffic personal site — 24h was often empty after a quiet
	// stretch, which forced a confusing "fall back to last 30 ever"
	// branch. With 7d the feed almost always shows real recent activity.
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	const recentCap = 500
	for i := len(all) - 1; i >= 0 && len(s.RecentEvents) < recentCap; i-- {
		if all[i].At.Before(cutoff) {
			break
		}
		s.RecentEvents = append(s.RecentEvents, all[i])
	}

	// Reverse each AttemptsBySlug list (newest first) and cap at 20 per slug
	for slug, list := range s.AttemptsBySlug {
		for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
			list[i], list[j] = list[j], list[i]
		}
		if len(list) > 20 {
			list = list[:20]
		}
		s.AttemptsBySlug[slug] = list
	}

	ss.cache.Set(s)
	return s, nil
}

// WindowStats holds counters for a single time window.
type WindowStats struct {
	Reads    int
	Visitors int
	Agents   int
	Humans   int
	Searches int
	Messages int
	Licenses int
}

// Windows is a set of rolling time buckets computed from the event log.
// 7d and 30d windows include today (overlapping), so 30d ≥ 7d ≥ today.
// Yesterday is a single-day bucket and does NOT overlap with Today.
type Windows struct {
	Today      WindowStats
	Yesterday  WindowStats
	Last7Days  WindowStats
	Last30Days WindowStats
	// DailyReads[0]=13 days ago, DailyReads[13]=today (for sparkline)
	DailyReads [14]int
}

// ComputeWindows aggregates events into rolling time windows relative to `now`.
// Bypasses the cache used by Compute() — windows depend on wall clock.
func (ss *StatStore) ComputeWindows(now time.Time) (*Windows, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	w := &Windows{}
	data, err := os.ReadFile(ss.path)
	if err != nil {
		if os.IsNotExist(err) {
			return w, nil
		}
		return nil, err
	}

	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	sevenStart := todayStart.AddDate(0, 0, -6)
	thirtyStart := todayStart.AddDate(0, 0, -29)
	fourteenStart := todayStart.AddDate(0, 0, -13)

	vToday := map[string]bool{}
	vYday := map[string]bool{}
	v7 := map[string]bool{}
	v30 := map[string]bool{}

	bump := func(ws *WindowStats, e Event) {
		if e.Type == EventRead {
			ws.Reads++
		}
		switch e.Caller {
		case CallerAgent:
			ws.Agents++
		case CallerHuman:
			ws.Humans++
		}
		if e.Type == EventMessage {
			ws.Messages++
		}
		if e.Type == EventSearch {
			ws.Searches++
		}
		if e.Type == EventLicense {
			ws.Licenses++
		}
	}

	for _, line := range splitLines(string(data)) {
		var e Event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		ts := e.At.In(loc)

		inToday := !ts.Before(todayStart)
		inYday := !ts.Before(yesterdayStart) && ts.Before(todayStart)
		in7 := !ts.Before(sevenStart)
		in30 := !ts.Before(thirtyStart)

		if inToday {
			bump(&w.Today, e)
			if e.VisitorHash != "" {
				vToday[e.VisitorHash] = true
			}
		}
		if inYday {
			bump(&w.Yesterday, e)
			if e.VisitorHash != "" {
				vYday[e.VisitorHash] = true
			}
		}
		if in7 {
			bump(&w.Last7Days, e)
			if e.VisitorHash != "" {
				v7[e.VisitorHash] = true
			}
		}
		if in30 {
			bump(&w.Last30Days, e)
			if e.VisitorHash != "" {
				v30[e.VisitorHash] = true
			}
		}

		if e.Type == EventRead && !ts.Before(fourteenStart) {
			dayOffset := int(ts.Sub(fourteenStart) / (24 * time.Hour))
			if dayOffset >= 0 && dayOffset < 14 {
				w.DailyReads[dayOffset]++
			}
		}
	}

	w.Today.Visitors = len(vToday)
	w.Yesterday.Visitors = len(vYday)
	w.Last7Days.Visitors = len(v7)
	w.Last30Days.Visitors = len(v30)
	return w, nil
}

// TopN returns the top N entries from a map by value
func TopN(m map[string]int, n int) []struct{ Key string; Val int } {
	type kv struct{ Key string; Val int }
	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Val > sorted[j].Val })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	result := make([]struct{ Key string; Val int }, len(sorted))
	for i, kv := range sorted {
		result[i] = struct{ Key string; Val int }{kv.Key, kv.Val}
	}
	return result
}

func cleanReferrer(ref string) string {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	if idx := strings.Index(ref, "/"); idx > 0 {
		ref = ref[:idx]
	}
	if idx := strings.Index(ref, "?"); idx > 0 {
		ref = ref[:idx]
	}
	// Skip self-referrals
	if strings.Contains(ref, "fly.dev") || strings.Contains(ref, "localhost") {
		return ""
	}
	return ref
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func CallerFromUA(ua string) CallerType {
	if ua == "" {
		return CallerUnknown
	}
	// Look at up to 400 chars — many crawlers (Googlebot, AppleBot, AhrefsBot)
	// place their identifier in the "(compatible; ...)" suffix past 120 chars
	// of a Mozilla-spoofed prefix.
	lower := strings.ToLower(ua[:min(len(ua), 400)])
	for _, kw := range []string{
		"claude", "gpt", "openai", "anthropic", "llm", "agent", "bot", "curl",
		"python", "go-http", "okhttp", "axios", "mcp", "langchain",
		"googlebot", "bingbot", "applebot", "yandex", "baidu", "slurp",
		"duckduckbot", "ahrefs", "semrush", "mj12bot", "facebookexternalhit",
		"twitterbot", "linkedinbot", "whatsapp", "discordbot", "telegram",
		"crawler", "spider", "scraper", "fetch", "http_client", "lighthouse",
	} {
		if strings.Contains(lower, kw) {
			return CallerAgent
		}
	}
	for _, kw := range []string{"mozilla", "chrome", "safari", "firefox", "webkit"} {
		if strings.Contains(lower, kw) {
			return CallerHuman
		}
	}
	return CallerUnknown
}

// VisitorHash creates a non-reversible daily visitor token from IP
func VisitorHash(ip, date string) string {
	// Simple hash — not storing IP, just a daily unique token
	h := 0
	for _, c := range ip + "|" + date {
		h = h*31 + int(c)
	}
	if h < 0 { h = -h }
	return fmt.Sprintf("%x", h)
}
