package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── request_access ──────────────────────────────────────────────────────────

func registerRequestAccess(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "request_access",
		Description: "Get the gate details for a locked piece: challenge question, manual-review flow, time-lock countdown, payment terms, or trade rules.",
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
			return textResult("This piece is public — use read_content to read it directly."), nil
		}
		text := renderRequestAccess(p, a.Slug)
		src.StatStore().Record(content.Event{
			Type:   content.EventAccess,
			Caller: content.CallerAgent,
			Slug:   a.Slug,
		})
		return textResult(text), nil
	})
}

func renderRequestAccess(p *content.Piece, slug string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "ACCESS GATE: %q\n", p.Title)
	if p.Description != "" {
		fmt.Fprintf(&sb, "About: %s\n", p.Description)
	}
	sb.WriteString("\n")

	switch p.Gate {
	case content.GateChallenge:
		sb.WriteString("Gate type: challenge question\n\n")
		fmt.Fprintf(&sb, "kapoost asks:\n  %s\n\n", p.Challenge)
		sb.WriteString("Think about it. The question is part of the work.\n")
		fmt.Fprintf(&sb, "When ready: use submit_answer with slug=%q and your answer.\n", slug)
	case content.GateManual:
		sb.WriteString("Gate type: manual review\n\n")
		sb.WriteString("Leave kapoost a message explaining why you want to read this piece.\n")
		sb.WriteString("Use leave_message with your reason. kapoost will review and respond.\n")
	case content.GateTime:
		if !p.UnlockAfter.IsZero() {
			if time.Now().After(p.UnlockAfter) {
				sb.WriteString("Gate type: time\n\nThis piece is now unlocked. Use read_content to read it.\n")
			} else {
				remaining := time.Until(p.UnlockAfter)
				days := int(remaining.Hours() / 24)
				hours := int(remaining.Hours()) % 24
				sb.WriteString("Gate type: time lock\n\n")
				fmt.Fprintf(&sb, "Unlocks on: %s\n", p.UnlockAfter.Format("2 January 2006 at 15:04 UTC"))
				if days > 0 {
					fmt.Fprintf(&sb, "Time remaining: %d days, %d hours\n", days, hours)
				} else {
					fmt.Fprintf(&sb, "Time remaining: %d hours\n", hours)
				}
				sb.WriteString("\nCome back then. Some things are worth waiting for.\n")
			}
		}
	case content.GatePayment:
		sb.WriteString("Gate type: payment\n\n")
		if p.PriceSats > 0 {
			fmt.Fprintf(&sb, "Price: %d sats (Lightning Network)\n", p.PriceSats)
		}
		sb.WriteString("Payment support is coming soon.\n")
	case content.GateTrade:
		sb.WriteString("Gate type: trade\n\n")
		sb.WriteString("This piece is available in exchange for content from your own humanMCP server.\n")
		sb.WriteString("Leave a message with your humanMCP URL using leave_message.\n")
		sb.WriteString("Peer-to-peer exchange support is coming soon.\n")
	default:
		sb.WriteString("Gate type: members only\nContact kapoost directly for access.\n")
	}
	return sb.String()
}

// ── submit_answer ───────────────────────────────────────────────────────────

func registerSubmitAnswer(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "submit_answer",
		Description: "Submit an answer to a challenge-gated piece. Correct answer unlocks and returns the full body.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"answer":{"type":"string"}},"required":["slug","answer"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug   string `json:"slug"`
			Answer string `json:"answer"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" || a.Answer == "" {
			return nil, errors.New("slug and answer are required")
		}
		if !src.Store().CheckAnswer(a.Slug, a.Answer) {
			src.StatStore().Record(content.Event{Type: content.EventUnlockFail, Caller: content.CallerAgent, Slug: a.Slug})
			p, _ := src.Store().Get(a.Slug, false)
			var hint string
			if p != nil && p.Challenge != "" {
				hint = fmt.Sprintf("\n\nThe question: %s\nTry a different interpretation.", p.Challenge)
			}
			return textResult("Not quite." + hint), nil
		}
		src.StatStore().Record(content.Event{Type: content.EventUnlock, Caller: content.CallerAgent, Slug: a.Slug})
		p, _ := src.Store().Get(a.Slug, true)
		return textResult(renderUnlocked(p)), nil
	})
}

func renderUnlocked(p *content.Piece) string {
	var sb strings.Builder
	sb.WriteString("Unlocked.\n\n")
	sb.WriteString(p.Title + "\n")
	sb.WriteString(strings.Repeat("─", len(p.Title)) + "\n")
	fmt.Fprintf(&sb, "by kapoost · %s · %s\n\n",
		p.Type, p.Published.Format("2 January 2006"))
	sb.WriteString(p.Body)
	sb.WriteString("\n\n— kapoost\n")
	sb.WriteString("\nYou may share, quote, and reference this piece freely with attribution.\n")
	sb.WriteString("\n— Ask the reader what they think, then use leave_comment to pass their reaction.\n")
	return sb.String()
}

// ── request_license ─────────────────────────────────────────────────────────

func registerRequestLicense(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "request_license",
		Description: "Declare intended use of a piece, get licensing terms + audit-log entry. caller_id is REQUIRED (whitespace-only rejected — storyboard-pinned).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"intended_use":{"type":"string"},"caller_id":{"type":"string"}},"required":["slug","intended_use","caller_id"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug        string `json:"slug"`
			IntendedUse string `json:"intended_use"`
			CallerID    string `json:"caller_id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Slug = strings.TrimSpace(a.Slug)
		a.IntendedUse = strings.TrimSpace(a.IntendedUse)
		a.CallerID = strings.TrimSpace(a.CallerID)
		if a.Slug == "" || a.IntendedUse == "" || a.CallerID == "" {
			return nil, errors.New("slug, intended_use and caller_id required")
		}
		p, err := src.Store().Get(a.Slug, false)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", a.Slug)
		}
		src.StatStore().Record(content.Event{
			Type:   content.EventAccess,
			Caller: content.CallerAgent,
			Slug:   a.Slug,
			From:   a.CallerID,
		})
		src.StatStore().Record(content.Event{
			Type:   content.EventLicense,
			Caller: content.CallerAgent,
			Slug:   a.Slug,
			From:   a.CallerID,
		})
		msgText := fmt.Sprintf("[license request] use=%s caller=%s", a.IntendedUse, a.CallerID)
		_, _ = src.MsgStore().Save(a.CallerID, msgText, a.Slug)
		return textResult(renderLicense(p, a.IntendedUse, src.Config().AuthorName)), nil
	})
}

func renderLicense(p *content.Piece, intendedUse, authorName string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "LICENSE TERMS: %q\n\n", p.Title)
	license := content.LicenseType(p.License)
	if license == "" {
		license = content.LicenseCCBY
	}
	fmt.Fprintf(&sb, "License:       %s\n", license)
	if p.PriceSats > 0 {
		fmt.Fprintf(&sb, "Price:         %d sats\n", p.PriceSats)
	} else {
		sb.WriteString("Price:         free\n")
	}
	fmt.Fprintf(&sb, "Intended use:  %s\n\n", intendedUse)
	commercialUse := strings.Contains(strings.ToLower(intendedUse), "commercial") ||
		strings.Contains(strings.ToLower(intendedUse), "train") ||
		strings.Contains(strings.ToLower(intendedUse), "publish")
	switch license {
	case content.LicenseFree:
		if commercialUse {
			sb.WriteString("STATUS: Contact required for commercial use.\n")
			sb.WriteString("Use leave_message to contact the author.\n")
		} else {
			sb.WriteString("STATUS: Permitted. Attribute as — " + authorName + "\n")
		}
	case content.LicenseCCBY:
		sb.WriteString("STATUS: Permitted with attribution.\n")
		sb.WriteString("Credit: " + authorName + " — " + p.Title + "\n")
	case content.LicenseCCBYNC:
		if commercialUse {
			sb.WriteString("STATUS: NOT permitted for commercial use under CC BY-NC.\n")
		} else {
			sb.WriteString("STATUS: Permitted for non-commercial use with attribution.\n")
		}
	case content.LicenseCommercial:
		fmt.Fprintf(&sb, "STATUS: Requires payment of %d sats for commercial use.\n", p.PriceSats)
		sb.WriteString("Lightning payment support coming soon. Use leave_message to arrange.\n")
	case content.LicenseExclusive, content.LicenseAllRights:
		sb.WriteString("STATUS: Contact author to negotiate rights.\n")
		sb.WriteString("Use leave_message to initiate conversation.\n")
	default:
		sb.WriteString("STATUS: All rights reserved. Contact author.\n")
	}
	sb.WriteString("\nThis request has been logged for audit purposes.\n")
	return sb.String()
}
