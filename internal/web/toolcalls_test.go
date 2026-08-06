package web

import (
	"testing"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// Guards every counter buildEnrichedStats is contractually supposed to
// populate. Catches the class of regression that bit ToolCalls and then
// TotalSearches: field declared on the struct, threaded into the render
// map, never assigned — silently renders the zero value (0 / "" / false)
// while the page returns 200.
//
// Add a row whenever buildEnrichedStats gains a new derived field, and
// every time you add a new card to mc.html that reads $.Foo, audit
// whether Foo needs to be propagated here.
// Do NOT cover fields set elsewhere (Inbox*, SessionExp) — those belong
// in handleMissionControl's own test.
func TestBuildEnrichedStatsPropagation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ContentDir: dir}
	store := content.NewStore(dir)
	h := NewHandler(cfg, store, auth.New("x"))

	in := &content.Stats{
		AgentCalls:    42,
		TotalReads:    17,
		HumanVisits:   9,
		TotalSearches: 11,
		TopSearches:   map[string]int{"niebo": 3, "morze": 2},
	}
	es := h.buildEnrichedStats(in, 5, 3)

	cases := []struct {
		field string
		got   interface{}
		want  interface{}
	}{
		// args propagate verbatim
		{"PieceCount", es.PieceCount, 5},
		{"TotalListings", es.TotalListings, 3},

		// derived from stats
		{"ToolCalls", es.ToolCalls, 42},
		{"TotalSearches", es.TotalSearches, 11},

		// Stats pointer preserved + readable through embedding
		{"Stats.TotalReads", es.Stats.TotalReads, 17},
		{"Stats.AgentCalls", es.Stats.AgentCalls, 42},
		{"Stats.HumanVisits", es.Stats.HumanVisits, 9},
		{"Stats.TotalSearches", es.Stats.TotalSearches, 11},

		// Nil liveness (SetLiveness never called) ⇒ we don't claim online.
		// The old test asserted VaultOnline==true unconditionally — that
		// hid the fact we weren't measuring anything.
		{"VaultOnline (no liveness wired)", es.VaultOnline, false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}

	// Uptime is time-dependent so we only assert non-empty. formatUptime
	// always emits at least "0s" — a literal "" means the field was
	// never assigned.
	if es.Uptime == "" {
		t.Error("Uptime is empty — buildEnrichedStats forgot to set it")
	}
	// TopSearches map propagation — declaring the field but not
	// assigning it leaves a nil map, which collapses both the card
	// {{if .TopSearches}} and any subsequent {{range}}.
	if got := es.TopSearches["niebo"]; got != 3 {
		t.Errorf("TopSearches[niebo] = %d, want 3 (map must propagate)", got)
	}
}

// Guards the axel-brandt regression: VaultOnline was hardcoded true, so
// every storyboard that "tested offline" tested nothing. Now the field must
// actually reflect liveness state; injecting fresh/stale heartbeats must
// flip the flag deterministically.
func TestVaultOnlineTracksLivenessState(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ContentDir: dir}
	store := content.NewStore(dir)
	h := NewHandler(cfg, store, auth.New("x"))

	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := base
	live := mysloodsiewnia.NewWith(30*time.Second, func() time.Time { return now })
	h.SetLiveness(live)

	// Fresh heartbeat with healthy FTS ⇒ online.
	live.Update("sha1", base, base, true)
	if got := h.buildEnrichedStats(&content.Stats{}, 0, 0).VaultOnline; !got {
		t.Fatal("fresh heartbeat should render VaultOnline=true")
	}

	// Advance past TTL ⇒ offline in the dashboard.
	now = base.Add(31 * time.Second)
	if got := h.buildEnrichedStats(&content.Stats{}, 0, 0).VaultOnline; got {
		t.Fatal("stale heartbeat should render VaultOnline=false")
	}

	// Fresh heartbeat but FTS rebuilding ⇒ degraded ⇒ not online.
	// MC dashboard collapses degraded to OFFLINE badge on purpose: both
	// states mean "don't route agent operations here right now".
	live.Update("sha2", base, base, false)
	now = base.Add(1 * time.Second)
	if got := h.buildEnrichedStats(&content.Stats{}, 0, 0).VaultOnline; got {
		t.Fatal("degraded (fts rebuilding) should not render VaultOnline=true")
	}
}

// Specifically guards the bug that triggered this whole exercise: the
// mcp-tool-calls card on /mc rendered 0 forever because nothing wrote
// es.ToolCalls. Locks the AgentCalls → ToolCalls mirror, including the
// zero case (so a future refactor doesn't accidentally default to a
// constant or copy from the wrong field).
func TestEnrichedStatsToolCallsMirrorsAgentCalls(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ContentDir: dir}
	store := content.NewStore(dir)
	h := NewHandler(cfg, store, auth.New("x"))

	if es := h.buildEnrichedStats(&content.Stats{AgentCalls: 42}, 0, 0); es.ToolCalls != 42 {
		t.Errorf("ToolCalls = %d, want 42 (AgentCalls field must propagate)", es.ToolCalls)
	}
	if es := h.buildEnrichedStats(&content.Stats{AgentCalls: 0}, 0, 0); es.ToolCalls != 0 {
		t.Errorf("ToolCalls = %d, want 0 for zero AgentCalls", es.ToolCalls)
	}
}
