package storyboard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/content"
)

// runStore exercises the content.Store Save → reload → Load round-trip.
// Used for persistence guarantees: signatures, OTSProof, frontmatter fields.
//
// Operations execute in order. After each `reload: true` a fresh Store is
// instantiated against the same dir so we re-read from disk. `load: <slug>`
// fetches a piece into the assertion bag.
func runStore(t *testing.T, sb Storyboard) {
	t.Helper()
	dir := t.TempDir()
	store := content.NewStore(dir)
	_ = store.Load()

	var loaded map[string]*content.Piece = make(map[string]*content.Piece)

	for i, op := range sb.Operations {
		switch {
		case op.Save != nil:
			p := pieceFromMap(op.Save)
			if err := store.Save(p); err != nil {
				t.Fatalf("op %d: save %s: %v", i, p.Slug, err)
			}
		case op.Reload:
			store = content.NewStore(dir)
			if err := store.Load(); err != nil {
				t.Fatalf("op %d: reload: %v", i, err)
			}
		case op.Load != "":
			p, err := store.GetForEdit(op.Load)
			if err != nil {
				t.Fatalf("op %d: load %s: %v", i, op.Load, err)
			}
			loaded[op.Load] = p
		}
	}

	for _, a := range sb.StoreAssertions {
		// If `on` not specified and exactly one piece loaded, use that one.
		on := a.On
		if on == "" {
			if len(loaded) != 1 {
				t.Errorf("assertion needs `on:` when multiple pieces loaded (got %d)", len(loaded))
				continue
			}
			for k := range loaded {
				on = k
			}
		}
		p, ok := loaded[on]
		if !ok {
			t.Errorf("assertion targets %q but no piece loaded with that slug", on)
			continue
		}
		got := reflectField(p, a.Field)
		t.Run(a.Field+"_"+on, func(t *testing.T) {
			if a.NotEmpty && got == "" {
				t.Errorf("field %s is empty, want non-empty", a.Field)
			}
			if a.Equals != "" && got != a.Equals {
				t.Errorf("field %s: got %q, want %q", a.Field, truncate(got, 60), truncate(a.Equals, 60))
			}
			if a.StartsWith != "" && !strings.HasPrefix(got, a.StartsWith) {
				t.Errorf("field %s: got %q, want prefix %q", a.Field, truncate(got, 60), a.StartsWith)
			}
		})
	}
}

func pieceFromMap(m map[string]interface{}) *content.Piece {
	p := &content.Piece{
		Slug:      toStr(m["slug"]),
		Title:     toStr(m["title"]),
		Type:      toStr(m["type"]),
		Body:      toStr(m["body"]),
		Signature: toStr(m["signature"]),
		OTSProof:  toStr(m["ots_proof"]),
	}
	if p.Type == "" {
		p.Type = "poem"
	}
	if access := toStr(m["access"]); access != "" {
		p.Access = content.AccessLevel(access)
	} else {
		p.Access = content.AccessPublic
	}
	return p
}

// reflectField fetches a string-valued exported field by name. Returns ""
// for unknown fields or non-string types — assertions surface that as the
// field being empty.
func reflectField(p *content.Piece, name string) string {
	v := reflect.ValueOf(p).Elem().FieldByName(name)
	if !v.IsValid() {
		return ""
	}
	if v.Kind() != reflect.String {
		return ""
	}
	return v.String()
}
