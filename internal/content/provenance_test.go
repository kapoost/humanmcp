package content

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProvenanceCategoriesCoverAllTypes(t *testing.T) {
	// Defensive: every closed-set Type constant must resolve to a
	// non-empty Category. New type added without an audit category =
	// red build (Harvey's invariant).
	all := []ProvenanceType{
		ProvenanceCertificate, ProvenanceInvoice, ProvenanceExhibition,
		ProvenanceConservation, ProvenanceAppraisal, ProvenanceSaleRecord,
		ProvenancePhotoRecord, ProvenanceShipping, ProvenanceInsurance,
	}
	for _, ty := range all {
		if categoryFor(ty) == "" {
			t.Errorf("type %q has no category mapping", ty)
		}
		if !ValidProvenanceType(string(ty)) {
			t.Errorf("type %q rejected by ValidProvenanceType", ty)
		}
	}
	if ValidProvenanceType("other") {
		t.Error(`"other" should be rejected — closed set, not free-form`)
	}
	if ValidProvenanceType("") {
		t.Error("empty type should be rejected")
	}
}

func TestProvenanceStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewProvenanceStore(dir)
	issuedAt := time.Date(2024, 5, 12, 0, 0, 0, 0, time.UTC)
	saved, err := store.Save(ProvenanceItem{
		ArtworkSlug:   "test-painting",
		Type:          ProvenanceCertificate,
		IssuedBy:      "Galeria Foksal",
		IssuedAt:      issuedAt,
		ChainPosition: 1,
		Title:         "Certificate of Authenticity",
		Files: []FileEntry{
			{FileRef: "test-painting/abc.pdf", Filename: "cert.pdf", ContentHash: "abc123", MimeType: "application/pdf", SizeBytes: 12345},
		},
		Notes: "Original certificate, signed by gallery director.",
	}, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.ID == "" {
		t.Error("ID not generated")
	}
	if saved.Category != CategoryAuthenticity {
		t.Errorf("Category = %q, want %q", saved.Category, CategoryAuthenticity)
	}
	if saved.Version != ProvenanceSchemaVersion {
		t.Errorf("Version = %d, want %d", saved.Version, ProvenanceSchemaVersion)
	}

	// Reload through a fresh store — disk persistence
	store2 := NewProvenanceStore(dir)
	items, err := store2.List("test-painting")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].IssuedBy != "Galeria Foksal" {
		t.Errorf("IssuedBy lost: %q", items[0].IssuedBy)
	}
	if items[0].Files[0].ContentHash != "abc123" {
		t.Errorf("ContentHash lost: %q", items[0].Files[0].ContentHash)
	}
}

func TestProvenanceListGroupsAndSorts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewProvenanceStore(dir)
	slug := "test-art"

	// Insert in deliberately wrong order — store should re-sort on List.
	store.Save(ProvenanceItem{ArtworkSlug: slug, Type: ProvenanceConservation, Title: "B", ChainPosition: 5, IssuedAt: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}, nil)
	store.Save(ProvenanceItem{ArtworkSlug: slug, Type: ProvenanceCertificate, Title: "A", ChainPosition: 1, IssuedAt: time.Date(1998, 1, 1, 0, 0, 0, 0, time.UTC)}, nil)
	store.Save(ProvenanceItem{ArtworkSlug: slug, Type: ProvenanceInvoice, Title: "C", ChainPosition: 2, IssuedAt: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)}, nil)

	items, err := store.List(slug)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3, got %d", len(items))
	}
	// Categories alphabetically: authenticity → care → ownership.
	wantCats := []ProvenanceCategory{CategoryAuthenticity, CategoryCare, CategoryOwnership}
	for i, want := range wantCats {
		if items[i].Category != want {
			t.Errorf("position %d: category %q, want %q", i, items[i].Category, want)
		}
	}
}

func TestProvenanceSignAndVerify(t *testing.T) {
	kp, _ := GenerateKeyPair()
	dir := filepath.Join(t.TempDir(), "content")
	store := NewProvenanceStore(dir)
	item, err := store.Save(ProvenanceItem{
		ArtworkSlug:   "test-art",
		Type:          ProvenanceCertificate,
		IssuedBy:      "X",
		IssuedAt:      time.Now(),
		ChainPosition: 1,
		Title:         "cert",
		Files: []FileEntry{
			{Filename: "cert.pdf", ContentHash: "deadbeef"},
		},
	}, kp)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if item.Signature == "" {
		t.Fatal("Signature empty after Save with keypair")
	}
	ok, msg := VerifyProvenance(&item, kp.PublicKeyHex())
	if !ok {
		t.Errorf("verify failed: %s", msg)
	}

	// Tampered hash — verification must fail
	tampered := item
	tampered.Files = []FileEntry{{Filename: "cert.pdf", ContentHash: "feedface"}}
	if ok, _ := VerifyProvenance(&tampered, kp.PublicKeyHex()); ok {
		t.Error("tampered file hash should invalidate signature")
	}

	// Empty signature → unsigned
	unsigned := item
	unsigned.Signature = ""
	if ok, msg := VerifyProvenance(&unsigned, kp.PublicKeyHex()); ok || !strings.Contains(msg, "unsigned") {
		t.Errorf("expected unsigned, got %q", msg)
	}
}

func TestProvenanceDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewProvenanceStore(dir)
	saved, _ := store.Save(ProvenanceItem{
		ArtworkSlug: "x",
		Type:        ProvenanceCertificate,
		Title:       "C1",
		IssuedAt:    time.Now(),
	}, nil)
	if err := store.Delete("x", saved.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	items, _ := store.List("x")
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d", len(items))
	}
	if err := store.Delete("x", "nonexistent"); err == nil {
		t.Error("Delete on missing id should error")
	}
}

func TestProvenanceRejectsFreeFormType(t *testing.T) {
	store := NewProvenanceStore(filepath.Join(t.TempDir(), "content"))
	_, err := store.Save(ProvenanceItem{
		ArtworkSlug: "x",
		Type:        "other",
		Title:       "X",
		IssuedAt:    time.Now(),
	}, nil)
	if err == nil {
		t.Error(`save with Type="other" should error — closed set`)
	}
}
