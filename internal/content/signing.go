package content

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// KeyPair holds an Ed25519 signing keypair
type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateKeyPair creates a new Ed25519 keypair
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{PublicKey: pub, PrivateKey: priv}, nil
}

// PublicKeyHex returns the public key as a hex string (safe to publish)
func (kp *KeyPair) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey)
}

// PrivateKeyBase64 returns the private key as base64 (store as secret)
func (kp *KeyPair) PrivateKeyBase64() string {
	return base64.StdEncoding.EncodeToString(kp.PrivateKey)
}

// KeyPairFromBase64 loads a keypair from a base64-encoded private key
func KeyPairFromBase64(privBase64 string) (*KeyPair, error) {
	privBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privBase64))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key length: got %d, want %d", len(privBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return &KeyPair{PublicKey: pub, PrivateKey: priv}, nil
}

// PublicKeyFromHex loads just a public key from hex (for verification only)
func PublicKeyFromHex(hexStr string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexStr))
	if err != nil {
		return nil, fmt.Errorf("invalid public key hex: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length")
	}
	return ed25519.PublicKey(b), nil
}

// SignPiece signs a piece's content and returns a base64 signature.
// The signed payload is: sha256(slug + title + body)
func SignPiece(p *Piece, kp *KeyPair) (string, error) {
	payload := piecePayload(p)
	sig := ed25519.Sign(kp.PrivateKey, payload)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyPiece checks a piece's signature against a public key.
// Returns true if valid, plus a human-readable status.
func VerifyPiece(p *Piece, pubKeyHex string) (bool, string) {
	if p.Signature == "" {
		return false, "unsigned — this piece has no signature"
	}
	pub, err := PublicKeyFromHex(pubKeyHex)
	if err != nil {
		return false, "invalid public key"
	}
	sigBytes, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return false, "malformed signature"
	}
	payload := piecePayload(p)
	if !ed25519.Verify(pub, payload, sigBytes) {
		return false, "invalid signature — content may have been modified"
	}
	return true, "verified — signed by kapoost's key"
}

// piecePayload builds the canonical bytes to sign: sha256(slug|title|body)
func piecePayload(p *Piece) []byte {
	canonical := p.Slug + "|" + p.Title + "|" + p.Body
	hash := sha256.Sum256([]byte(canonical))
	return hash[:]
}
