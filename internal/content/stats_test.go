package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStatStore(t *testing.T) *StatStore {
	t.Helper()
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	return NewStatStore(contentDir)
}

func TestStatStoreRecord(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "poem-1"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "poem-1"})
	ss.Record(Event{Type: EventMessage, Caller: CallerHuman})

	stats, err := ss.Compute()
	if err != nil { t.Fatalf("Compute: %v", err) }
	if stats.TotalReads != 2 { t.Errorf("reads: got %d", stats.TotalReads) }
	if stats.TotalMessages != 1 { t.Errorf("messages: got %d", stats.TotalMessages) }
	if stats.AgentCalls != 1 { t.Errorf("agent calls: got %d", stats.AgentCalls) }
	if stats.HumanVisits != 2 { t.Errorf("human visits: got %d, want 2 (read+message)", stats.HumanVisits) }
}

func TestStatStoreReadsBySlug(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "poem-a"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "poem-a"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "poem-b"})

	stats, _ := ss.Compute()
	if stats.ReadsBySlug["poem-a"] != 2 { t.Errorf("poem-a reads: %d", stats.ReadsBySlug["poem-a"]) }
	if stats.ReadsBySlug["poem-b"] != 1 { t.Errorf("poem-b reads: %d", stats.ReadsBySlug["poem-b"]) }
}

func TestStatStoreChallengeFunnel(t *testing.T) {
	ss := newTestStatStore(t)
	// 3 check the gate
	ss.Record(Event{Type: EventAccess, Caller: CallerAgent, Slug: "locked-poem"})
	ss.Record(Event{Type: EventAccess, Caller: CallerAgent, Slug: "locked-poem"})
	ss.Record(Event{Type: EventAccess, Caller: CallerHuman, Slug: "locked-poem"})
	// 2 try
	ss.Record(Event{Type: EventUnlockFail, Caller: CallerAgent, Slug: "locked-poem"})
	ss.Record(Event{Type: EventUnlockFail, Caller: CallerHuman, Slug: "locked-poem"})
	// 1 succeeds
	ss.Record(Event{Type: EventUnlock, Caller: CallerHuman, Slug: "locked-poem"})

	stats, _ := ss.Compute()
	f := stats.ChallengeFunnel["locked-poem"]
	if f[0] != 3 { t.Errorf("funnel checked: got %d, want 3", f[0]) }
	if f[1] != 2 { t.Errorf("funnel tried: got %d, want 2", f[1]) }
	if f[2] != 1 { t.Errorf("funnel unlocked: got %d, want 1", f[2]) }
}

func TestStatStoreHourlyReads(t *testing.T) {
	ss := newTestStatStore(t)
	// Record events and verify hour bucket populated
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "p"})

	stats, _ := ss.Compute()
	total := 0
	for _, v := range stats.HourlyReads {
		total += v
	}
	if total != 2 { t.Errorf("hourly total: got %d, want 2", total) }
}

func TestStatStoreTagReads(t *testing.T) {
	ss := newTestStatStore(t)

	// Set up slug-tag index
	ss.UpdateSlugTags(map[string][]string{
		"sea-poem":  {"sea", "sailing"},
		"code-poem": {"code", "sea"},
	})

	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "sea-poem"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "sea-poem"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "code-poem"})

	stats, _ := ss.Compute()
	if stats.TagReads["sea"] != 3 { t.Errorf("tag sea: got %d, want 3", stats.TagReads["sea"]) }
	if stats.TagReads["sailing"] != 2 { t.Errorf("tag sailing: got %d, want 2", stats.TagReads["sailing"]) }
	if stats.TagReads["code"] != 1 { t.Errorf("tag code: got %d, want 1", stats.TagReads["code"]) }
}

func TestStatStoreUniqueVisitors(t *testing.T) {
	ss := newTestStatStore(t)
	// Same visitor hash = same visitor
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", VisitorHash: "abc123"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", VisitorHash: "abc123"})
	// Different hash = different visitor
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", VisitorHash: "xyz789"})

	stats, _ := ss.Compute()
	if stats.UniqueVisitors != 2 { t.Errorf("unique visitors: got %d, want 2", stats.UniqueVisitors) }
}

func TestStatStoreCountries(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Country: "PL"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Country: "PL"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "p", Country: "DE"})

	stats, _ := ss.Compute()
	if stats.Countries["PL"] != 2 { t.Errorf("PL: got %d, want 2", stats.Countries["PL"]) }
	if stats.Countries["DE"] != 1 { t.Errorf("DE: got %d, want 1", stats.Countries["DE"]) }
}

func TestStatStoreReferrers(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Ref: "https://claude.ai/chat/123"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Ref: "https://claude.ai/other"})
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Ref: "https://twitter.com"})
	// Self-referral should be ignored
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", Ref: "https://kapoost-humanmcp.fly.dev/p/poem"})

	stats, _ := ss.Compute()
	if stats.TopReferrers["claude.ai"] != 2 { t.Errorf("claude.ai: got %d, want 2", stats.TopReferrers["claude.ai"]) }
	if stats.TopReferrers["twitter.com"] != 1 { t.Errorf("twitter.com: got %d, want 1", stats.TopReferrers["twitter.com"]) }
	if _, ok := stats.TopReferrers["kapoost-humanmcp.fly.dev"]; ok {
		t.Error("self-referral should be stripped")
	}
}

func TestStatStoreTopAgents(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "p", From: "claude"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "p", From: "claude"})
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "p", From: "gpt-4"})

	stats, _ := ss.Compute()
	if stats.TopAgents["claude"] != 2 { t.Errorf("claude: got %d, want 2", stats.TopAgents["claude"]) }
	if stats.TopAgents["gpt-4"] != 1 { t.Errorf("gpt-4: got %d, want 1", stats.TopAgents["gpt-4"]) }
}

func TestStatStoreCacheInvalidatedOnRecord(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p"})
	s1, _ := ss.Compute()
	if s1.TotalReads != 1 { t.Fatalf("want 1 read") }

	// Add another event — cache should be invalidated
	ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p"})
	s2, _ := ss.Compute()
	if s2.TotalReads != 2 { t.Errorf("cache not invalidated: still got %d reads", s2.TotalReads) }
}

func TestStatStoreRecentEvents(t *testing.T) {
	ss := newTestStatStore(t)
	for i := 0; i < 35; i++ {
		ss.Record(Event{Type: EventRead, Caller: CallerHuman, Slug: "p"})
	}
	stats, _ := ss.Compute()
	// All 35 events are within the last 24h → all should appear in RecentEvents.
	if len(stats.RecentEvents) != 35 {
		t.Errorf("recent events: got %d, want 35 (all within 24h window)", len(stats.RecentEvents))
	}
}

func TestVisitorHash(t *testing.T) {
	h1 := VisitorHash("1.2.3.4", "2024-01-01")
	h2 := VisitorHash("1.2.3.4", "2024-01-01")
	h3 := VisitorHash("1.2.3.5", "2024-01-01")
	h4 := VisitorHash("1.2.3.4", "2024-01-02")

	if h1 != h2 { t.Error("same ip+date should produce same hash") }
	if h1 == h3 { t.Error("different IPs should produce different hashes") }
	if h1 == h4 { t.Error("different dates should produce different hashes") }
	if h1 == "" { t.Error("hash should not be empty") }
	// Verify it doesn't contain the raw IP
	if h1 == "1.2.3.4" { t.Error("hash should not be raw IP") }
}

func TestCleanReferrer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://claude.ai/chat/abc", "claude.ai"},
		{"https://twitter.com/kapoost", "twitter.com"},
		{"http://github.com/repo?foo=bar", "github.com"},
		{"https://kapoost-humanmcp.fly.dev/p/poem", ""},   // self
		{"https://localhost:8080/page", ""},                 // localhost
		{"", ""},
	}
	for _, c := range cases {
		got := cleanReferrer(c.in)
		if got != c.want { t.Errorf("cleanReferrer(%q) = %q, want %q", c.in, got, c.want) }
	}
}

func TestCallerFromUA(t *testing.T) {
	cases := []struct{ ua string; want CallerType }{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X) AppleWebKit/537 Chrome/120", CallerHuman},
		{"Mozilla/5.0 Firefox/120", CallerHuman},
		{"Claude/3.5 Sonnet", CallerAgent},
		{"python-httpx/0.24", CallerAgent},
		{"curl/7.88", CallerAgent},
		{"Go-http-client/1.1", CallerAgent},
		{"", CallerUnknown},
		{"SomeRandomClient/1.0", CallerUnknown},
		// Googlebot smartphone: Mozilla-spoofed prefix with "Googlebot"
		// only appearing past the 120-char mark in the (compatible; ...) tail.
		{"Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/W.X.Y.Z Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", CallerAgent},
		{"facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)", CallerAgent},
		{"Mozilla/5.0 (compatible; Bingbot/2.0; +http://www.bing.com/bingbot.htm)", CallerAgent},
		{"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)", CallerAgent},
	}
	for _, c := range cases {
		got := CallerFromUA(c.ua)
		if got != c.want { t.Errorf("CallerFromUA(%q) = %q, want %q", c.ua, got, c.want) }
	}
}

func TestTopN(t *testing.T) {
	m := map[string]int{"a": 5, "b": 10, "c": 3, "d": 8}
	top := TopN(m, 2)
	if len(top) != 2 { t.Fatalf("want 2, got %d", len(top)) }
	if top[0].Key != "b" || top[0].Val != 10 { t.Errorf("top[0]: %+v", top[0]) }
	if top[1].Key != "d" || top[1].Val != 8 { t.Errorf("top[1]: %+v", top[1]) }
}

func TestStatStoreEmptyFile(t *testing.T) {
	ss := newTestStatStore(t)
	stats, err := ss.Compute()
	if err != nil { t.Fatalf("Compute on empty: %v", err) }
	if stats.TotalReads != 0 { t.Error("empty store should have 0 reads") }
	if stats.ReadsBySlug == nil { t.Error("ReadsBySlug should be initialized") }
}

func TestStatStoreJSON(t *testing.T) {
	ss := newTestStatStore(t)
	ss.Record(Event{Type: EventRead, Caller: CallerAgent, Slug: "test", From: "claude", Country: "PL"})
	stats, _ := ss.Compute()
	// Should serialize to JSON without error
	_, err := json.Marshal(stats)
	if err != nil { t.Errorf("json.Marshal: %v", err) }
}

func TestStatStoreCorruptLines(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	ss := NewStatStore(contentDir)

	// Write one valid + one corrupt line
	statsPath := filepath.Join(dir, "stats.ndjson")
	valid, _ := json.Marshal(Event{Type: EventRead, Caller: CallerHuman, Slug: "p", At: time.Now()})
	os.WriteFile(statsPath, append(valid, []byte("\nnot valid json\n")...), 0644)

	stats, err := ss.Compute()
	if err != nil { t.Fatalf("Compute: %v", err) }
	// Should parse 1 valid event and skip corrupt line
	if stats.TotalReads != 1 { t.Errorf("reads: got %d, want 1", stats.TotalReads) }
}

// TestComputeWindows verifies that ComputeWindows aggregates events into the
// expected rolling windows: today/yesterday don't overlap, 7d and 30d include
// today, visitors are unique-per-window.
func TestComputeWindows(t *testing.T) {
	ss := newTestStatStore(t)

	loc := time.UTC
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, loc) // anchor for windows
	todayStart := time.Date(2026, 5, 26, 0, 0, 0, 0, loc)

	mkEvent := func(at time.Time, caller CallerType, vh string) Event {
		return Event{Type: EventRead, Caller: caller, At: at, Slug: "p", VisitorHash: vh}
	}

	events := []Event{
		// today (2 humans different vh, 1 agent)
		mkEvent(todayStart.Add(2*time.Hour), CallerHuman, "vh-h1"),
		mkEvent(todayStart.Add(3*time.Hour), CallerHuman, "vh-h1"), // dup vh — same unique
		mkEvent(todayStart.Add(4*time.Hour), CallerHuman, "vh-h2"),
		mkEvent(todayStart.Add(5*time.Hour), CallerAgent, "vh-a1"),
		// yesterday (1 human)
		mkEvent(todayStart.Add(-3*time.Hour), CallerHuman, "vh-h3"),
		// 5 days ago (1 agent) — in 7d, not yesterday
		mkEvent(todayStart.Add(-5*24*time.Hour), CallerAgent, "vh-a2"),
		// 20 days ago (1 human) — in 30d, not 7d
		mkEvent(todayStart.Add(-20*24*time.Hour), CallerHuman, "vh-h4"),
		// 60 days ago (out of all windows)
		mkEvent(todayStart.Add(-60*24*time.Hour), CallerHuman, "vh-h5"),
	}

	statsPath := filepath.Join(filepath.Dir(ss.path), "stats.ndjson")
	var buf []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(statsPath, buf, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	w, err := ss.ComputeWindows(now)
	if err != nil {
		t.Fatalf("ComputeWindows: %v", err)
	}

	// Today
	if w.Today.Reads != 4 {
		t.Errorf("today reads = %d, want 4", w.Today.Reads)
	}
	if w.Today.Humans != 3 {
		t.Errorf("today humans = %d, want 3", w.Today.Humans)
	}
	if w.Today.Agents != 1 {
		t.Errorf("today agents = %d, want 1", w.Today.Agents)
	}
	if w.Today.Visitors != 3 { // vh-h1, vh-h2, vh-a1
		t.Errorf("today visitors = %d, want 3 unique", w.Today.Visitors)
	}
	// Yesterday — single day, no overlap with today
	if w.Yesterday.Reads != 1 {
		t.Errorf("yesterday reads = %d, want 1", w.Yesterday.Reads)
	}
	if w.Yesterday.Humans != 1 {
		t.Errorf("yesterday humans = %d, want 1", w.Yesterday.Humans)
	}
	// 7d: today (4) + yesterday (1) + 5d ago (1) = 6
	if w.Last7Days.Reads != 6 {
		t.Errorf("7d reads = %d, want 6", w.Last7Days.Reads)
	}
	// 30d: 7d (6) + 20d ago (1) = 7
	if w.Last30Days.Reads != 7 {
		t.Errorf("30d reads = %d, want 7", w.Last30Days.Reads)
	}
	// 60d ago must be excluded everywhere
	if w.Last30Days.Reads > 7 {
		t.Errorf("60d-old event leaked into 30d window")
	}
	// Sparkline DailyReads[13] = today, should be 4
	if w.DailyReads[13] != 4 {
		t.Errorf("DailyReads[13] (today) = %d, want 4", w.DailyReads[13])
	}
}
