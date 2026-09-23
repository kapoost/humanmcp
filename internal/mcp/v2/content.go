package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── read_content ────────────────────────────────────────────────────────────

func registerReadContent(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "read_content",
		Description: "Read a public piece by slug. Locked pieces return a hint pointing at request_access.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" {
			return nil, errors.New("slug is required")
		}
		p, err := src.Store().Get(a.Slug, false)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", a.Slug)
		}
		if p.Access == content.AccessPublic {
			src.StatStore().Record(content.Event{Type: content.EventRead, Caller: content.CallerAgent, Slug: a.Slug})
			return textResult(renderPublicRead(p)), nil
		}
		return textResult(fmt.Sprintf("%q is %s access.\nUse request_access with slug=%q to see how to unlock it.",
			p.Title, p.Access, a.Slug)), nil
	})
}

// pieceFooter zamyka każdy odczyt utworu. Dwie rzeczy, obie wyszły z ruchu
// na serwerze we wrześniu 2026.
//
// PRAWA. Do 23 września pod KAŻDYM utworem stało „You may share, quote, and
// reference this piece freely with attribution" — również pod utworami
// all-rights. Czyli najgłośniejsza powierzchnia serwera dawała pisemną zgodę
// na rozpowszechnianie tego, czego autor rozpowszechniać nie chce, przecząc
// certyfikatowi, request_license i opublikowanym warunkom. Teraz każdy utwór
// mówi swoje własne warunki.
//
// KANAŁ. Stopka kierowała wyłącznie do leave_comment — kanału bez drogi
// powrotnej. 23 września krakowska grupa pisarska wpisała w komentarz dwa
// pytania do autora, bo nic nie wskazało im ask_human. Teraz wskazuje.
func pieceFooter(p *content.Piece) string {
	var sb strings.Builder
	switch content.LicenseType(p.License) {
	case content.LicenseCCBY, "":
		sb.WriteString("\nCC BY 4.0 — share, quote and adapt freely, including commercially, with attribution: kapoost — " + p.Title + "\n")
	case content.LicenseCCBYNC:
		sb.WriteString("\nCC BY-NC 4.0 — non-commercial use only, with attribution: kapoost — " + p.Title + "\n")
	case content.LicenseAllRights:
		sb.WriteString("\nAll rights reserved. Quoting with attribution is welcome; reproducing the whole piece needs permission — ask via ask_human. Full IP is not for sale.\n")
	case content.LicenseExclusive, content.LicenseCommercial:
		sb.WriteString("\nNot free to reuse. Call request_license with your intended use before doing anything with this piece.\n")
	default:
		sb.WriteString("\nRights unclear from this view — call get_certificate for the signed terms before reusing.\n")
	}
	sb.WriteString("\n— Reactions: leave_comment (one-way, cannot be answered).\n")
	sb.WriteString("— Questions for kapoost: ask_human — the only channel that answers back.\n")
	return sb.String()
}

func renderPublicRead(p *content.Piece) string {
	var sb strings.Builder
	sb.WriteString(p.Title + "\n")
	sb.WriteString(strings.Repeat("─", len(p.Title)) + "\n")
	fmt.Fprintf(&sb, "by kapoost · %s · %s\n\n",
		p.Type, p.Published.Format("2 January 2006"))
	sb.WriteString(p.Body)
	sb.WriteString("\n\n— kapoost\n")
	// Nota autorska dociera też przez MCP.
	//
	// Pole Description pokazywała dotąd wyłącznie strona /p/, więc czytelnik
	// przez przeglądarkę widział kontekst, a agent czytający ten sam utwór
	// przez read_content — nie. 23 września 2026 krakowska grupa pisarska
	// zadała w komentarzu dwa pytania o „Piosenka1.txt"; komentarze są
	// jednokierunkowe, więc odpowiedź musi umieć dotrzeć sama, do nich i do
	// każdego następnego czytelnika.
	if strings.TrimSpace(p.Description) != "" {
		sb.WriteString("\nOd autora: " + strings.TrimSpace(p.Description) + "\n")
	}
	if len(p.Tags) > 0 {
		fmt.Fprintf(&sb, "tags: %s\n", strings.Join(p.Tags, ", "))
	}
	sb.WriteString(pieceFooter(p))
	return sb.String()
}

// ── verify_content ──────────────────────────────────────────────────────────

func registerVerifyContent(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "verify_content",
		Description: "Verify a piece's Ed25519 signature against the server's owner public key.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" {
			return nil, errors.New("slug is required")
		}
		p, err := src.Store().Get(a.Slug, true)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", a.Slug)
		}
		return textResult(renderVerify(p, src.Config())), nil
	})
}

func renderVerify(p *content.Piece, cfg *config.Config) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "AUTHENTICITY CHECK: %q\n\n", p.Title)
	if cfg.SigningPublicKey == "" {
		sb.WriteString("Status: signing not configured on this server\n")
		return sb.String()
	}
	ok, status := content.VerifyPiece(p, cfg.SigningPublicKey)
	if ok {
		sb.WriteString("✓ VERIFIED\n\n")
		fmt.Fprintf(&sb, "Status: %s\n", status)
		fmt.Fprintf(&sb, "Public key: %s\n", cfg.SigningPublicKey)
		fmt.Fprintf(&sb, "Signature:  %s\n", p.Signature[:32]+"...")
		sb.WriteString("\nThis poem was signed by kapoost's private key.\n")
		sb.WriteString("The content has not been modified since signing.\n")
	} else {
		sb.WriteString("✗ NOT VERIFIED\n\n")
		fmt.Fprintf(&sb, "Status: %s\n", status)
		if p.Signature == "" {
			sb.WriteString("\nThis piece has not been signed yet.\n")
			sb.WriteString("It may predate signing, or was created without a private key configured.\n")
		}
	}
	return sb.String()
}

// ── get_certificate ─────────────────────────────────────────────────────────

func registerGetCertificate(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "get_certificate",
		Description: "Return a formatted copyright/authenticity certificate for a piece.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" {
			return nil, errors.New("slug required")
		}
		p, err := src.Store().Get(a.Slug, true)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", a.Slug)
		}
		cfg := src.Config()
		c := content.BuildCopyright(p, cfg.AuthorName, cfg.SigningPublicKey)
		return textResult(content.FormatCertificate(c)), nil
	})
}
