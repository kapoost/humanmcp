package v2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── list_blobs ──────────────────────────────────────────────────────────────

func registerListBlobs(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "list_blobs",
		Description: "List typed data artifacts (images, contact, vectors, documents, datasets, capsules). Filter by blob_type. Readable-column reflects caller_kind + caller_id vs audience.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"blob_type":{"type":"string"},"caller_kind":{"type":"string"},"caller_id":{"type":"string"}}}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			BlobType   string `json:"blob_type"`
			CallerKind string `json:"caller_kind"`
			CallerID   string `json:"caller_id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		blobs, err := src.BlobStore().Load()
		if err != nil || len(blobs) == 0 {
			return textResult("No data artifacts available."), nil
		}
		out, count := renderBlobList(blobs, a.BlobType, a.CallerKind, a.CallerID)
		if count == 0 {
			return textResult("No blobs match your filter."), nil
		}
		return textResult(out), nil
	})
}

func renderBlobList(blobs []*content.Blob, blobType, callerKind, callerID string) (string, int) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Data artifacts from kapoost (%d total):\n\n", len(blobs)))
	count := 0
	for _, b := range blobs {
		if blobType != "" && string(b.BlobType) != blobType {
			continue
		}
		count++
		sb.WriteString(fmt.Sprintf("slug:        %s\n", b.Slug))
		sb.WriteString(fmt.Sprintf("type:        %s\n", b.BlobType))
		sb.WriteString(fmt.Sprintf("title:       %s\n", b.Title))
		sb.WriteString(fmt.Sprintf("access:      %s\n", b.Access))
		if b.MimeType != "" {
			sb.WriteString(fmt.Sprintf("mime_type:   %s\n", b.MimeType))
		}
		if b.Schema != "" {
			sb.WriteString(fmt.Sprintf("schema:      %s\n", b.Schema))
		}
		if b.Dimensions > 0 {
			sb.WriteString(fmt.Sprintf("dimensions:  %d\n", b.Dimensions))
		}
		if b.Encoding != "" {
			sb.WriteString(fmt.Sprintf("encoding:    %s\n", b.Encoding))
		}
		if b.Description != "" {
			sb.WriteString(fmt.Sprintf("description: %s\n", b.Description))
		}
		if len(b.Audience) > 0 {
			parts := make([]string, len(b.Audience))
			for i, a := range b.Audience {
				parts[i] = a.Kind + ":" + a.ID
			}
			sb.WriteString(fmt.Sprintf("audience:    %s\n", strings.Join(parts, ", ")))
		}
		if b.IsAccessibleTo(callerKind, callerID) {
			sb.WriteString("readable:    yes — use read_blob\n")
		} else {
			sb.WriteString("readable:    no — not in audience list\n")
		}
		sb.WriteString("\n")
	}
	return sb.String(), count
}

// ── read_blob ───────────────────────────────────────────────────────────────

func registerReadBlob(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "read_blob",
		Description: "Read one blob by slug. Returns text or base64-encoded data. Access-gated by audience unless blob is public.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"caller_kind":{"type":"string"},"caller_id":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug       string `json:"slug"`
			CallerKind string `json:"caller_kind"`
			CallerID   string `json:"caller_id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" {
			return nil, errors.New("slug is required")
		}
		b, err := src.BlobStore().Get(a.Slug)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", a.Slug)
		}
		if !b.IsAccessibleTo(a.CallerKind, a.CallerID) && b.Access != content.AccessPublic {
			return textResult(renderBlobDenied(b, a.CallerKind, a.CallerID)), nil
		}
		src.StatStore().Record(content.Event{Type: content.EventRead, Caller: content.CallerAgent, Slug: a.Slug, From: a.CallerID})
		return textResult(renderBlobBody(b, src.BlobStore())), nil
	})
}

func renderBlobDenied(b *content.Blob, callerKind, callerID string) string {
	text := fmt.Sprintf("Access denied: %q\n\nYou (%s:%s) are not in the audience list for this artifact.\n", b.Title, callerKind, callerID)
	if len(b.Audience) > 0 {
		parts := make([]string, len(b.Audience))
		for i, au := range b.Audience {
			parts[i] = au.Kind + ":" + au.ID
		}
		text += fmt.Sprintf("Authorized: %s\n", strings.Join(parts, ", "))
	}
	return text
}

func renderBlobBody(b *content.Blob, store *content.BlobStore) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("BLOB: %s\n", b.Title))
	sb.WriteString(fmt.Sprintf("slug:      %s\n", b.Slug))
	sb.WriteString(fmt.Sprintf("type:      %s\n", b.BlobType))
	if b.MimeType != "" {
		sb.WriteString(fmt.Sprintf("mime_type: %s\n", b.MimeType))
	}
	if b.Schema != "" {
		sb.WriteString(fmt.Sprintf("schema:    %s\n", b.Schema))
	}
	if b.Dimensions > 0 {
		sb.WriteString(fmt.Sprintf("dimensions: %d\n", b.Dimensions))
	}
	if b.Encoding != "" {
		sb.WriteString(fmt.Sprintf("encoding:  %s\n", b.Encoding))
	}
	if b.Signature != "" {
		n := 32
		if len(b.Signature) < n {
			n = len(b.Signature)
		}
		sb.WriteString(fmt.Sprintf("signature: %s...\n", b.Signature[:n]))
	}
	sb.WriteString("\n")

	switch b.BlobType {
	case content.BlobVector, content.BlobDocument, content.BlobImage:
		if b.Base64Data != "" {
			sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data))
		} else if b.FileRef != "" {
			data, err := store.ReadFile(b.FileRef)
			if err != nil {
				sb.WriteString(fmt.Sprintf("file_ref: %s (read error: %v)\n", b.FileRef, err))
			} else {
				encoded := base64.StdEncoding.EncodeToString(data)
				sb.WriteString(fmt.Sprintf("data (base64, from file):\n%s\n", encoded))
			}
		}
	case content.BlobContact, content.BlobDataset, content.BlobCapsule:
		if b.TextData != "" {
			sb.WriteString(fmt.Sprintf("data:\n%s\n", b.TextData))
		} else if b.Base64Data != "" {
			sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data))
		}
	default:
		if b.TextData != "" {
			sb.WriteString(fmt.Sprintf("data:\n%s\n", b.TextData))
		}
		if b.Base64Data != "" {
			sb.WriteString(fmt.Sprintf("data (base64):\n%s\n", b.Base64Data))
		}
	}
	return sb.String()
}
