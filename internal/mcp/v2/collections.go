package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── list_collection ─────────────────────────────────────────────────────────

func registerListCollection(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_collection",
		Description: "List works kapoost owns but did NOT create. Public items always; members-only surface only after session activation.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		bootstrapped := false
		if req.Extra != nil {
			bootstrapped = src.IsSessionActiveByHeaders(req.Extra.Header)
		}
		items := filterCollectionItems(src.CollectionStore().List(), bootstrapped)
		if len(items) == 0 {
			return textResult("No collection items visible to this caller."), nil
		}
		return textResult(renderCollectionList(items)), nil
	})
}

func filterCollectionItems(all []content.CollectionItem, bootstrapped bool) []content.CollectionItem {
	var out []content.CollectionItem
	for _, it := range all {
		if it.Access == "public" {
			out = append(out, it)
		} else if it.Access == "members" && bootstrapped {
			out = append(out, it)
		}
	}
	return out
}

func renderCollectionList(items []content.CollectionItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collection (%d items) — works kapoost owns but did NOT create.\n", len(items))
	fmt.Fprint(&b, "Nothing here is signed by kapoost; IP belongs to the original creator.\n\n")
	for _, it := range items {
		fmt.Fprintf(&b, "- %s\n", it.Title)
		fmt.Fprintf(&b, "  slug:     %s\n", it.Slug)
		fmt.Fprintf(&b, "  by:       %s", it.OriginalCreator)
		if it.Year != 0 {
			fmt.Fprintf(&b, ", %d", it.Year)
		}
		fmt.Fprintln(&b)
		if it.Medium != "" || it.Dimensions != "" {
			fmt.Fprintf(&b, "  details:  %s %s\n", it.Medium, it.Dimensions)
		}
		if !it.AcquiredAt.IsZero() {
			fmt.Fprintf(&b, "  acquired: %s", it.AcquiredAt.Format("2006-01-02"))
			if it.AcquiredFrom != "" {
				fmt.Fprintf(&b, " from %s", it.AcquiredFrom)
			}
			fmt.Fprintln(&b)
		}
		fmt.Fprintf(&b, "  access:   %s\n\n", it.Access)
	}
	return b.String()
}

// ── read_collection_item ────────────────────────────────────────────────────

func registerReadCollectionItem(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "read_collection_item",
		Description: "Full record for one collection item + dossier count. Access-gated: private is never visible, members needs session.",
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
		item, err := src.CollectionStore().Get(a.Slug)
		if err != nil {
			return textResult("Not found: " + err.Error()), nil
		}
		bootstrapped := false
		if req.Extra != nil {
			bootstrapped = src.IsSessionActiveByHeaders(req.Extra.Header)
		}
		if item.Access == "private" {
			return textResult("Item is private; not visible to MCP callers."), nil
		}
		if item.Access == "members" && !bootstrapped {
			return textResult("Item is members-only. Bootstrap a session to see it."), nil
		}
		dossier, _ := src.ProvenanceStore().List(content.OwnerCollection, a.Slug)
		return textResult(renderCollectionItem(item, len(dossier))), nil
	})
}

func renderCollectionItem(item *content.CollectionItem, dossierCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collection item: %s\n\n", item.Title)
	fmt.Fprintf(&b, "Slug:            %s\n", item.Slug)
	fmt.Fprintf(&b, "Original creator: %s\n", item.OriginalCreator)
	if item.Year != 0 {
		fmt.Fprintf(&b, "Year:            %d\n", item.Year)
	}
	if item.Medium != "" {
		fmt.Fprintf(&b, "Medium:          %s\n", item.Medium)
	}
	if item.Dimensions != "" {
		fmt.Fprintf(&b, "Dimensions:      %s\n", item.Dimensions)
	}
	if !item.AcquiredAt.IsZero() {
		fmt.Fprintf(&b, "Acquired:        %s\n", item.AcquiredAt.Format("2006-01-02"))
	}
	if item.AcquiredFrom != "" {
		fmt.Fprintf(&b, "Acquired from:   %s\n", item.AcquiredFrom)
	}
	fmt.Fprintf(&b, "Access:          %s\n", item.Access)
	if item.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", item.Notes)
	}
	fmt.Fprintf(&b, "\nDossier: %d document(s). Use list_provenance(slug=%q) to enumerate.\n",
		dossierCount, item.Slug)
	fmt.Fprintln(&b, "\nThis work is NOT signed by kapoost. It belongs to the original creator.")
	return b.String()
}
