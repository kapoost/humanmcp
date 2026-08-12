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

// CheckFriendTokenRateLimit enforces a per-token sliding 1h window via the
// shared ratelimit.Bucket. Owner (tokenID=="owner" or "") bypasses.
// Returns (allowed, retryAfterSecs) — retryAfterSecs is the time until the
// oldest hit in the current window falls off. Zero when allowed.
//
// Called by mysloodsiewnia_* tools right before scope check, so a burn
// attack costs the token its budget but never reaches the vault.
//
// Wave 3 Z4 fallback: if per-token limit is unset (<=0), the bucket's
// default limit (30/hr) applies via AllowWithLimit.
func (h *Handler) CheckFriendTokenRateLimit(tokenID string, limitPerHour int) (allowed bool, retryAfterSecs int) {
	if tokenID == "" || tokenID == ownerTokenID {
		return true, 0
	}
	return h.friendTokenBucket.AllowWithLimit(tokenID, limitPerHour)
}
