package storyboard

import "testing"

// TestStoryboards walks storyboards/ at the repo root and runs each YAML
// file as a subtest. Filter narrowly with:
//
//	go test ./internal/storyboard/... -run 'TestStoryboards/web/listings'
func TestStoryboards(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatalf("locate storyboards root: %v", err)
	}
	Run(t, root)
}
