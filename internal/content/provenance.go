package content

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProvenanceSchemaVersion is the on-disk schema version. Bump when the
// shape changes; the parser checks it and can apply migrations.
const ProvenanceSchemaVersion = 1

// ProvenanceType is one of nine closed types. Free-form "other" was
// rejected during the 2026-06-09 audit (Harvey) — a known type list is
// the difference between gallery-grade provenance and a folder of PDFs.
type ProvenanceType string

const (
	ProvenanceCertificate     ProvenanceType = "certificate_of_authenticity"
	ProvenanceInvoice         ProvenanceType = "invoice"
	ProvenanceExhibition      ProvenanceType = "exhibition_record"
	ProvenanceConservation    ProvenanceType = "conservation_report"
	ProvenanceAppraisal       ProvenanceType = "appraisal"
	ProvenanceSaleRecord      ProvenanceType = "sale_record"
	ProvenancePhotoRecord     ProvenanceType = "photograph_of_record"
	ProvenanceShipping        ProvenanceType = "shipping_record"
	ProvenanceInsurance       ProvenanceType = "insurance_document"
)

// ProvenanceCategory groups types for UI rendering. Derived from Type;
// owner does not set this explicitly.
type ProvenanceCategory string

const (
	CategoryAuthenticity  ProvenanceCategory = "authenticity"
	CategoryOwnership     ProvenanceCategory = "ownership"
	CategoryPublicHistory ProvenanceCategory = "public_history"
	CategoryCare          ProvenanceCategory = "care"
)

// categoryFor maps each closed Type to its UI category. Anti-pattern
// guard: tests assert every constant above appears here. Eleanor's audit
// note — painters think categorically, not chronologically.
func categoryFor(t ProvenanceType) ProvenanceCategory {
	switch t {
	case ProvenanceCertificate, ProvenanceAppraisal:
		return CategoryAuthenticity
	case ProvenanceInvoice, ProvenanceSaleRecord:
		return CategoryOwnership
	case ProvenanceExhibition, ProvenancePhotoRecord:
		return CategoryPublicHistory
	case ProvenanceConservation, ProvenanceShipping, ProvenanceInsurance:
		return CategoryCare
	}
	return ""
}

// ValidProvenanceType reports whether a type string maps to a known
// constant. Used by handlers to reject free-form input.
func ValidProvenanceType(t string) bool {
	return categoryFor(ProvenanceType(t)) != ""
}

// FileEntry is a single attached file. One provenance item can carry
// several — Eleanor's audit note: a sale record often comes as original
// invoice + translation + condition photograph.
type FileEntry struct {
	FileRef     string `json:"file_ref"`     // path under /data/provenance/files/
	Filename    string `json:"filename"`     // original upload filename
	ContentHash string `json:"content_hash"` // SHA-256 hex — Harvey's audit insistence
	MimeType    string `json:"mime_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// OwnerKind tells the store which entity a dossier is attached to. Pieces
// (artworks kapoost made) and collection items (works he owns) both grow
// provenance the same way.
type OwnerKind string

const (
	OwnerPiece      OwnerKind = "piece"
	OwnerCollection OwnerKind = "collection"
)

// ProvenanceItem is one dossier entry.
type ProvenanceItem struct {
	ID            string             `json:"id"`
	// Owner identifies the parent. Older items wrote ArtworkSlug only;
	// loader fills OwnerKind=piece when that legacy field is present
	// and OwnerKind/OwnerSlug are not.
	OwnerKind     OwnerKind          `json:"owner_kind,omitempty"`
	OwnerSlug     string             `json:"owner_slug,omitempty"`
	ArtworkSlug   string             `json:"artwork_slug,omitempty"` // legacy alias for OwnerSlug+kind=piece
	Version       int                `json:"version"`
	Category      ProvenanceCategory `json:"category"`
	Type          ProvenanceType     `json:"type"`
	IssuedBy      string             `json:"issued_by"`
	IssuedAt      time.Time          `json:"issued_at"`
	ChainPosition int                `json:"chain_position"`
	Title         string             `json:"title"`
	Files         []FileEntry        `json:"files"`
	Notes         string             `json:"notes"`
	Signature     string             `json:"signature,omitempty"`
	OTSProof      string             `json:"ots_proof,omitempty"`
	AddedAt       time.Time          `json:"added_at"`
}

// normalizeOwner fills in OwnerKind/OwnerSlug from the legacy
// ArtworkSlug field so callers can rely on the new fields regardless of
// when the item was written.
func (it *ProvenanceItem) normalizeOwner() {
	if it.OwnerKind == "" && it.OwnerSlug == "" && it.ArtworkSlug != "" {
		it.OwnerKind = OwnerPiece
		it.OwnerSlug = it.ArtworkSlug
	}
	if it.OwnerKind == "" {
		it.OwnerKind = OwnerPiece
	}
}

// ProvenanceStore persists items grouped by artwork slug — one JSON file
// per artwork plus a per-artwork files directory.
type ProvenanceStore struct {
	dir     string // /data/provenance
	filesDir string // /data/provenance/files
	mu      sync.RWMutex
}

func NewProvenanceStore(contentDir string) *ProvenanceStore {
	dataDir := filepath.Dir(contentDir)
	dir := filepath.Join(dataDir, "provenance")
	filesDir := filepath.Join(dir, "files")
	_ = os.MkdirAll(filesDir, 0o755)
	return &ProvenanceStore{dir: dir, filesDir: filesDir}
}

// FilesDir returns the absolute path under which file payloads live.
// Used by the upload handler to write the actual bytes.
func (s *ProvenanceStore) FilesDir() string {
	return s.filesDir
}

// List returns all items for a given owner (piece or collection), sorted
// by Category → ChainPosition → IssuedAt. The (kind, slug) pair lets the
// same store back both artwork dossiers and collection-item dossiers.
func (s *ProvenanceStore) List(kind OwnerKind, slug string) ([]ProvenanceItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items, err := s.loadAnyLocked(kind, slug)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		if items[i].ChainPosition != items[j].ChainPosition {
			return items[i].ChainPosition < items[j].ChainPosition
		}
		return items[i].IssuedAt.Before(items[j].IssuedAt)
	})
	return items, nil
}

// Get loads a single item by id, scoped to one owner.
func (s *ProvenanceStore) Get(kind OwnerKind, slug, id string) (*ProvenanceItem, error) {
	items, err := s.List(kind, slug)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("provenance item not found: %s/%s/%s", kind, slug, id)
}

// Save persists a new item or replaces an existing one with the same ID.
// item.OwnerKind + item.OwnerSlug are required (legacy ArtworkSlug accepted
// via normalizeOwner).
func (s *ProvenanceStore) Save(item ProvenanceItem, signer *KeyPair) (ProvenanceItem, error) {
	item.normalizeOwner()
	if item.OwnerSlug == "" {
		return ProvenanceItem{}, fmt.Errorf("owner_slug required")
	}
	if item.OwnerKind == "" {
		return ProvenanceItem{}, fmt.Errorf("owner_kind required")
	}
	if !ValidProvenanceType(string(item.Type)) {
		return ProvenanceItem{}, fmt.Errorf("invalid provenance type %q", item.Type)
	}
	item.Category = categoryFor(item.Type)
	if item.Version == 0 {
		item.Version = ProvenanceSchemaVersion
	}
	if item.AddedAt.IsZero() {
		item.AddedAt = time.Now().UTC()
	}
	if item.ID == "" {
		item.ID = generateProvenanceID(item.OwnerSlug, item.Type, item.IssuedAt)
	}
	// Keep ArtworkSlug in sync so older readers stay happy.
	if item.OwnerKind == OwnerPiece {
		item.ArtworkSlug = item.OwnerSlug
	} else {
		item.ArtworkSlug = ""
	}
	if signer != nil {
		sig, err := signProvenance(&item, signer)
		if err == nil {
			item.Signature = sig
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	items, _ := s.loadAnyLocked(item.OwnerKind, item.OwnerSlug)
	replaced := false
	for i := range items {
		if items[i].ID == item.ID {
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	if err := s.saveLocked(item.OwnerKind, item.OwnerSlug, items); err != nil {
		return ProvenanceItem{}, err
	}
	return item, nil
}

// Delete removes a single item plus its file payloads.
func (s *ProvenanceStore) Delete(kind OwnerKind, slug, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadAnyLocked(kind, slug)
	if err != nil {
		return err
	}
	var removed *ProvenanceItem
	kept := items[:0]
	for i := range items {
		if items[i].ID == id {
			removed = &items[i]
			continue
		}
		kept = append(kept, items[i])
	}
	if removed == nil {
		return fmt.Errorf("provenance item not found: %s/%s/%s", kind, slug, id)
	}
	for _, f := range removed.Files {
		_ = os.Remove(filepath.Join(s.filesDir, f.FileRef))
	}
	return s.saveLocked(kind, slug, kept)
}

// ComputeContentHash returns the SHA-256 hex of bytes — used both at upload
// time and (optionally) by verifying clients.
func ComputeContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// itemsPath returns /data/provenance/<kind>/<slug>.json. For OwnerPiece
// the legacy path /data/provenance/<slug>.json is checked first via
// loadAnyLocked so older artwork dossiers still resolve without migration.
func (s *ProvenanceStore) itemsPath(kind OwnerKind, slug string) string {
	return filepath.Join(s.dir, string(kind), slug+".json")
}

// legacyPiecePath returns the pre-2026-06-09 layout where artwork dossiers
// lived directly under /data/provenance/<slug>.json.
func (s *ProvenanceStore) legacyPiecePath(slug string) string {
	return filepath.Join(s.dir, slug+".json")
}

// loadAnyLocked tries the new (kind-scoped) path first, then falls back to
// the legacy artwork layout for backward compatibility.
func (s *ProvenanceStore) loadAnyLocked(kind OwnerKind, slug string) ([]ProvenanceItem, error) {
	if items, err := s.loadFromPath(s.itemsPath(kind, slug)); err == nil {
		if items != nil {
			for i := range items {
				items[i].normalizeOwner()
			}
			return items, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if kind == OwnerPiece {
		items, err := s.loadFromPath(s.legacyPiecePath(slug))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for i := range items {
			items[i].normalizeOwner()
		}
		return items, nil
	}
	return nil, nil
}

func (s *ProvenanceStore) loadFromPath(path string) ([]ProvenanceItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []ProvenanceItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *ProvenanceStore) saveLocked(kind OwnerKind, slug string, items []ProvenanceItem) error {
	path := s.itemsPath(kind, slug)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func generateProvenanceID(slug string, t ProvenanceType, issuedAt time.Time) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%d", slug, t, issuedAt.Format("2006-01-02"), time.Now().UnixNano())
	return "prov-" + hex.EncodeToString(h.Sum(nil)[:8])
}

// signProvenance signs the canonical bytes of an item — same shape as
// SignPiece. Files' ContentHash is folded in so any file swap invalidates
// the signature.
func signProvenance(item *ProvenanceItem, kp *KeyPair) (string, error) {
	canonical := provenanceCanonicalBytes(item)
	sig := ed25519.Sign(kp.PrivateKey, canonical)
	return base64.StdEncoding.EncodeToString(sig), nil
}

func provenanceCanonicalBytes(item *ProvenanceItem) []byte {
	owner := item.OwnerSlug
	if owner == "" {
		owner = item.ArtworkSlug // legacy items
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%d|%s",
		owner, item.ID, item.Type, item.IssuedBy,
		item.IssuedAt.Format(time.RFC3339), item.ChainPosition, item.Title)
	for _, f := range item.Files {
		fmt.Fprintf(&b, "|%s:%s", f.Filename, f.ContentHash)
	}
	hash := sha256.Sum256([]byte(b.String()))
	return hash[:]
}

// VerifyProvenance checks a single item against a public key. Returns
// (true, "verified — ...") on success, (false, reason) otherwise.
func VerifyProvenance(item *ProvenanceItem, pubKeyHex string) (bool, string) {
	if item.Signature == "" {
		return false, "unsigned"
	}
	pub, err := PublicKeyFromHex(pubKeyHex)
	if err != nil {
		return false, "invalid public key"
	}
	sigBytes, err := base64.StdEncoding.DecodeString(item.Signature)
	if err != nil {
		return false, "malformed signature"
	}
	if !ed25519.Verify(pub, provenanceCanonicalBytes(item), sigBytes) {
		return false, "invalid signature — item or file hashes modified"
	}
	return true, "verified"
}
