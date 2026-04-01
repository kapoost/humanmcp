package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- BlobStore tests ---

func TestBlobSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	b := &Blob{
		Slug:        "my-vector",
		Title:       "Test Vector",
		BlobType:    BlobVector,
		Description: "An embedding.",
		Access:      AccessLocked,
		Schema:      "text-embedding-3-small",
		Dimensions:  1536,
		Encoding:    "base64-float32",
		Base64Data:  "AAAA",
		Audience:    []AudienceEntry{{Kind: "agent", ID: "claude"}},
		Published:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := bs.Save(b); err != nil {
		t.Fatalf("Save: %v", err)
	}

	blobs, err := bs.Load()
	if err != nil { t.Fatalf("Load: %v", err) }
	if len(blobs) != 1 { t.Fatalf("want 1, got %d", len(blobs)) }

	loaded := blobs[0]
	if loaded.Slug != "my-vector" { t.Errorf("slug: %q", loaded.Slug) }
	if loaded.BlobType != BlobVector { t.Errorf("type: %q", loaded.BlobType) }
	if loaded.Schema != "text-embedding-3-small" { t.Errorf("schema: %q", loaded.Schema) }
	if loaded.Dimensions != 1536 { t.Errorf("dimensions: %d", loaded.Dimensions) }
	if loaded.Encoding != "base64-float32" { t.Errorf("encoding: %q", loaded.Encoding) }
	if loaded.Base64Data != "AAAA" { t.Errorf("base64_data: %q", loaded.Base64Data) }
	if len(loaded.Audience) != 1 || loaded.Audience[0].Kind != "agent" {
		t.Errorf("audience: %v", loaded.Audience)
	}
}

func TestBlobGetBySlug(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	bs.Save(&Blob{Slug: "alpha", Title: "Alpha", BlobType: BlobImage, Access: AccessPublic})
	bs.Save(&Blob{Slug: "beta", Title: "Beta", BlobType: BlobContact, Access: AccessPublic})

	b, err := bs.Get("beta")
	if err != nil { t.Fatalf("Get: %v", err) }
	if b.Title != "Beta" { t.Errorf("title: %q", b.Title) }

	_, err = bs.Get("nonexistent")
	if err == nil { t.Error("should error for nonexistent slug") }
}

func TestBlobDelete(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	bs.Save(&Blob{Slug: "todelete", Title: "Delete Me", BlobType: BlobDataset, Access: AccessPublic})
	if err := bs.Delete("todelete"); err != nil { t.Fatalf("Delete: %v", err) }

	_, err := bs.Get("todelete")
	if err == nil { t.Error("should be gone after delete") }
}

func TestBlobStoreFile(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	data := []byte{0xFF, 0xD8, 0xFF} // JPEG magic bytes
	ref, err := bs.StoreFile("photo-slug", "photo.jpg", data)
	if err != nil { t.Fatalf("StoreFile: %v", err) }
	if ref != "files/photo-slug.jpg" { t.Errorf("ref: %q", ref) }

	read, err := bs.ReadFile(ref)
	if err != nil { t.Fatalf("ReadFile: %v", err) }
	if string(read) != string(data) { t.Error("file content mismatch") }
}

// --- Audience / access control tests ---

func TestIsAccessibleToPublic(t *testing.T) {
	b := &Blob{Access: AccessPublic}
	if !b.IsAccessibleTo("agent", "claude") { t.Error("public should be accessible to anyone") }
	if !b.IsAccessibleTo("human", "alice") { t.Error("public should be accessible to anyone") }
	if !b.IsAccessibleTo("", "") { t.Error("public should be accessible to anyone") }
}

func TestIsAccessibleToAudience(t *testing.T) {
	b := &Blob{
		Access: AccessLocked,
		Audience: []AudienceEntry{
			{Kind: "agent", ID: "claude"},
			{Kind: "human", ID: "alice"},
		},
	}
	if !b.IsAccessibleTo("agent", "claude") { t.Error("claude should have access") }
	if !b.IsAccessibleTo("human", "alice") { t.Error("alice should have access") }
	if b.IsAccessibleTo("agent", "gpt") { t.Error("gpt should NOT have access") }
	if b.IsAccessibleTo("human", "bob") { t.Error("bob should NOT have access") }
}

func TestIsAccessibleToWildcard(t *testing.T) {
	b := &Blob{
		Access:   AccessLocked,
		Audience: []AudienceEntry{{Kind: "agent", ID: "*"}},
	}
	if !b.IsAccessibleTo("agent", "claude") { t.Error("any agent should have access") }
	if !b.IsAccessibleTo("agent", "gpt") { t.Error("any agent should have access") }
	if b.IsAccessibleTo("human", "alice") { t.Error("humans should NOT have access via agent wildcard") }
}

func TestIsAccessibleCaseInsensitive(t *testing.T) {
	b := &Blob{
		Access:   AccessLocked,
		Audience: []AudienceEntry{{Kind: "agent", ID: "Claude"}},
	}
	if !b.IsAccessibleTo("agent", "claude") { t.Error("should be case-insensitive") }
	if !b.IsAccessibleTo("AGENT", "CLAUDE") { t.Error("should be case-insensitive") }
}

// --- Blob signing tests ---

func TestSignAndVerifyBlob(t *testing.T) {
	kp, _ := GenerateKeyPair()
	b := &Blob{
		Slug:       "vec",
		Title:      "My Vector",
		Base64Data: "AAABBBCCC",
	}
	sig, err := SignBlob(b, kp)
	if err != nil { t.Fatalf("SignBlob: %v", err) }
	b.Signature = sig

	ok, status := VerifyBlob(b, kp.PublicKeyHex())
	if !ok { t.Errorf("should verify: %s", status) }
}

func TestVerifyBlobFailsOnTamperedData(t *testing.T) {
	kp, _ := GenerateKeyPair()
	b := &Blob{Slug: "x", Title: "X", Base64Data: "original"}
	sig, _ := SignBlob(b, kp)
	b.Signature = sig
	b.Base64Data = "tampered"

	ok, _ := VerifyBlob(b, kp.PublicKeyHex())
	if ok { t.Error("tampered data should fail") }
}

// --- Contact/dataset content roundtrip ---

func TestContactBlobRoundTrip(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	contact := map[string]string{
		"name":    "kapoost",
		"email":   "kapoost@example.com",
		"address": "Warsaw, Poland",
	}
	data, _ := json.Marshal(contact)

	b := &Blob{
		Slug:     "my-contact",
		Title:    "My Contact",
		BlobType: BlobContact,
		MimeType: "application/json",
		Access:   AccessLocked,
		Audience: []AudienceEntry{{Kind: "human", ID: "trusted-friend"}},
		TextData: string(data),
	}
	bs.Save(b)

	loaded, _ := bs.Get("my-contact")
	if loaded.TextData == "" { t.Error("TextData should survive roundtrip") }

	var roundtripped map[string]string
	if err := json.Unmarshal([]byte(loaded.TextData), &roundtripped); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if roundtripped["email"] != "kapoost@example.com" {
		t.Errorf("email: %q", roundtripped["email"])
	}
}

func TestVectorBlobRoundTrip(t *testing.T) {
	dir := t.TempDir()
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	bs := NewBlobStore(contentDir)

	// Simulate a small vector
	fakeVector := strings.Repeat("AAEC", 384) // 1536 fake floats as base64

	b := &Blob{
		Slug:       "poem-embedding",
		Title:      "Embedding: deka-log",
		BlobType:   BlobVector,
		Schema:     "text-embedding-3-small",
		Dimensions: 1536,
		Encoding:   "base64-float32",
		MimeType:   "application/octet-stream",
		Access:     AccessLocked,
		Audience:   []AudienceEntry{{Kind: "agent", ID: "*"}},
		Base64Data: fakeVector,
	}
	bs.Save(b)

	loaded, _ := bs.Get("poem-embedding")
	if loaded.Schema != "text-embedding-3-small" { t.Errorf("schema: %q", loaded.Schema) }
	if loaded.Dimensions != 1536 { t.Errorf("dimensions: %d", loaded.Dimensions) }
	if loaded.Base64Data != fakeVector { t.Error("vector data should survive roundtrip") }
}
