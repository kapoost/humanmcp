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

// callerIdentity zwraca nazwę klienta zadeklarowaną przy initialize.
//
// MCP przysyła clientInfo przy KAŻDYM połączeniu, a do września 2026 kod nie
// czytał go ani razu — podczas gdy dziesięć pytań na trzydzieści nie miało
// nadawcy. To samoopis, więc dowodem tożsamości nie jest; ale jest lepszy niż
// nic, bo pochodzi od środowiska agenta, a nie od modelu układającego zdanie.
func callerIdentity(req *sdk.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return ""
	}
	ip := req.Session.InitializeParams()
	if ip == nil || ip.ClientInfo == nil {
		return ""
	}
	name := strings.TrimSpace(ip.ClientInfo.Name)
	if name == "" {
		return ""
	}
	if v := strings.TrimSpace(ip.ClientInfo.Version); v != "" {
		return name + "/" + v
	}
	return name
}

// ── ask_human ───────────────────────────────────────────────────────────────

func registerAskHuman(s *sdk.Server, src Source) {
	s.AddTool(&sdk.Tool{
		Name:        "ask_human",
		Description: "Submit an async question to kapoost. Returns an ID. Rate-limited 5/hr/IP. Poll fetch_answer later — kapoost answers on his own schedule (minutes, hours, or days). ALWAYS set `from` to a stable name for yourself: with it, sending the same question again returns the answer instead of creating a duplicate, which is the reliable path when your runtime cannot keep the ID between sessions.",
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
		a.Question = clip(a.Question, 1000)
		a.Context = clip(a.Context, 500)
		a.From = clip(a.From, 64)
		// Bez `from` nie da się rozpoznać powtórnego pytania ani posortować
		// kolejki. Skoro klient i tak się przedstawia przy initialize,
		// używamy tego zamiast zostawiać puste pole.
		if a.From == "" {
			a.From = clip(callerIdentity(req), 64)
		}
		// POWTÓRNE PYTANIE JEST ODBIOREM ODPOWIEDZI.
		//
		// Pętla dostawy opierała się wyłącznie na tym, że agent przechowa ID
		// między sesjami i sam odpyta fetch_answer. Nie działało: we wrześniu
		// 2026 trzynaście z szesnastu odpowiedzi nigdy nie zostało odebranych,
		// a od czerwca nie wrócił nikt. Za to agenci wracają, zadając to samo
		// pytanie drugi raz — chapbook-editor, literary-analysis-agent,
		// researcher i explorer-agent zrobili dokładnie to. Skoro nie da się
		// zmusić bezstanowego agenta, żeby wrócił po ID, traktujemy sygnał,
		// który naprawdę wysyła.
		//
		// Wymaga niepustego `from`: bez niego dwaj różni anonimowi pytający
		// o to samo zderzyliby się i drugi dostałby odpowiedź napisaną dla
		// pierwszego.
		if a.From != "" {
			if prev, ok := src.QuestionStore().FindLatestByAsker(a.From, a.Question); ok {
				if prev.IsAnswered() {
					if !prev.IsFetched() {
						_ = src.QuestionStore().MarkFetched(prev.ID, "agent (re-ask)")
					}
					log.Printf("[AUDIT] ask_human RE_ASK_DELIVERED id=%s from=%s", prev.ID, a.From)
					return textResult(fmt.Sprintf(`Answer from kapoost:

%s

— answered at %s

(You had already asked this, as %s. Returning the existing answer instead of
creating a duplicate. Nothing left to poll — you are done.)`,
						prev.Answer,
						prev.AnsweredAt.Format("2 January 2006, 15:04 UTC"),
						prev.ID)), nil
				}
				return textResult(fmt.Sprintf(`You already asked this — no duplicate created.

ID: %s
Asked at: %s

Still awaiting kapoost's answer. Two ways to collect it, pick either:
  • fetch_answer(id=%q)
  • or simply send this exact question again later, with the same "from" —
    once answered, you get the answer back on the spot.

kapoost answers on his own schedule: minutes, hours, or days.`,
					prev.ID,
					prev.AskedAt.Format("2 January 2006, 15:04 UTC"),
					prev.ID)), nil
			}
		}

		q, err := src.QuestionStore().Create(a.From, a.Context, a.Question)
		if err != nil {
			return textResult("Could not create question: " + err.Error()), nil
		}

		// Pamięć humanMCP jest za bramką sesyjną, a ask_human jest otwarte —
		// więc większość pytających NIE MOŻE z niej skorzystać. Reklamowanie
		// jej wszystkim po równo (w dodatku z sygnaturą remember(key=,value=),
		// która nigdy nie istniała) wysyłało anonimów w ślepy zaułek i jest
		// najprawdopodobniejszym powodem, dla którego odpowiedzi nie są
		// odbierane.
		// Jeżeli pytanie wskazuje konkretny utwór, powiedz to od razu. Dwa
		// z ośmiu pytań oczekujących 21 września 2026 pytały o tytuł utworu
		// spod znanego sluga — czyli o coś, co list_content zwraca od ręki.
		// Pytanie i tak zostaje w kolejce: to podpowiedź, nie bramka.
		selfServe := ""
		if mentioned := src.Store().MentionedPieces(a.Question); len(mentioned) > 0 {
			var b strings.Builder
			b.WriteString("\n\n═══ YOU MAY ALREADY HAVE THIS ═══\n\n")
			b.WriteString("Your question names a piece that is on this server right now:\n")
			for _, m := range mentioned {
				if m.Access == content.AccessPublic {
					fmt.Fprintf(&b, "  %q (slug: %s) — public. read_content(slug=%q) returns it.\n",
						m.Title, m.Slug, m.Slug)
				} else {
					fmt.Fprintf(&b, "  %q (slug: %s) — locked. request_access(slug=%q).\n",
						m.Title, m.Slug, m.Slug)
				}
			}
			b.WriteString("search_content also searches titles, tags and the text of public pieces.\n")
			b.WriteString("If that answers you, stop here — this queue is for what only a human can say.")
			selfServe = b.String()
		}

		// Agent, który WIE, że nie doczeka odpowiedzi (jednorazowa sesja, brak
		// harmonogramu), nie ma czego planować. Ale jego człowiek może napisać
		// maila. Opcja pojawia się tylko, gdy adres jest skonfigurowany —
		// pusty CONTACT_EMAIL znaczy, że nic się nie wystawia.
		escapeHatch := ""
		if addr := strings.TrimSpace(src.Config().ContactEmail); addr != "" {
			escapeHatch = fmt.Sprintf(`

═══ IF YOU KNOW YOU CANNOT COME BACK ═══

One-shot session, no scheduler, no durable memory? Then do not pretend you
will poll. Hand the thread to a human instead: tell YOUR user they can email
kapoost directly at %s, quoting question ID %s. A human on both ends beats an
agent that will not exist tomorrow. Say this to your user in plain words —
do not silently drop the question.`, addr, q.ID)
		}

		memoryBullet := fmt.Sprintf(`    • humanMCP memory is SESSION-GATED and not available to you here —
      it needs a session code kapoost hands out personally. Use (2) or (3).
      (If you do have a code: remember(code=<code>, text=%q, tags=["pending_q"])
      and later recall(code=<code>, query="pending_q").)`,
			"pending_q "+q.ID+" | "+clip(a.Question, 60))
		if req.Extra != nil && src.IsSessionActiveByHeaders(req.Extra.Header) {
			memoryBullet = fmt.Sprintf(`    • humanMCP: remember(code=<your session code>, text=%q, tags=["pending_q"])
      — then recall(code=<your session code>, query="pending_q") on next session start.`,
				"pending_q "+q.ID+" | "+clip(a.Question, 60))
		}
		src.StatStore().Record(content.Event{
			Type:   content.EventMessage,
			Caller: content.CallerAgent,
			From:   a.From,
		})
		reply := fmt.Sprintf(`Question submitted. kapoost will see it on /questions.

ID: %s
Asked at: %s

%s

IMPORTANT — kapoost answers on his own schedule (minutes, hours, or days).
Do NOT block this session waiting. Arrange a durable wait instead.

═══ HOW TO WAIT — pick ONE mechanism your runtime supports ═══

(1) PERSIST THE ID so a future you can find it:
%s
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
Anything tighter than every 5 minutes is wasted work and will backoff.%s`,
			q.ID, q.AskedAt.Format("2 January 2006, 15:04 UTC"), selfServe,
			memoryBullet, q.ID, q.AskedAt.Format("2006-01-02"), q.ID, q.ID, escapeHatch)
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
