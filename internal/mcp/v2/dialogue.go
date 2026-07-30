package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// ── ask_human ───────────────────────────────────────────────────────────────

func registerAskHuman(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "ask_human",
		Description: "Submit an async question to kapoost. Returns an ID. Rate-limited 5/hr/IP. Poll fetch_answer later — kapoost answers on his own schedule (minutes, hours, or days).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"context":{"type":"string"},"from":{"type":"string"}},"required":["question"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ip := ""
		if req.Extra != nil {
			ip = src.ClientIPFromHeaders(req.Extra.Header)
		}
		if !src.CheckAskHumanRateLimit(ip) {
			log.Printf("[AUDIT] ask_human RATE_LIMITED ip=%s", ip)
			return textResult("Too many questions from this caller — limit is 5 per hour. Try again later."), nil
		}
		var a struct {
			Question string `json:"question"`
			Context  string `json:"context"`
			From     string `json:"from"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		if len(a.Question) > 1000 {
			a.Question = a.Question[:1000]
		}
		if len(a.Context) > 500 {
			a.Context = a.Context[:500]
		}
		if len(a.From) > 64 {
			a.From = a.From[:64]
		}
		q, err := src.QuestionStore().Create(a.From, a.Context, a.Question)
		if err != nil {
			return textResult("Could not create question: " + err.Error()), nil
		}
		src.StatStore().Record(content.Event{
			Type:   content.EventMessage,
			Caller: content.CallerAgent,
			From:   a.From,
		})
		reply := fmt.Sprintf(`Question submitted. kapoost will see it on /questions.

ID: %s
Asked at: %s

IMPORTANT — kapoost answers on his own schedule (minutes, hours, or days).
Do NOT block this session waiting. Arrange a durable wait instead.

═══ HOW TO WAIT — pick ONE mechanism your runtime supports ═══

(1) PERSIST THE ID so a future you can find it:
    • humanMCP: call remember(key="pending_q_%s", value="<id>|<short question>")
      — then recall(key="pending_q_*") on next session start.
    • Claude Code: append a line to your project MEMORY.md, e.g.
      "Pending humanMCP question: id=%s asked=%s — poll fetch_answer".
    • Claude Agent SDK / API: store in Memory tool, KV, or a DB row.
    • Any runtime: whatever file / note / journal survives session teardown.

(2) SCHEDULE THE POLL — don't loop now, don't retry hot:
    • Claude Code: use /schedule to create a cron routine that calls
      fetch_answer(id=%q), or /loop with an interval of 1-6 hours.
    • Standalone agents: register a cron / systemd timer that pings
      fetch_answer and delivers the answer when it arrives.
    • Session-based agents: poll ONCE on each new session start —
      that is the natural cadence, no scheduling needed.

(3) ON WAKE, call fetch_answer(id=%q):
    • Still awaiting? Reschedule and go back to sleep.
    • Answered? Act on it, then clear the persisted ID so you don't
      re-check a resolved question.

Rate limit: fetch_answer is capped at 30 polls per hour per IP.
Anything tighter than every 5 minutes is wasted work and will backoff.`,
			q.ID, q.AskedAt.Format("2 January 2006, 15:04 UTC"),
			q.ID, q.ID, q.AskedAt.Format("2006-01-02"), q.ID, q.ID)
		return textResult(reply), nil
	})
}

// ── fetch_answer ────────────────────────────────────────────────────────────

func registerFetchAnswer(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "fetch_answer",
		Description: "Poll for an answer to an ask_human question. Marks the question as fetched the first time an answer is returned. Rate-limited 30/hr/IP.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ip := ""
		if req.Extra != nil {
			ip = src.ClientIPFromHeaders(req.Extra.Header)
		}
		if !src.CheckFetchAnswerRateLimit(ip) {
			log.Printf("[AUDIT] fetch_answer RATE_LIMITED ip=%s", ip)
			return textResult("Too many polls from this caller — limit is 30 per hour. Try again later."), nil
		}
		var a struct {
			ID string `json:"id"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &a)
		}
		a.ID = strings.TrimSpace(a.ID)
		if a.ID == "" {
			return textResult("id required"), nil
		}
		q, err := src.QuestionStore().Get(a.ID)
		if err != nil {
			return textResult("No question with that ID. Check the id from ask_human's response."), nil
		}
		if !q.IsAnswered() {
			reply := fmt.Sprintf("Still awaiting kapoost's answer.\n\nID: %s\nAsked: %s\nQuestion: %s\n\nkapoost answers on his own time — minutes, hours, or days. Keep this ID in durable memory and come back later. No need to keep this session open or to poll tightly. Try again at your next session start, or in a few hours.",
				q.ID, q.AskedAt.Format("2 January 2006, 15:04 UTC"), q.Question)
			return textResult(reply), nil
		}
		if !q.IsFetched() {
			_ = src.QuestionStore().MarkFetched(q.ID, "agent")
		}
		reply := fmt.Sprintf("Answer from kapoost:\n\n%s\n\n— answered at %s",
			q.Answer, q.AnsweredAt.Format("2 January 2006, 15:04 UTC"))
		return textResult(reply), nil
	})
}
