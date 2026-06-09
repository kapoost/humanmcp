package web

import (
	"testing"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

// Guards the "mcp tool calls" card on /mc. enrichedStats.ToolCalls must
// surface stats.AgentCalls — not stay at zero. The card was silently
// broken for ~weeks because the field was declared, threaded through
// the render map, but never populated. Catch the regression early.
func TestEnrichedStatsToolCallsMirrorsAgentCalls(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ContentDir: dir}
	store := content.NewStore(dir)
	h := NewHandler(cfg, store, auth.New("x"))

	es := h.buildEnrichedStats(&content.Stats{AgentCalls: 42}, 0, 0)
	if es.ToolCalls != 42 {
		t.Errorf("ToolCalls = %d, want 42 (AgentCalls field must propagate)", es.ToolCalls)
	}

	es = h.buildEnrichedStats(&content.Stats{AgentCalls: 0}, 0, 0)
	if es.ToolCalls != 0 {
		t.Errorf("ToolCalls = %d, want 0 for zero AgentCalls", es.ToolCalls)
	}
}
