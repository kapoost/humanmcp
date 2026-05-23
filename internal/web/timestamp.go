package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ─── OpenTimestamps binary format constants ────────────────────────────────
//
// Reference: https://github.com/opentimestamps/python-opentimestamps
//
// .ots file layout:
//   <31-byte magic> <version byte> <file_hash_op> <32-byte digest> <timestamp body>
//
// The timestamp body is a sequence of ops applied to the digest, ending in
// attestations. Multiple attestations are separated with the 0xff marker.

const (
	otsMagic        = "\x00OpenTimestamps\x00\x00Proof\x00\xbf\x89\xe2\xe8\x84\xe8\x92\x94"
	otsVersion      = byte(0x01)
	otsMagicLen     = 31
	otsMaxFileSize  = 1 << 20 // 1 MB sanity cap
	otsHTTPTimeout  = 25 * time.Second
)

// Ops
const (
	opAppend  = 0xf0
	opPrepend = 0xf1
	opReverse = 0xf2
	opHexlify = 0xf3
	opSHA1    = 0x02
	opRIPEMD  = 0x03
	opSHA256  = 0x08
	opKeccak  = 0x67
)

// Attestation tag prefix
const tagAttestation = 0x00

// Attestation type tags (8 bytes each)
var (
	tagPending = [8]byte{0x83, 0xdf, 0xe3, 0x0d, 0x2e, 0xf9, 0x0c, 0x8e}
	tagBitcoin = [8]byte{0x05, 0x88, 0x96, 0x0d, 0x73, 0xd7, 0x19, 0x01}
)

// Default calendars. Same set the OTS reference client ships with.
var defaultCalendars = []string{
	"https://alice.btc.calendar.opentimestamps.org",
	"https://bob.btc.calendar.opentimestamps.org",
	"https://finney.calendar.eternitywall.com",
}

// ─── HTTP I/O with calendars ───────────────────────────────────────────────

func stampDigest(ctx context.Context, calendar string, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("digest must be 32 bytes, got %d", len(digest))
	}
	req, err := http.NewRequestWithContext(ctx, "POST", calendar+"/digest", bytes.NewReader(digest))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/vnd.opentimestamps.v1")
	req.Header.Set("User-Agent", "humanmcp-ots/1.0")
	client := &http.Client{Timeout: otsHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("calendar %s: HTTP %d: %s", calendar, resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, otsMaxFileSize))
}

func fetchUpgrade(ctx context.Context, calendar string, commitment []byte) ([]byte, error) {
	url := calendar + "/timestamp/" + hex.EncodeToString(commitment)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.opentimestamps.v1")
	req.Header.Set("User-Agent", "humanmcp-ots/1.0")
	client := &http.Client{Timeout: otsHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errPendingNotReady
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("calendar %s: HTTP %d: %s", calendar, resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, otsMaxFileSize))
}

var errPendingNotReady = errors.New("not yet anchored — calendar has no Bitcoin attestation for this commitment yet")

// ─── .ots file construction ────────────────────────────────────────────────

// buildOTSFile creates a complete .ots file: magic + version + file_hash_op +
// digest + bodies (joined by 0xff if multiple).
func buildOTSFile(digest []byte, bodies [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(otsMagic)
	buf.WriteByte(otsVersion)
	buf.WriteByte(opSHA256)
	buf.Write(digest)
	for i, b := range bodies {
		if i > 0 {
			buf.WriteByte(0xff)
		}
		buf.Write(b)
	}
	return buf.Bytes()
}

// splitOTS returns the digest and the timestamp body (everything after the digest).
func splitOTS(data []byte) (digest []byte, body []byte, err error) {
	if len(data) < otsMagicLen+1+1+32 {
		return nil, nil, errors.New("ots file too short")
	}
	if string(data[:otsMagicLen]) != otsMagic {
		return nil, nil, errors.New("ots file: bad magic")
	}
	if data[otsMagicLen] != otsVersion {
		return nil, nil, fmt.Errorf("ots file: unsupported version 0x%02x", data[otsMagicLen])
	}
	if data[otsMagicLen+1] != opSHA256 {
		return nil, nil, fmt.Errorf("ots file: unsupported file hash op 0x%02x", data[otsMagicLen+1])
	}
	digest = data[otsMagicLen+2 : otsMagicLen+2+32]
	body = data[otsMagicLen+2+32:]
	return digest, body, nil
}

// ─── Status detection ──────────────────────────────────────────────────────

// otsStatusOf decodes a base64 proof and reports "none" | "pending" | "anchored".
func otsStatusOf(b64 string) string {
	if b64 == "" {
		return "none"
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "pending"
	}
	if bytes.Contains(raw, tagBitcoin[:]) {
		return "anchored"
	}
	return "pending"
}

// ─── VLQ (variable-length quantity) — OTS uses big-endian "more bit" encoding ──

func readVLQ(b []byte) (val uint64, n int, err error) {
	for n < len(b) {
		c := b[n]
		val = (val << 7) | uint64(c&0x7f)
		n++
		if c&0x80 == 0 {
			return val, n, nil
		}
		if n > 9 {
			return 0, 0, errors.New("vlq too long")
		}
	}
	return 0, 0, errors.New("vlq truncated")
}

// ─── Recursive Timestamp parser ────────────────────────────────────────────
//
// OTS timestamp tree (per python-opentimestamps Timestamp._serialize):
//   - At each node, write items separated by 0xff (between items, not after last).
//   - Each item is either an attestation (\x00 + tag[8] + vlq(len) + payload)
//     or an op (op-byte [+ operand] + recursive child Timestamp).
//
// walkAndUpgrade copies the tree to out, attempting to replace each
// pending-attestation item with the result of upgrade(calendarURL, currentDigest).

type upgradeFn func(calendarURL string, commitment []byte) ([]byte, error)

type walker struct {
	buf      []byte
	pos      int
	out      *bytes.Buffer
	upgrade  upgradeFn
	upgraded int
}

func walkAndUpgrade(body, current []byte, upgrade upgradeFn) ([]byte, int, error) {
	w := &walker{buf: body, out: &bytes.Buffer{}, upgrade: upgrade}
	if err := w.parseTimestamp(current); err != nil {
		return nil, 0, err
	}
	if w.pos != len(w.buf) {
		// Trailing bytes — tolerate but don't error; could be padding from a
		// non-conforming calendar. Append as-is.
		w.out.Write(w.buf[w.pos:])
	}
	return w.out.Bytes(), w.upgraded, nil
}

func (w *walker) peek() (byte, error) {
	if w.pos >= len(w.buf) {
		return 0, errors.New("unexpected end of timestamp")
	}
	return w.buf[w.pos], nil
}

func (w *walker) read(n int) ([]byte, error) {
	if w.pos+n > len(w.buf) {
		return nil, errors.New("truncated")
	}
	b := w.buf[w.pos : w.pos+n]
	w.pos += n
	return b, nil
}

func (w *walker) parseTimestamp(current []byte) error {
	for {
		b, err := w.peek()
		if err != nil {
			return err
		}
		hasMore := false
		if b == 0xff {
			w.out.WriteByte(0xff)
			w.pos++
			hasMore = true
		}
		if err := w.parseItem(current); err != nil {
			return err
		}
		if !hasMore {
			return nil
		}
	}
}

func (w *walker) parseItem(current []byte) error {
	tag, err := w.peek()
	if err != nil {
		return err
	}
	if tag == tagAttestation {
		w.pos++
		return w.parseAttestation(current)
	}
	// Otherwise it's an op
	return w.parseOp(current)
}

func (w *walker) parseAttestation(current []byte) error {
	tagBytes, err := w.read(8)
	if err != nil {
		return fmt.Errorf("attestation tag: %w", err)
	}
	var tag [8]byte
	copy(tag[:], tagBytes)
	plen, n, err := readVLQ(w.buf[w.pos:])
	if err != nil {
		return fmt.Errorf("attestation length: %w", err)
	}
	w.pos += n
	payload, err := w.read(int(plen))
	if err != nil {
		return fmt.Errorf("attestation payload: %w", err)
	}

	if tag == tagPending && w.upgrade != nil {
		urlLen, m, uerr := readVLQ(payload)
		if uerr == nil && int(m)+int(urlLen) <= len(payload) {
			calURL := string(payload[m : m+int(urlLen)])
			newBody, err := w.upgrade(calURL, current)
			if err == nil && len(newBody) > 0 {
				// Splice in the upgrade response — it's a full timestamp body
				// starting from `current`. It already includes its own attestations.
				w.out.Write(newBody)
				w.upgraded++
				return nil
			}
		}
	}
	// Keep attestation as-is
	w.out.WriteByte(tagAttestation)
	w.out.Write(tag[:])
	w.out.Write(encodeVLQ(plen))
	w.out.Write(payload)
	return nil
}

func (w *walker) parseOp(current []byte) error {
	op, err := w.read(1)
	if err != nil {
		return err
	}
	w.out.Write(op)
	o := op[0]
	switch o {
	case opSHA1, opRIPEMD, opSHA256, opKeccak, opReverse, opHexlify:
		// unary ops — no operand
		next := applyOp(o, current, nil)
		return w.parseTimestamp(next)
	case opAppend, opPrepend:
		operandLen, n, err := readVLQ(w.buf[w.pos:])
		if err != nil {
			return fmt.Errorf("operand length: %w", err)
		}
		w.pos += n
		w.out.Write(encodeVLQ(operandLen))
		operand, err := w.read(int(operandLen))
		if err != nil {
			return fmt.Errorf("operand bytes: %w", err)
		}
		w.out.Write(operand)
		next := applyOp(o, current, operand)
		return w.parseTimestamp(next)
	default:
		return fmt.Errorf("unknown op 0x%02x at offset %d", o, w.pos-1)
	}
}

func applyOp(op byte, current, operand []byte) []byte {
	switch op {
	case opSHA256:
		h := sha256.Sum256(current)
		return h[:]
	case opAppend:
		out := make([]byte, 0, len(current)+len(operand))
		return append(append(out, current...), operand...)
	case opPrepend:
		out := make([]byte, 0, len(current)+len(operand))
		return append(append(out, operand...), current...)
	case opReverse:
		out := make([]byte, len(current))
		for i, b := range current {
			out[len(current)-1-i] = b
		}
		return out
	case opHexlify:
		return []byte(hex.EncodeToString(current))
	}
	// Unsupported hash op — return a deterministic-but-wrong placeholder. The
	// upgrade RPC will simply fail to match, which is fine — we'll keep the
	// pending attestation rather than replace it.
	return make([]byte, 32)
}

func encodeVLQ(v uint64) []byte {
	if v == 0 {
		return []byte{0}
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte(v & 0x7f)}, buf...)
		v >>= 7
	}
	for i := 0; i < len(buf)-1; i++ {
		buf[i] |= 0x80
	}
	return buf
}

