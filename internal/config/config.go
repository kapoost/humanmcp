package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	// Server
	Host   string `json:"host"`
	Port   string `json:"port"`
	Domain     string `json:"domain"`
	AIMetadata bool   `json:"ai_metadata"`

	// Author
	AuthorName    string `json:"author_name"`
	AuthorBio     string `json:"author_bio"`
	AuthorAvatar  string `json:"author_avatar"`

	// Content
	ContentDir string `json:"content_dir"`

	// Auth — owner keypair (base64 encoded)
	OwnerPublicKey  string `json:"owner_public_key"`
	OwnerPrivateKey string `json:"owner_private_key"`

	// Edit token
	EditToken  string `json:"edit_token"`
	AgentToken string `json:"agent_token"`

	// Session secret — for machine auth (Fly secret, rotated locally)
	SessionSecret string `json:"session_secret"`

	// Rotating poet passwords — HMAC-selected from pool, 1h TTL
	PoetPool   []string `json:"-"` // parsed from POET_POOL env (base64-encoded JSON array)
	PoetSecret string   `json:"-"` // POET_SECRET env — HMAC key for poem selection

	// Ed25519 signing keypair (base64 private key, hex public key)
	SigningPrivateKey string `json:"signing_private_key"`
	SigningPublicKey  string `json:"signing_public_key"`

	// Claude API — used by narada worker to generate persona voices.
	// Empty key falls back to a stub voice so the async plumbing still works
	// (useful for local dev and CI).
	ClaudeAPIKey string `json:"-"`

	// VaultBridgeToken authenticates the mysłoodsiewnia vault when it
	// polls /mysloodsiewnia/pending-ops, heartbeats, and posts back
	// completed operations. Shared secret over TLS; empty ⇒ bridge disabled.
	// Rotate quarterly (see docs/adr/0001-mysloodsiewnia-bridge.md).
	VaultBridgeToken string `json:"-"`

	// FriendTokens are named, scoped, expirable, rate-limited tokens that
	// grant read access to mysloodsiewnia_* tools. Wave 3 sharing —
	// see docs/adr/0001-mysloodsiewnia-bridge.md sekcja "Wave 3 —
	// sharing / friend tokens". Keyed by slug (tokenID). Loaded from
	// FRIEND_TOKENS_JSON env (base64-encoded JSON of the map) — never
	// from disk, never committed. Empty map ⇒ only EditToken works.
	FriendTokens map[string]*FriendTokenSpec `json:"-"`
}

// FriendTokenSpec is one named friend-token entry. Wave 3 read-only sharing
// (ADR-0001). Token is the opaque bearer value the caller sends; the slug
// (map key) is what's logged and propagated to the vault for scope + audit.
// ExpiresAt is required (zero-value = never = rejected — horcrux tokens
// live under a separate ADR).
type FriendTokenSpec struct {
	Token            string    `json:"token"`
	Scopes           []string  `json:"scopes"`
	RateLimitPerHour int       `json:"rate_limit_per_hour"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Host:       "0.0.0.0",
		Port:       "8080",
		Domain:     "localhost:8080",
		AuthorName: "Anonymous",
		AuthorBio:  "A human with something to say.",
		ContentDir: "./content",
		EditToken:  os.Getenv("EDIT_TOKEN"),
	}

	// Override from env vars (12-factor)
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("DOMAIN"); v != "" {
		cfg.Domain = v
	}
	if v := os.Getenv("AI_METADATA"); v == "true" {
		cfg.AIMetadata = true
	}
	if v := os.Getenv("AUTHOR_NAME"); v != "" {
		cfg.AuthorName = v
	}
	if v := os.Getenv("AUTHOR_BIO"); v != "" {
		cfg.AuthorBio = v
	}
	if v := os.Getenv("CONTENT_DIR"); v != "" {
		cfg.ContentDir = v
	}
	if v := os.Getenv("AGENT_TOKEN"); v != "" {
		cfg.AgentToken = v
	}
	if v := os.Getenv("SESSION_SECRET"); v != "" {
		cfg.SessionSecret = v
	}
	if v := os.Getenv("SIGNING_PRIVATE_KEY"); v != "" {
		cfg.SigningPrivateKey = v
	}
	if v := os.Getenv("SIGNING_PUBLIC_KEY"); v != "" {
		cfg.SigningPublicKey = v
	}
	if v := os.Getenv("POET_SECRET"); v != "" {
		cfg.PoetSecret = v
	}
	if v := os.Getenv("CLAUDE_API_KEY"); v != "" {
		cfg.ClaudeAPIKey = v
	}
	if v := os.Getenv("VAULT_BRIDGE_TOKEN"); v != "" {
		cfg.VaultBridgeToken = v
	}
	// Wave 3 sharing / friend tokens. FRIEND_TOKENS_JSON is a
	// base64-encoded JSON blob mapping slug → FriendTokenSpec. Absent /
	// empty ⇒ friend tokens disabled (owner-only, wave 1 behavior). Invalid
	// JSON logs a warning and is treated as empty (defense in depth: a
	// misformatted blob must not fall open by exposing everything).
	// Rotate whole blob at once — see incident playbook wave 3 section.
	if v := os.Getenv("FRIEND_TOKENS_JSON"); v != "" {
		if tokens, err := parseFriendTokensBlob(v); err != nil {
			// Log via os.Stderr — this file doesn't import a logger yet
			// and adding one just for this path is more noise than value.
			fmt.Fprintf(os.Stderr, "[config] FRIEND_TOKENS_JSON parse failed: %v — friend tokens disabled\n", err)
		} else {
			cfg.FriendTokens = tokens
		}
	}
	if v := os.Getenv("POET_POOL"); v != "" {
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			var pool []string
			if err := json.Unmarshal(decoded, &pool); err == nil {
				cfg.PoetPool = pool
			}
		}
	}

	// Load from config.json if present
	cfgPath := "config.json"
	if _, err := os.Stat(cfgPath); err == nil {
		data, err := os.ReadFile(cfgPath)
		if err == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}

	// Resolve content dir
	abs, err := filepath.Abs(cfg.ContentDir)
	if err == nil {
		cfg.ContentDir = abs
	}

	return cfg, nil
}

// PickActivePoem returns the current and previous hour's poem from PoetPool.
// Returns ("", "") if pool is empty or secret is missing.
func (c *Config) PickActivePoem(now time.Time) (current string, previous string) {
	if len(c.PoetPool) == 0 || c.PoetSecret == "" {
		return "", ""
	}
	hourKey := now.Unix() / 3600
	current = poemForHour(c.PoetPool, c.PoetSecret, hourKey)
	previous = poemForHour(c.PoetPool, c.PoetSecret, hourKey-1)
	return current, previous
}

// parseFriendTokensBlob decodes the base64-encoded JSON map of friend
// tokens loaded from FRIEND_TOKENS_JSON. Fails hard on decode error so a
// misconfigured blob doesn't fall open (returns nil map + error rather
// than a partial map with fewer entries than expected).
func parseFriendTokensBlob(b64 string) (map[string]*FriendTokenSpec, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var out map[string]*FriendTokenSpec
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	// Reject any spec with expires_at zero (unbounded TTL — that is horcrux
	// territory, see ADR-0001 sekcja "Horcrux vs sharing token" Z6). Missing
	// expires_at ⇒ token silently dropped and event logged; the deployment
	// keeps working with the rest.
	for slug, spec := range out {
		if spec == nil || spec.ExpiresAt.IsZero() {
			fmt.Fprintf(os.Stderr, "[config] FRIEND_TOKENS_JSON: dropping slug %q — expires_at required (Z6 horcrux ban)\n", slug)
			delete(out, slug)
		}
	}
	return out, nil
}

func poemForHour(pool []string, secret string, hourKey int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(hourKey))
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	idx := new(big.Int).SetBytes(sum[:8])
	idx.Mod(idx, big.NewInt(int64(len(pool))))
	return pool[idx.Int64()]
}
