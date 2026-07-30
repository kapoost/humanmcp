package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── list_provenance ─────────────────────────────────────────────────────────

func registerListProvenance(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_provenance",
		Description: "List provenance items (chain of custody, certificates, invoices) for an artwork slug. Tries piece then collection.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" {
			return textResult("slug required"), nil
		}
		items, err := src.ProvenanceStore().List(content.OwnerPiece, a.Slug)
		if err != nil || len(items) == 0 {
			if ci, cerr := src.ProvenanceStore().List(content.OwnerCollection, a.Slug); cerr == nil && len(ci) > 0 {
				items, err = ci, nil
			}
		}
		if err != nil {
			return textResult("Could not list provenance: " + err.Error()), nil
		}
		if len(items) == 0 {
			return textResult(fmt.Sprintf("No provenance items for artwork %q.", a.Slug)), nil
		}
		return textResult(renderProvenanceList(a.Slug, items)), nil
	})
}

func renderProvenanceList(slug string, items []content.ProvenanceItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Provenance for %q (%d items, grouped by category):\n\n", slug, len(items))
	prevCat := ""
	for _, it := range items {
		if string(it.Category) != prevCat {
			fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(string(it.Category)))
			prevCat = string(it.Category)
		}
		fmt.Fprintf(&b, "- [%s] %s\n", it.Type, it.Title)
		fmt.Fprintf(&b, "  id: %s\n", it.ID)
		fmt.Fprintf(&b, "  issued_by: %s\n", it.IssuedBy)
		fmt.Fprintf(&b, "  issued_at: %s\n", it.IssuedAt.Format("2006-01-02"))
		if it.ChainPosition > 0 {
			fmt.Fprintf(&b, "  chain_position: %d\n", it.ChainPosition)
		}
		for _, f := range it.Files {
			fmt.Fprintf(&b, "  file: %s  sha256=%s  bytes=%d\n", f.Filename, f.ContentHash, f.SizeBytes)
		}
		if it.Signature != "" {
			fmt.Fprintf(&b, "  signed: yes\n")
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// ── read_provenance ─────────────────────────────────────────────────────────

func registerReadProvenance(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "read_provenance",
		Description: "Return one provenance item with resolvable file URLs. slug + id required.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"id":{"type":"string"}},"required":["slug","id"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
			ID   string `json:"id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" || a.ID == "" {
			return textResult("slug and id required"), nil
		}
		item, err := src.ProvenanceStore().Get(content.OwnerPiece, a.Slug, a.ID)
		if err != nil {
			if ci, cerr := src.ProvenanceStore().Get(content.OwnerCollection, a.Slug, a.ID); cerr == nil {
				item, err = ci, nil
			}
		}
		if err != nil {
			return textResult("Not found: " + err.Error()), nil
		}
		return textResult(renderProvenanceItem(item, src.Config().Domain)), nil
	})
}

func renderProvenanceItem(item *content.ProvenanceItem, domain string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Provenance item %s\n\n", item.ID)
	fmt.Fprintf(&b, "Artwork:        %s\n", item.ArtworkSlug)
	fmt.Fprintf(&b, "Category:       %s\n", item.Category)
	fmt.Fprintf(&b, "Type:           %s\n", item.Type)
	fmt.Fprintf(&b, "Title:          %s\n", item.Title)
	fmt.Fprintf(&b, "Issued by:      %s\n", item.IssuedBy)
	fmt.Fprintf(&b, "Issued at:      %s\n", item.IssuedAt.Format("2006-01-02"))
	if item.ChainPosition > 0 {
		fmt.Fprintf(&b, "Chain position: %d\n", item.ChainPosition)
	}
	fmt.Fprintln(&b)
	if len(item.Files) > 0 {
		fmt.Fprintln(&b, "Files:")
		for _, f := range item.Files {
			fmt.Fprintf(&b, "  https://%s/provenance/files/%s\n", domain, f.FileRef)
			fmt.Fprintf(&b, "    sha256: %s\n", f.ContentHash)
			fmt.Fprintf(&b, "    bytes:  %d\n", f.SizeBytes)
		}
		fmt.Fprintln(&b)
	}
	if item.Signature != "" {
		fmt.Fprintf(&b, "Signature:      %s (Ed25519)\n", item.Signature[:32]+"…")
	}
	if item.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", item.Notes)
	}
	return b.String()
}
