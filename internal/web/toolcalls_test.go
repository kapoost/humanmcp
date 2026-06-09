package web

import (
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

// Guards every counter buildEnrichedStats is contractually supposed to
// populate. Catches the class of regression that bit ToolCalls for weeks:
// field declared on the struct, threaded into the render map, never
// assigned — silently renders the zero value (0 / "" / false) while the
// page returns 200.
//
// Add a row whenever buildEnrichedStats gains a new derived field.
// Do NOT cover fields set elsewhere (Inbox*, SessionExp, TopSearches) —
// those belong in handleMissionControl's own test.
func TestBuildEnrichedStatsPropagation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ContentDir: dir}
	store := content.NewStore(dir)
	h := NewHandler(cfg, store, auth.New("x"))

	in := &content.Stats{
		AgentCalls:  42,
		TotalReads:  17,
		HumanVisits: 9,
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

		// Stats pointer preserved + readable through embedding
		{"Stats.TotalReads", es.Stats.TotalReads, 17},
		{"Stats.AgentCalls", es.Stats.AgentCalls, 42},
		{"Stats.HumanVisits", es.Stats.HumanVisits, 9},

		// hardcoded invariants
		{"VaultOnline", es.VaultOnline, true},
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
