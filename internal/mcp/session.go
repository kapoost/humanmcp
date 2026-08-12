package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// SessionTokenTTL is how long a bootstrap_session token stays valid.
// Matches wave-1 in-memory session TTL. Short by design — session tokens
// are shared over the wire (Authorization: Bearer, visible in access
// logs / proxy dumps) so a 1h leak window bounds blast radius.
const SessionTokenTTL = time.Hour

// GenerateSessionToken creates a stateless v2 session token. Format:
//
//	"<expiry_unix>.<hmac_hex>"
//
// where hmac = HMAC-SHA256(str(expiry_unix), sessionSecret). No server-
// side state needed for validation — the token encodes its own expiry
// and proves origin via HMAC. Revocation is coarse (rotate
// SessionSecret → all outstanding tokens invalidated); fine-grained
// revocation would need a backing store which wave 3 doesn't
// require. See ADR-0001 wave 3 W4 for the analogous design
// principle applied to friend tokens.
//
// The HMAC covers only the expiry string — no session id / user id /
// scopes are encoded. Session tokens exclusively mean "this caller
// did bootstrap_session with a valid poet code recently", nothing
// more. Downstream code (IsSessionActiveByHeaders) uses the token as
// a boolean signal for members-tier access.
//
// If sessionSecret is empty, returns empty string — caller must check.
// This lets deployments without SessionSecret configured degrade to
// wave-1 behavior (session tokens don't work; owner + friend tokens
// still do).
func GenerateSessionToken(sessionSecret string) string {
	if sessionSecret == "" {
		return ""
	}
	expiry := time.Now().Add(SessionTokenTTL).Unix()
	payload := strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// ValidateSessionToken returns true if the token is well-formed, its
// HMAC verifies against sessionSecret, and its expiry is in the
// future. Constant-time HMAC compare — malformed / forged / expired
// tokens all fail identically (no timing side-channel that would let
// an attacker distinguish "unknown key" vs "wrong signature").
func ValidateSessionToken(token, sessionSecret string) bool {
	if token == "" || sessionSecret == "" {
		return false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expiryStr, macHex := parts[0], parts[1]
	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	mac.Write([]byte(expiryStr))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(macHex), []byte(expected)) {
		return false
	}
	return time.Now().Unix() < expiryUnix
}
