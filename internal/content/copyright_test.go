package content

import (
	"strings"
	"testing"
)

// --- OriginalityIndex tests ---

func TestOriginalityEmptyText(t *testing.T) {
	idx := ComputeOriginality("")
	if idx.Grade != "D" { t.Errorf("empty should be D, got %s", idx.Grade) }
	if idx.Combined != 0 { t.Errorf("empty should be 0, got %.2f", idx.Combined) }
}

func TestOriginalityShortText(t *testing.T) {
	idx := ComputeOriginality("Hello world.")
	if idx.WordCount != 2 { t.Errorf("word count: got %d", idx.WordCount) }
}

func TestOriginalityPoemVsGeneric(t *testing.T) {
	poem := ComputeOriginality(`Najwiekszy dzielnik; wspolny mianownik!
Mowiac jezykiem maszyn.
Milosci nie znajac krzyczy sie glosniej.
Dekalog ludzkich nieznaczen.
Wspolne przeciecie.
Pierwsza pochodna.
Druga potega!
Cztery wymiary?
Siodme poty.
Osmy krag.
Miejsce zerowe.`)

	generic := ComputeOriginality("The sky is blue. The sun is bright. Today is a good day. I feel happy about life. Everything is wonderful. The world is great. People are kind. Nature is amazing.")

	if poem.Combined <= generic.Combined {
		t.Errorf("poem (%.2f) should score higher than generic (%.2f)", poem.Combined, generic.Combined)
	}
}

func TestOriginalityComponentsInRange(t *testing.T) {
	text := `Some text with multiple sentences.
Short line.
And a much longer line that goes on and on for quite some time.
Another short one.`

	idx := ComputeOriginality(text)
	for name, val := range map[string]float64{
		"Combined":       idx.Combined,
		"Burstiness":     idx.Burstiness,
		"LexicalDensity": idx.LexicalDensity,
		"ShannonEntropy": idx.ShannonEntropy,
		"StructuralSig":  idx.StructuralSig,
	} {
		if val < 0 || val > 1 {
			t.Errorf("%s out of range: %.2f", name, val)
		}
	}
}

func TestOriginalityGradeAssignment(t *testing.T) {
	// Force different combined scores and verify grade
	cases := []struct {
		text        string
		minGrade    string
	}{
		// Rich diverse text should score at least C
		{`The quick brown fox jumps over lazy dogs.
She sells seashells by the seashore!
How much wood would a woodchuck chuck?
Peter Piper picked a peck of pickled peppers.
Betty Botter bought some butter.
Round the rugged rocks the ragged rascal ran.`, "C"},
	}
	for _, c := range cases {
		idx := ComputeOriginality(c.text)
		grades := map[string]int{"D": 0, "C": 1, "B": 2, "A": 3, "S": 4}
		if grades[idx.Grade] < grades[c.minGrade] {
			t.Errorf("expected at least %s, got %s (%.2f)", c.minGrade, idx.Grade, idx.Combined)
		}
	}
}

func TestOriginalityWordCount(t *testing.T) {
	text := "one two three four five"
	idx := ComputeOriginality(text)
	if idx.WordCount != 5 { t.Errorf("word count: got %d, want 5", idx.WordCount) }
	if idx.UniqueWords != 5 { t.Errorf("unique: got %d, want 5", idx.UniqueWords) }
}

func TestOriginalityDuplicateWords(t *testing.T) {
	// Repeated words should lower lexical density
	rich := ComputeOriginality("apple orange mango banana cherry grape lemon peach plum kiwi fig date")
	repetitive := ComputeOriginality("apple apple apple apple apple apple apple apple apple apple apple apple")
	if rich.LexicalDensity <= repetitive.LexicalDensity {
		t.Errorf("rich (%.2f) should beat repetitive (%.2f)", rich.LexicalDensity, repetitive.LexicalDensity)
	}
}

func TestBurstyTextScoresHigher(t *testing.T) {
	// Highly varied sentence lengths vs uniform
	bursty := ComputeOriginality(`Hi.
This is a much longer sentence that goes on for quite some time.
No.
Another very long sentence here with many many words and ideas packed in.
Yes!
And yet another sentence of considerable length exploring various concepts.`)

	uniform := ComputeOriginality("The cat sat. The dog ran. The bird flew. The fish swam. The horse trotted. The cow mooed. The pig oinked.")

	if bursty.Burstiness <= uniform.Burstiness {
		t.Errorf("bursty (%.2f) should beat uniform (%.2f)", bursty.Burstiness, uniform.Burstiness)
	}
}

func TestStructuralSignaturePoetry(t *testing.T) {
	// Poetry with varying line lengths should have high structural signature
	poetry := ComputeOriginality(`Short.
A medium length line here.
And then an extraordinarily long line that just keeps going and going.
Brief.
Another quite long sentence with many descriptive words in it.`)

	prose := ComputeOriginality(`This is one paragraph with similar length throughout the text.
Another line about the same length as the previous one.
And then yet another line with approximately equal length to all others.
This is consistent and uniform in its structure throughout.`)

	if poetry.StructuralSig <= prose.StructuralSig {
		t.Errorf("poetry struct (%.2f) should beat prose struct (%.2f)", poetry.StructuralSig, prose.StructuralSig)
	}
}

// --- ContentHash tests ---

func TestContentHashConsistent(t *testing.T) {
	h1 := ContentHash("hello world")
	h2 := ContentHash("hello world")
	if h1 != h2 { t.Error("same content should produce same hash") }
}

func TestContentHashDiffers(t *testing.T) {
	h1 := ContentHash("hello world")
	h2 := ContentHash("hello world!")
	if h1 == h2 { t.Error("different content should produce different hash") }
}

func TestContentHashLength(t *testing.T) {
	h := ContentHash("test")
	if len(h) != 64 { t.Errorf("sha256 hex should be 64 chars, got %d", len(h)) }
}

// --- BuildCopyright tests ---

func TestBuildCopyright(t *testing.T) {
	p := &Piece{
		Slug:      "test",
		Title:     "Test Poem",
		Body:      "Short poem.\nWith two lines.\nAnd a third.",
		License:   "cc-by",
		PriceSats: 0,
		Signature: "abc123",
	}
	c := BuildCopyright(p, "kapoost", "pubkey123")

	if c.Author != "kapoost" { t.Errorf("author: %s", c.Author) }
	if c.Title != "Test Poem" { t.Errorf("title: %s", c.Title) }
	if c.ContentHash == "" { t.Error("hash should not be empty") }
	if c.Originality.Combined < 0 { t.Error("originality should be >= 0") }
	if c.License != "cc-by" { t.Errorf("license: %s", c.License) }
}

// --- FormatCertificate tests ---

func TestFormatCertificateContainsRequiredFields(t *testing.T) {
	p := &Piece{
		Title:     "My Poem",
		Body:      "Line one.\nLine two longer.\nLine three even longer than that.",
		License:   "free",
		Signature: "sig123abc",
	}
	c := BuildCopyright(p, "kapoost", "pubkeyhex123")
	cert := FormatCertificate(c)

	required := []string{
		"INTELLECTUAL PROPERTY CERTIFICATE",
		"My Poem",
		"kapoost",
		"ORIGINALITY INDEX",
		"AUTHENTICITY",
		"TERMS:",
		"content_hash:",
	}
	for _, s := range required {
		if !strings.Contains(cert, s) {
			t.Errorf("certificate missing: %q", s)
		}
	}
}

func TestFormatCertificateCommercialPrice(t *testing.T) {
	p := &Piece{Title: "T", Body: "B", License: "commercial", PriceSats: 500}
	c := BuildCopyright(p, "author", "key")
	cert := FormatCertificate(c)
	if !strings.Contains(cert, "500 sats") {
		t.Error("commercial certificate should show price")
	}
}

func TestFormatCertificateLicenseTerms(t *testing.T) {
	licenses := map[string]string{
		"free":       "attribution",
		"cc-by":      "Creative Commons",
		"cc-by-nc":   "Non-Commercial",
		"commercial": "sats",
		"exclusive":  "negotiate",
		"all-rights": "all rights",
	}
	for lic, expected := range licenses {
		p := &Piece{Title: "T", Body: "B", License: lic, PriceSats: 100}
		c := BuildCopyright(p, "author", "key")
		cert := FormatCertificate(c)
		if !strings.Contains(cert, expected) {
			t.Errorf("license %s: certificate should contain %q", lic, expected)
		}
	}
}
