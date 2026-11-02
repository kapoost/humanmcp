package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kapoost/humanmcp-go/internal/krag"
)

// notAKrag is the single response for every failure mode: unknown phrase,
// expired token, feature disabled, empty bundle, rate-limited guesser. It must
// stay byte-identical across all of them — a different status code, a longer
// body or a faster reply would each confirm a guess and turn the endpoint into
// an oracle. Same discipline as mcp.AuthorizeRequestByHeaders (ADR-0001 W4/Z3).
const notAKrag = "Nie ma takiego kręgu.\n"

// handleKrag serves GET /krag/<fraza> — one line of poetry in, a paste-ready
// markdown bundle of personas and skills out. No Authorization header, no MCP
// registration, no client config: the point is that a friend can run exactly
// one curl.
//
// The phrase is never written to a log line here. It is the entire credential,
// and access logs are the one place it would otherwise accumulate in clear.
func (h *Handler) handleKrag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit before resolving, so a guesser spends their budget without
	// ever reaching the token comparison.
	if allowed, _ := h.kragBucket.Allow(h.contactClientIP(r)); !allowed {
		writeKragMiss(w)
		return
	}

	phrase := strings.TrimPrefix(r.URL.Path, "/krag/")
	if phrase == "" || strings.Contains(phrase, "/") {
		writeKragMiss(w)
		return
	}

	bundle, ok := krag.Resolve(h.cfg, phrase)
	if !ok {
		writeKragMiss(w)
		return
	}

	body := krag.Render(bundle, h.cfg.AuthorName, h.cfg.Domain)

	// text/plain, not text/markdown: the friend is expected to curl this and
	// copy it, and every terminal and browser renders plain text predictably.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(body))
}

func writeKragMiss(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(notAKrag))
}
