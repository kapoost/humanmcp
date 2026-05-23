package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// Synthetic body: D → prepend(nonce1) → sha256 → pending(alice)
//                  → prepend(nonce2) → sha256 → pending(bob)
func TestWalkRoundTrip_NoUpgrade(t *testing.T) {
	digest := sha256.Sum256([]byte("hello"))

	body := buildSyntheticBody(t)
	out, count, err := walkAndUpgrade(body, digest[:], func(cal string, commit []byte) ([]byte, error) {
		return nil, errPendingNotReady
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count != 0 {
		t.Fatalf("upgrade count=%d want 0", count)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("round-trip mismatch:\nwant %x\ngot  %x", body, out)
	}
}

func TestWalkUpgrade_SplicesIn(t *testing.T) {
	digest := sha256.Sum256([]byte("hello"))
	body := buildSyntheticBody(t)

	// Replacement body: a single attestation (bitcoin marker)
	replacement := []byte{tagAttestation}
	replacement = append(replacement, tagBitcoin[:]...)
	replacement = append(replacement, encodeVLQ(0)...)

	out, count, err := walkAndUpgrade(body, digest[:], func(cal string, commit []byte) ([]byte, error) {
		return replacement, nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count != 2 {
		t.Fatalf("upgrade count=%d want 2", count)
	}
	if !bytes.Contains(out, tagBitcoin[:]) {
		t.Fatalf("expected bitcoin tag in output")
	}
}

func TestOTSStatus(t *testing.T) {
	// Empty
	if otsStatusOf("") != "none" {
		t.Errorf("empty: want none")
	}
	// Pending body (only pending tag)
	pending := buildSyntheticBody(t)
	pendingFile := buildOTSFile(make([]byte, 32), [][]byte{pending})
	if otsStatusOf(base64.StdEncoding.EncodeToString(pendingFile)) != "pending" {
		t.Errorf("pending: want pending")
	}
	// Anchored — manually inject bitcoin tag
	anchored := append([]byte{}, pending...)
	anchored = append(anchored, tagBitcoin[:]...)
	anchoredFile := buildOTSFile(make([]byte, 32), [][]byte{anchored})
	if otsStatusOf(base64.StdEncoding.EncodeToString(anchoredFile)) != "anchored" {
		t.Errorf("anchored: want anchored")
	}
}

// buildSyntheticBody returns: prepend(0xaa) sha256 pending(alice) 0xff prepend(0xbb) sha256 pending(bob)
func buildSyntheticBody(t *testing.T) []byte {
	t.Helper()
	pendingAlice := pendingPayload("https://alice.example/")
	pendingBob := pendingPayload("https://bob.example/")

	var b bytes.Buffer
	// branch 1
	b.WriteByte(0xff)
	b.WriteByte(opPrepend)
	b.Write(encodeVLQ(1))
	b.WriteByte(0xaa)
	b.WriteByte(opSHA256)
	b.WriteByte(tagAttestation)
	b.Write(tagPending[:])
	b.Write(encodeVLQ(uint64(len(pendingAlice))))
	b.Write(pendingAlice)
	// branch 2 (last — no 0xff prefix)
	b.WriteByte(opPrepend)
	b.Write(encodeVLQ(1))
	b.WriteByte(0xbb)
	b.WriteByte(opSHA256)
	b.WriteByte(tagAttestation)
	b.Write(tagPending[:])
	b.Write(encodeVLQ(uint64(len(pendingBob))))
	b.Write(pendingBob)
	return b.Bytes()
}

func pendingPayload(url string) []byte {
	var b bytes.Buffer
	b.Write(encodeVLQ(uint64(len(url))))
	b.WriteString(url)
	return b.Bytes()
}

