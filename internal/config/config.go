package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
