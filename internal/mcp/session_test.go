package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenerateAndValidateSessionToken(t *testing.T) {
	tok := GenerateSessionToken("secret")
	if tok == "" {
		t.Fatal("GenerateSessionToken returned empty")
	}
	if !strings.Contains(tok, ".") {
		t.Fatalf("expected dot-separated token, got %q", tok)
	}
	if !ValidateSessionToken(tok, "secret") {
		t.Fatal("freshly-generated token should validate")
	}
}

func TestSessionTokenWrongSecretFails(t *testing.T) {
	tok := GenerateSessionToken("correct")
	if ValidateSessionToken(tok, "wrong") {
		t.Fatal("token forged for one secret must not validate under another")
	}
}

func TestSessionTokenMalformedRejected(t *testing.T) {
	cases := []string{
		"",
		"no-dot",
		"not-a-number.deadbeef",
		"9999999999.not-hex-!!!!",
		".deadbeef",
		"9999999999.",
	}
	for _, c := range cases {
		if ValidateSessionToken(c, "s") {
			t.Errorf("malformed token %q must be rejected", c)
		}
	}
}

func TestSessionTokenExpiredFails(t *testing.T) {
	// Hand-craft a token with expiry in the past — mirror GenerateSessionToken
	// logic but with a chosen expiry so we can control validation outcome.
	pastExpiry := time.Now().Add(-1 * time.Second).Unix()
	payload := strconv.FormatInt(pastExpiry, 10)
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write([]byte(payload))
	tok := payload + "." + hex.EncodeToString(mac.Sum(nil))
	if ValidateSessionToken(tok, "s") {
		t.Fatal("expired token must not validate")
	}
}

func TestSessionTokenEmptySecret(t *testing.T) {
	// Empty secret ⇒ generation returns "" ⇒ validation returns false.
	if tok := GenerateSessionToken(""); tok != "" {
		t.Fatal("empty secret should generate empty token")
	}
	if ValidateSessionToken("anything", "") {
		t.Fatal("empty secret should reject all tokens")
	}
}

func TestSessionTokenTTLHonored(t *testing.T) {
	// The default TTL should keep freshly-generated tokens valid for
	// meaningfully longer than test wallclock (avoid flakes on slow CI).
	if SessionTokenTTL < 10*time.Minute {
		t.Fatalf("SessionTokenTTL suspiciously short: %v", SessionTokenTTL)
	}
	tok := GenerateSessionToken("s")
	// The token's embedded expiry should be roughly now + TTL.
	parts := strings.SplitN(tok, ".", 2)
	expiryUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	expected := time.Now().Add(SessionTokenTTL).Unix()
	if diff := expected - expiryUnix; diff > 2 || diff < -2 {
		t.Errorf("expiry drift too large: expected ~%d, got %d (diff %d)", expected, expiryUnix, diff)
	}
}
