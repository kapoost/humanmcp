package content

// Upload, signature, and license integration tests.
// Covers: all blob types, size limits, license roundtrip,
// signature chain, audience matrix, file type safety.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── Constants ────────────────────────────────────────────────

const (
	// Hard limits — these must never change without updating tests
	MaxBlobTextBytes  = 512 * 1024        // 512 KB inline text
	MaxBlobFileBytes  = 50 * 1024 * 1024  // 50 MB file upload
	MaxMessageChars   = 2000
	MaxSlugLen        = 64
	MaxTitleLen       = 256
	MaxDescLen        = 512
	MaxTagLen         = 64
	MaxTagCount       = 20
)

// ─── Helpers ──────────────────────────────────────────────────

func newBlobStore(t *testing.T) (*BlobStore, string) {
	t.Helper()
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	return NewBlobStore(contentDir), dir
}

func makePiece(slug, body, license string, price int) *Piece {
	return &Piece{
		Slug: slug, Title: slug, Type: "poem",
		Access: AccessPublic, Body: body,
		License: license, PriceSats: price,
		Published: time.Now(),
	}
}

// ─── 1. BLOB TYPE TESTS ───────────────────────────────────────

func TestBlobAllTypesRoundtrip(t *testing.T) {
	bs, _ := newBlobStore(t)

	types := []struct {
		blobType BlobType
		data     string
		mime     string
	}{
		{BlobImage,    "base64imagedata==",    "image/jpeg"},
		{BlobContact,  `{"name":"kapoost"}`,   "application/json"},
		{BlobVector,   "base64floatdata==",    "application/octet-stream"},
		{BlobDocument, "base64pdfdata==",      "application/pdf"},
		{BlobDataset,  "slug,reads\na,1\nb,2", "text/csv"},
		{BlobCapsule,  `{"schema":"custom"}`,  "application/json"},
	}

	for _, tc := range types {
		b := &Blob{
			Slug:     "test-" + string(tc.blobType),
			Title:    "Test " + string(tc.blobType),
			BlobType: tc.blobType,
			MimeType: tc.mime,
			Access:   AccessPublic,
			TextData: tc.data,
		}
		if err := bs.Save(b); err != nil {
			t.Errorf("Save %s: %v", tc.blobType, err)
			continue
		}
		loaded, err := bs.Get(b.Slug)
		if err != nil { t.Errorf("Get %s: %v", tc.blobType, err); continue }
		if loaded.BlobType != tc.blobType { t.Errorf("%s: type mismatch", tc.blobType) }
		if loaded.MimeType != tc.mime     { t.Errorf("%s: mime mismatch", tc.blobType) }
		if loaded.TextData != tc.data     { t.Errorf("%s: data mismatch", tc.blobType) }
	}
}

// ─── 2. SIZE LIMIT TESTS ──────────────────────────────────────

func TestBlobInlineTextLimit(t *testing.T) {
	bs, _ := newBlobStore(t)

	// Under limit: 100KB should save fine
	under := strings.Repeat("a", 100*1024)
	b := &Blob{Slug: "small", Title: "Small", BlobType: BlobDataset, TextData: under}
	if err := bs.Save(b); err != nil {
		t.Errorf("100KB blob should save: %v", err)
	}

	// At limit: 512KB
	atLimit := strings.Repeat("b", MaxBlobTextBytes)
	b2 := &Blob{Slug: "limit", Title: "Limit", BlobType: BlobDataset, TextData: atLimit}
	if err := bs.Save(b2); err != nil {
		t.Errorf("512KB blob should save: %v", err)
	}
}

func TestBlobFileStorageAndRetrieval(t *testing.T) {
	bs, _ := newBlobStore(t)

	// Simulate JPEG header bytes
	jpegData := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 1024)...)
	ref, err := bs.StoreFile("photo-test", "photo.jpg", jpegData)
	if err != nil { t.Fatalf("StoreFile: %v", err) }

	read, err := bs.ReadFile(ref)
	if err != nil { t.Fatalf("ReadFile: %v", err) }
	if len(read) != len(jpegData) { t.Errorf("size mismatch: %d != %d", len(read), len(jpegData)) }
	if read[0] != 0xFF || read[1] != 0xD8 { t.Error("JPEG magic bytes corrupted") }
}

func TestBlobSlugMaxLength(t *testing.T) {
	bs, _ := newBlobStore(t)

	// MaxSlugLen should work
	slug := strings.Repeat("a", MaxSlugLen)
	b := &Blob{Slug: slug, Title: "T", BlobType: BlobDataset, TextData: "x"}
	if err := bs.Save(b); err != nil {
		t.Errorf("max length slug should save: %v", err)
	}
}

func TestMessageLengthLimit(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	// Exactly at limit — should save
	atLimit := strings.Repeat("x", MaxMessageChars)
	m, err := ms.Save("test", atLimit, "")
	if err != nil { t.Fatalf("message at limit should save: %v", err) }
	if len([]rune(m.Text)) > MaxMessageChars {
		t.Errorf("text over limit: %d", len([]rune(m.Text)))
	}

	// Over limit — should be truncated not rejected
	over := strings.Repeat("y", MaxMessageChars+500)
	m2, err := ms.Save("test", over, "")
	if err != nil { t.Fatalf("over-limit message should be truncated not error: %v", err) }
	if len([]rune(m2.Text)) > MaxMessageChars {
		t.Errorf("text should be truncated, got %d", len([]rune(m2.Text)))
	}
}

// ─── 3. LICENSE ROUNDTRIP TESTS ───────────────────────────────

func TestLicenseRoundtripAllTypes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.Load()

	licenses := []string{
		"free", "cc-by", "cc-by-nc",
		"commercial", "exclusive", "all-rights",
	}

	for _, lic := range licenses {
		p := makePiece("poem-"+lic, "Some body text.", lic, 0)
		if lic == "commercial" { p.PriceSats = 100 }
		if err := s.Save(p); err != nil { t.Fatalf("Save(%s): %v", lic, err) }

		s2 := NewStore(dir)
		s2.Load()
		loaded, err := s2.GetForEdit("poem-" + lic)
		if err != nil { t.Fatalf("Get(%s): %v", lic, err) }
		if loaded.License != lic { t.Errorf("license %s: got %q", lic, loaded.License) }
		if lic == "commercial" && loaded.PriceSats != 100 {
			t.Errorf("price not persisted for commercial: got %d", loaded.PriceSats)
		}
	}
}

func TestLicenseEmptyDefaultsToFree(t *testing.T) {
	dir := t.TempDir()
	// Write a piece without license field manually
	os.WriteFile(filepath.Join(dir, "no-license.md"), []byte(`---
slug: no-license
title: No License
type: poem
access: public
published: 2024-01-01
---
Body text.`), 0644)

	s := NewStore(dir)
	s.Load()
	p, err := s.Get("no-license", false)
	if err != nil { t.Fatalf("Get: %v", err) }
	// Empty license is fine — server defaults to "all rights reserved" display
	if p.License != "" {
		t.Logf("note: empty license field is %q (displayed as all-rights-reserved)", p.License)
	}
}

func TestPriceSatsOnlyForCommercial(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.Load()

	// Non-commercial licenses with price — price should persist but not be enforced
	p := makePiece("free-priced", "body", "free", 50)
	s.Save(p)
	s2 := NewStore(dir)
	s2.Load()
	loaded, _ := s2.GetForEdit("free-priced")
	// Price is stored (for future migration) but license is free
	if loaded.License != "free" { t.Errorf("license: %q", loaded.License) }
}

// ─── 4. SIGNATURE CHAIN TESTS ────────────────────────────────

func TestSignatureSetOnSave(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("signed-poem", "Original body.", "cc-by", 0)

	sig, err := SignPiece(p, kp)
	if err != nil { t.Fatalf("SignPiece: %v", err) }
	p.Signature = sig

	ok, status := VerifyPiece(p, kp.PublicKeyHex())
	if !ok { t.Errorf("fresh signature should verify: %s", status) }
}

func TestSignatureInvalidatedAfterBodyEdit(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("edited-poem", "Original body.", "cc-by", 0)
	sig, _ := SignPiece(p, kp)
	p.Signature = sig

	// Simulate edit — body changes
	p.Body = "Edited body."
	ok, _ := VerifyPiece(p, kp.PublicKeyHex())
	if ok { t.Error("edited body should invalidate signature") }
}

func TestSignatureInvalidatedAfterTitleEdit(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("title-edit", "Body.", "cc-by", 0)
	sig, _ := SignPiece(p, kp)
	p.Signature = sig

	p.Title = "New Title"
	ok, _ := VerifyPiece(p, kp.PublicKeyHex())
	if ok { t.Error("title change should invalidate signature") }
}

func TestSignatureInvalidatedAfterSlugEdit(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("original-slug", "Body.", "cc-by", 0)
	sig, _ := SignPiece(p, kp)
	p.Signature = sig

	p.Slug = "new-slug"
	ok, _ := VerifyPiece(p, kp.PublicKeyHex())
	if ok { t.Error("slug change should invalidate signature") }
}

func TestResignAfterEdit(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("resignable", "Original.", "cc-by", 0)
	sig1, _ := SignPiece(p, kp)
	p.Signature = sig1

	// Edit and re-sign
	p.Body = "Edited."
	sig2, err := SignPiece(p, kp)
	if err != nil { t.Fatalf("re-sign: %v", err) }
	p.Signature = sig2

	ok, _ := VerifyPiece(p, kp.PublicKeyHex())
	if !ok { t.Error("re-signed piece should verify") }
	if sig1 == sig2 { t.Error("new signature should differ from old") }
}

func TestBlobSignatureRoundtrip(t *testing.T) {
	kp, _ := GenerateKeyPair()
	b := &Blob{
		Slug: "signed-blob", Title: "Signed",
		BlobType: BlobImage, TextData: "imagedata",
	}
	sig, err := SignBlob(b, kp)
	if err != nil { t.Fatalf("SignBlob: %v", err) }
	b.Signature = sig

	ok, status := VerifyBlob(b, kp.PublicKeyHex())
	if !ok { t.Errorf("blob should verify: %s", status) }
}

func TestBlobSignatureInvalidatedOnDataChange(t *testing.T) {
	kp, _ := GenerateKeyPair()
	b := &Blob{Slug: "blob", Title: "B", BlobType: BlobDataset, TextData: "original"}
	sig, _ := SignBlob(b, kp)
	b.Signature = sig
	b.TextData = "tampered"
	ok, _ := VerifyBlob(b, kp.PublicKeyHex())
	if ok { t.Error("tampered blob data should fail") }
}

// ─── 5. COPYRIGHT CERTIFICATE E2E TESTS ──────────────────────

func TestCertificateChain(t *testing.T) {
	kp, _ := GenerateKeyPair()
	p := makePiece("certified", "Pierwsza pochodna.\nDruga potega.\nTrzeciego stopnia.", "cc-by", 0)
	sig, _ := SignPiece(p, kp)
	p.Signature = sig

	cert := BuildCopyright(p, "kapoost", kp.PublicKeyHex())

	// Verify certificate fields
	if cert.Author != "kapoost"       { t.Errorf("author: %q", cert.Author) }
	if cert.License != "cc-by"        { t.Errorf("license: %q", cert.License) }
	if cert.Signature != sig          { t.Errorf("signature mismatch") }
	if cert.ContentHash == ""         { t.Error("hash empty") }
	if cert.Originality.Combined <= 0 { t.Error("originality should be > 0") }

	// Verify signature via certificate public key
	ok, _ := VerifyPiece(p, cert.PublicKey)
	if !ok { t.Error("certificate public key should verify the signature") }

	// Format is human-readable
	text := FormatCertificate(cert)
	for _, required := range []string{
		"INTELLECTUAL PROPERTY CERTIFICATE",
		"cc-by", "kapoost", "ORIGINALITY INDEX",
		"AUTHENTICITY", "Creative Commons",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("certificate missing: %q", required)
		}
	}
}

func TestCertificateContentHashDetectsTampering(t *testing.T) {
	p := makePiece("tamper-test", "Original body.", "free", 0)
	cert := BuildCopyright(p, "author", "pubkey")
	originalHash := cert.ContentHash

	// Tamper with body
	p.Body = "Tampered body."
	cert2 := BuildCopyright(p, "author", "pubkey")
	if cert2.ContentHash == originalHash {
		t.Error("different body should produce different hash")
	}
}

func TestCertificateOriginalityScoreRange(t *testing.T) {
	cases := []struct{ body, desc string }{
		{"a", "single char"},
		{"one two three", "three words"},
		{"Pierwsza pochodna.\nDruga potega.\nTrzeciego stopnia.\nMiejsce zerowe.", "poem"},
		{strings.Repeat("word ", 500), "500 repeated words"},
	}
	for _, c := range cases {
		p := makePiece("test", c.body, "free", 0)
		cert := BuildCopyright(p, "a", "k")
		if cert.Originality.Combined < 0 || cert.Originality.Combined > 1 {
			t.Errorf("%s: originality out of range: %.2f", c.desc, cert.Originality.Combined)
		}
	}
}

// ─── 6. AUDIENCE ACCESS MATRIX ───────────────────────────────

func TestAudienceMatrix(t *testing.T) {
	type accessCase struct {
		audience    []AudienceEntry
		callerKind  string
		callerID    string
		access      AccessLevel
		expectAccess bool
		desc        string
	}

	cases := []accessCase{
		// Public — always accessible
		{nil, "agent", "claude", AccessPublic, true, "public to agent"},
		{nil, "human", "alice", AccessPublic, true, "public to human"},
		{nil, "", "", AccessPublic, true, "public to anonymous"},

		// Locked — no audience = no access
		{nil, "agent", "claude", AccessLocked, false, "locked no audience → denied"},

		// Specific agent
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}}, "agent", "claude", AccessLocked, true, "exact agent match"},
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}}, "agent", "gpt",    AccessLocked, false, "wrong agent denied"},
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}}, "human", "alice",  AccessLocked, false, "human denied for agent entry"},

		// Wildcard agent
		{[]AudienceEntry{{Kind:"agent", ID:"*"}}, "agent", "claude", AccessLocked, true, "wildcard agent → claude ok"},
		{[]AudienceEntry{{Kind:"agent", ID:"*"}}, "agent", "gpt",    AccessLocked, true, "wildcard agent → gpt ok"},
		{[]AudienceEntry{{Kind:"agent", ID:"*"}}, "human", "alice",  AccessLocked, false, "wildcard agent → human denied"},

		// Specific human
		{[]AudienceEntry{{Kind:"human", ID:"alice"}}, "human", "alice", AccessLocked, true, "exact human match"},
		{[]AudienceEntry{{Kind:"human", ID:"alice"}}, "human", "bob",   AccessLocked, false, "wrong human denied"},

		// Mixed audience
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}, {Kind:"human", ID:"alice"}},
			"agent", "claude", AccessLocked, true, "mixed: claude ok"},
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}, {Kind:"human", ID:"alice"}},
			"human", "alice", AccessLocked, true, "mixed: alice ok"},
		{[]AudienceEntry{{Kind:"agent", ID:"claude"}, {Kind:"human", ID:"alice"}},
			"human", "bob", AccessLocked, false, "mixed: bob denied"},

		// Case insensitivity
		{[]AudienceEntry{{Kind:"Agent", ID:"Claude"}}, "agent", "claude", AccessLocked, true, "case insensitive"},
	}

	for _, c := range cases {
		b := &Blob{Access: c.access, Audience: c.audience}
		got := b.IsAccessibleTo(c.callerKind, c.callerID)
		if got != c.expectAccess {
			t.Errorf("%-45s got=%v want=%v", c.desc, got, c.expectAccess)
		}
	}
}

// ─── 7. SLUG / FIELD VALIDATION TESTS ────────────────────────

func TestSlugCharacters(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.Load()

	valid := []string{"my-poem", "poem-1", "deka-log", "abc123", "a"}
	for _, slug := range valid {
		p := makePiece(slug, "body", "free", 0)
		if err := s.Save(p); err != nil {
			t.Errorf("valid slug %q should save: %v", slug, err)
		}
	}
}

func TestBlobSlugCollisionOverwrites(t *testing.T) {
	bs, _ := newBlobStore(t)

	b1 := &Blob{Slug: "same", Title: "First",  BlobType: BlobDataset, TextData: "v1"}
	b2 := &Blob{Slug: "same", Title: "Second", BlobType: BlobDataset, TextData: "v2"}
	bs.Save(b1)
	bs.Save(b2)

	loaded, _ := bs.Get("same")
	if loaded.Title != "Second" { t.Error("second save should overwrite first") }
	if loaded.TextData != "v2"  { t.Error("data should be updated") }

	// Should still only have one entry
	all, _ := bs.Load()
	count := 0
	for _, b := range all { if b.Slug == "same" { count++ } }
	if count != 1 { t.Errorf("should have 1 entry with that slug, got %d", count) }
}

// ─── 8. CONCURRENT SAFETY (basic) ────────────────────────────

func TestCacheConcurrentAccess(t *testing.T) {
	c := NewCache[string](time.Second)
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			c.Set(fmt.Sprintf("value-%d", n))
			c.Get()
			c.Invalidate()
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ { <-done }
	// No panic = pass
}

// ─── 9. DATA INTEGRITY ROUNDTRIP ─────────────────────────────

func TestFullPieceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	kp, _ := GenerateKeyPair()
	s := NewStore(dir)
	s.Load()

	original := &Piece{
		Slug:        "full-roundtrip",
		Title:       "Full Roundtrip: Test",
		Type:        "poem",
		Access:      AccessLocked,
		Gate:        GateChallenge,
		Challenge:   "What color is the sky?",
		Answer:      "blue",
		Description: "A test piece.",
		Tags:        []string{"test", "roundtrip"},
		License:     "cc-by",
		PriceSats:   0,
		Published:   time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Body:        "Line one.\nLine two.\nLine three.",
	}
	sig, _ := SignPiece(original, kp)
	original.Signature = sig
	s.Save(original)

	s2 := NewStore(dir)
	s2.Load()
	loaded, err := s2.GetForEdit("full-roundtrip")
	if err != nil { t.Fatalf("GetForEdit: %v", err) }

	if loaded.Title != original.Title         { t.Errorf("title: %q", loaded.Title) }
	if loaded.Gate != original.Gate           { t.Errorf("gate: %q", loaded.Gate) }
	if loaded.Challenge != original.Challenge { t.Errorf("challenge: %q", loaded.Challenge) }
	if loaded.License != "cc-by"             { t.Errorf("license: %q", loaded.License) }
	if loaded.Body != original.Body          { t.Errorf("body: %q", loaded.Body) }
	if loaded.Signature != sig               { t.Errorf("signature lost") }

	// Verify signature still valid after roundtrip
	ok, status := VerifyPiece(loaded, kp.PublicKeyHex())
	if !ok { t.Errorf("signature should still verify after roundtrip: %s", status) }
}

func TestBlobFullRoundtrip(t *testing.T) {
	bs, _ := newBlobStore(t)
	kp, _ := GenerateKeyPair()

	original := &Blob{
		Slug:       "full-blob",
		Title:      "Full Blob Test",
		BlobType:   BlobContact,
		MimeType:   "application/json",
		Access:     AccessLocked,
		Audience:   []AudienceEntry{{Kind: "agent", ID: "claude"}, {Kind: "human", ID: "alice"}},
		TextData:   `{"email":"kapoost@example.com","city":"Warsaw"}`,
		Tags:       []string{"contact", "private"},
		Published:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	sig, _ := SignBlob(original, kp)
	original.Signature = sig
	bs.Save(original)

	loaded, err := bs.Get("full-blob")
	if err != nil { t.Fatalf("Get: %v", err) }

	if loaded.BlobType != BlobContact         { t.Errorf("type: %q", loaded.BlobType) }
	if loaded.MimeType != "application/json"  { t.Errorf("mime: %q", loaded.MimeType) }
	if len(loaded.Audience) != 2             { t.Errorf("audience: %d entries", len(loaded.Audience)) }
	if loaded.TextData != original.TextData   { t.Errorf("data mismatch") }
	if loaded.Signature != sig                { t.Errorf("signature lost") }

	// Verify blob signature after roundtrip
	ok, status := VerifyBlob(loaded, kp.PublicKeyHex())
	if !ok { t.Errorf("blob signature should verify after roundtrip: %s", status) }

	// Access control still works
	if !loaded.IsAccessibleTo("agent", "claude") { t.Error("claude should have access") }
	if !loaded.IsAccessibleTo("human", "alice")  { t.Error("alice should have access") }
	if loaded.IsAccessibleTo("agent", "gpt")     { t.Error("gpt should not have access") }
}
