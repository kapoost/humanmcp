package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// ── upsert_skill (owner-only) ───────────────────────────────────────────────

func registerUpsertSkill(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "upsert_skill",
		Description: "Create or replace a skill by slug. Owner-only: requires Authorization: Bearer <token>. Writes JSON to content/skills/<slug>.json.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"category":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["slug","category","title","body"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult("Unauthorized — requires agent token in Authorization: Bearer <token> header."), nil
		}
		var s mcp.Skill
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &s); err != nil {
				return textResult("slug, category, title, body are required."), nil
			}
		}
		if s.Slug == "" {
			return textResult("slug, category, title, body are required."), nil
		}
		s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.UpdatedBy = "agent"
		dir := filepath.Join(src.Config().ContentDir, "skills")
		_ = os.MkdirAll(dir, 0755)
		data, _ := json.MarshalIndent(s, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, s.Slug+".json"), data, 0644); err != nil {
			return textResult(fmt.Sprintf("Write error: %v", err)), nil
		}
		return textResult(fmt.Sprintf("Skill '%s' saved.", s.Slug)), nil
	})
}

// ── delete_skill (owner-only) ───────────────────────────────────────────────

func registerDeleteSkill(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "delete_skill",
		Description: "Delete a skill by slug. Owner-only: requires Authorization: Bearer <token>.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !ownerRequest(src, req) {
			return textResult("Unauthorized — requires agent token in Authorization: Bearer <token> header."), nil
		}
		var params struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &params)
		}
		if params.Slug == "" {
			return textResult("slug is required."), nil
		}
		path := filepath.Join(src.Config().ContentDir, "skills", params.Slug+".json")
		if err := os.Remove(path); err != nil {
			return textResult(fmt.Sprintf("Skill '%s' not found.", params.Slug)), nil
		}
		return textResult(fmt.Sprintf("Skill '%s' deleted.", params.Slug)), nil
	})
}

func ownerRequest(src Source, req *sdk.CallToolRequest) bool {
	if req == nil || req.Extra == nil {
		return false
	}
	return src.IsOwnerRequestByHeaders(req.Extra.Header)
}
