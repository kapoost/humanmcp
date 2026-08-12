package mcp

import (
	"net/http"
	"strings"
	"time"
)

// ownerTokenID is the reserved slug for any request bearing the edit / agent /
// session token. Friend tokens must never claim this value — validated at
// config load time in a future task; for now, kapoost owns the JSON file.
const ownerTokenID = "owner"

// AuthorizeRequestByHeaders resolves a request's bearer credential and
// returns (tokenID, grantedScopes, ok). Contract:
//
//   - Owner path (EditToken / AgentToken / SessionSecret): returns
//     ("owner", nil, true). nil scopes = unlimited; callers MUST treat
//     tokenID=="owner" as bypassing scope + rate-limit checks.
//   - Valid friend token (present, not expired): returns
//     (slug, spec.Scopes copy, true).
//   - Unknown / malformed / expired / missing bearer: returns
//     ("", nil, false) — identical shape for all four so nothing leaks
//     the existence, past-existence, or scope shape of friend tokens.
//     (Wave 3 W4 + Z3 pin — see ADR-0001.)
func (h *Handler) AuthorizeRequestByHeaders(hdr http.Header) (tokenID string, scopes []string, ok bool) {
	if h.IsOwnerRequestByHeaders(hdr) {
		return ownerTokenID, nil, true
	}
	authHeader := hdr.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", nil, false
	}
	presented := strings.TrimPrefix(authHeader, "Bearer ")
	if presented == "" {
		return "", nil, false
	}
	if h.cfg == nil || len(h.cfg.FriendTokens) == 0 {
		return "", nil, false
	}
	now := time.Now()
	for slug, spec := range h.cfg.FriendTokens {
		if spec == nil || spec.Token == "" {
			continue
		}
		if spec.Token != presented {
			continue
		}
		if !spec.ExpiresAt.IsZero() && now.After(spec.ExpiresAt) {
			return "", nil, false
		}
		out := make([]string, len(spec.Scopes))
		copy(out, spec.Scopes)
		return slug, out, true
	}
	return "", nil, false
}

// CheckFriendTokenRateLimit enforces a per-token sliding 1h window. Owner
// (tokenID=="owner" or "") bypasses. Returns (allowed, retryAfterSecs).
// retryAfterSecs is the time until the oldest hit in the current window
// falls off — so the caller knows exactly when they can retry. Zero when
// allowed.
//
// Called by mysloodsiewnia_* tools right before scope check, so a burn
// attack costs the token its budget but never reaches the vault.
func (h *Handler) CheckFriendTokenRateLimit(tokenID string, limitPerHour int) (allowed bool, retryAfterSecs int) {
	if tokenID == "" || tokenID == ownerTokenID {
		return true, 0
	}
	if limitPerHour <= 0 {
		// Z4 fallback: config missing → 30 req/hr (conservative — easier
		// to loosen per-token than to tighten mid-incident).
		limitPerHour = 30
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	window := time.Hour
	cutoff := now.Add(-window)
	times := h.friendTokenLog[tokenID]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limitPerHour {
		oldest := kept[0]
		retry := int(oldest.Add(window).Sub(now).Seconds()) + 1
		if retry < 1 {
			retry = 1
		}
		h.friendTokenLog[tokenID] = kept
		return false, retry
	}
	kept = append(kept, now)
	h.friendTokenLog[tokenID] = kept
	return true, 0
}
