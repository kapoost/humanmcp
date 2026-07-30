package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── leave_comment ───────────────────────────────────────────────────────────

func registerLeaveComment(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "leave_comment",
		Description: "Attach a short comment (≤280 chars) to a piece kapoost has published. Truncated silently past the limit.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"text":{"type":"string"},"from":{"type":"string"}},"required":["slug","text"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Slug string `json:"slug"`
			Text string `json:"text"`
			From string `json:"from"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if a.Slug == "" || a.Text == "" {
			return nil, errors.New("slug and text are required")
		}
		text := a.Text
		if len(text) > 280 {
			text = text[:280]
		}
		m, err := src.MsgStore().Save(a.From, text, a.Slug)
		if err != nil {
			return textResult("Could not save comment: " + err.Error()), nil
		}
		src.StatStore().Record(content.Event{
			Type:   content.EventComment,
			Caller: content.CallerAgent,
			Slug:   a.Slug,
			From:   a.From,
		})
		reply := fmt.Sprintf("Comment recorded. kapoost will read it.\n\nPiece: %s\nAt: %s",
			a.Slug, m.At.Format("2 January 2006, 15:04 UTC"))
		return textResult(reply), nil
	})
}

// ── leave_message ───────────────────────────────────────────────────────────

func registerLeaveMessage(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "leave_message",
		Description: "Send kapoost a longer message. context REQUIRED (why you're writing / which piece / which task). contact optional — without it no reply is possible.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"context":{"type":"string"},"contact":{"type":"string"},"from":{"type":"string"},"regarding":{"type":"string"}},"required":["text","context"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct {
			Text      string `json:"text"`
			Context   string `json:"context"`
			Contact   string `json:"contact"`
			From      string `json:"from"`
			Regarding string `json:"regarding"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.Text = strings.TrimSpace(a.Text)
		a.Context = strings.TrimSpace(a.Context)
		a.Contact = strings.TrimSpace(a.Contact)
		if a.Text == "" || a.Context == "" {
			return nil, errors.New("text and context required (context = why you are writing / which piece / which task)")
		}
		if len(a.Context) > 500 {
			a.Context = a.Context[:500]
		}
		if len(a.Contact) > 200 {
			a.Contact = a.Contact[:200]
		}

		var body strings.Builder
		body.WriteString("[context] " + a.Context + "\n")
		if a.Contact != "" {
			body.WriteString("[contact] " + a.Contact + "\n")
		}
		body.WriteString("\n")
		body.WriteString(a.Text)

		m, err := src.MsgStore().Save(a.From, body.String(), a.Regarding)
		if err != nil {
			return textResult("Could not save message: " + err.Error()), nil
		}
		src.StatStore().Record(content.Event{Type: content.EventMessage, Caller: content.CallerAgent})

		var reply strings.Builder
		fmt.Fprintf(&reply, "Message saved.\nID: %s\nTime: %s UTC\n\n",
			m.ID, m.At.Format("2006-01-02 15:04"))
		if a.Contact != "" {
			fmt.Fprintf(&reply, "Contact recorded: %s\n", a.Contact)
			reply.WriteString("kapoost reviews the inbox on his own schedule — no ETA is promised. If he decides to reply, it will go to the contact above.\n")
		} else {
			reply.WriteString("Saved as an anonymous note — no contact provided, so no reply is possible from this message alone.\n\n")
			reply.WriteString("If you want a reply, either:\n")
			reply.WriteString("  - call leave_message again with a `contact` field (email, URL, MCP endpoint), OR\n")
			reply.WriteString("  - use `ask_human` — returns an ID you can poll via fetch_answer later.\n")
		}
		return textResult(reply.String()), nil
	})
}
