package content

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectionRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "content")
	store := NewCollectionStore(dir)

	saved, err := store.Save(CollectionItem{
		Slug:             "rzeznik-mountains-1998",
		Title:            "Mountains, 1998",
		OriginalCreator:  "Artur Rzeźnik",
		Medium:           "oil on canvas",
		Year:             1998,
		Dimensions:       "60 × 80 cm",
		AcquiredAt:       time.Date(2010, 5, 1, 0, 0, 0, 0, time.UTC),
		AcquiredFrom:     "Galeria Foksal",
		AcquisitionPrice: "5000 PLN",
		Notes:            "Bought directly after a vernissage.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Access != "private" {
		t.Errorf("Access default should be private, got %q", saved.Access)
	}

	// Fresh store reads from disk
	store2 := NewCollectionStore(dir)
	loaded, err := store2.Get("rzeznik-mountains-1998")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Title != "Mountains, 1998" || loaded.OriginalCreator != "Artur Rzeźnik" {
		t.Errorf("fields lost: %+v", loaded)
	}
	if loaded.Year != 1998 {
		t.Errorf("Year lost: %d", loaded.Year)
	}
}

func TestCollectionRequiredFields(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "content"))
	cases := []struct {
		name string
		item CollectionItem
	}{
		{"missing slug", CollectionItem{Title: "X", OriginalCreator: "Y"}},
		{"missing title", CollectionItem{Slug: "x", OriginalCreator: "Y"}},
		{"missing original creator", CollectionItem{Slug: "x", Title: "X"}},
	}
	for _, c := range cases {
		if _, err := store.Save(c.item); err == nil {
			t.Errorf("%s: Save should error, did not", c.name)
		}
	}
}

func TestCollectionPublicListFilters(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "content"))
	store.Save(CollectionItem{Slug: "a", Title: "A", OriginalCreator: "Z", Access: "public"})
	store.Save(CollectionItem{Slug: "b", Title: "B", OriginalCreator: "Z"}) // default private
	store.Save(CollectionItem{Slug: "c", Title: "C", OriginalCreator: "Z", Access: "members"})

	pub := store.PublicList()
	if len(pub) != 1 || pub[0].Slug != "a" {
		t.Errorf("PublicList should return only 'a', got %v", pub)
	}
	all := store.List()
	if len(all) != 3 {
		t.Errorf("List should return all 3, got %d", len(all))
	}
}

func TestCollectionDelete(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "content"))
	store.Save(CollectionItem{Slug: "doomed", Title: "X", OriginalCreator: "Y"})
	if err := store.Delete("doomed"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if items := store.List(); len(items) != 0 {
		t.Errorf("expected empty list, got %d", len(items))
	}
}

func TestCollectionAccessLevelsPersist(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "content"))
	for _, access := range []string{"private", "members", "public"} {
		saved, err := store.Save(CollectionItem{
			Slug: "x-" + access, Title: "X", OriginalCreator: "Y", Access: access,
		})
		if err != nil {
			t.Fatalf("Save %s: %v", access, err)
		}
		if !strings.EqualFold(saved.Access, access) {
			t.Errorf("Access drifted: %s → %s", access, saved.Access)
		}
	}
}
