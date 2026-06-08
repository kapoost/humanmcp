// Package storyboard parses YAML scenario specs from storyboards/ and runs
// them as Go subtests. One YAML file = one Storyboard = one t.Run(id, ...).
//
// See storyboards/README.md for the philosophy and storyboards/SUBMIT.md for
// the per-PR discipline.
package storyboard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Root returns the path to the storyboards/ directory relative to the
// caller's package. It walks up from cwd looking for go.mod so tests pass
// regardless of where they're invoked from.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "storyboards"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent of cwd")
		}
		dir = parent
	}
}

// Load reads and unmarshals every *.yaml under root into a slice of
// Storyboards. Files are visited in lexical order so subtest output is
// stable.
func Load(root string) ([]Storyboard, error) {
	var out []Storyboard
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("%s: %w", path, rerr)
		}
		var sb Storyboard
		if uerr := yaml.Unmarshal(data, &sb); uerr != nil {
			return fmt.Errorf("%s: %w", path, uerr)
		}
		if sb.ID == "" {
			return fmt.Errorf("%s: missing 'id' field", path)
		}
		out = append(out, sb)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Run executes every storyboard under root as a subtest. The subtest name
// is the scenario id, so `go test -run TestStoryboards/web/listings` filters
// by prefix.
func Run(t *testing.T, root string) {
	t.Helper()
	boards, err := Load(root)
	if err != nil {
		t.Fatalf("load storyboards: %v", err)
	}
	if len(boards) == 0 {
		t.Fatalf("no storyboards found under %s", root)
	}
	for _, sb := range boards {
		sb := sb
		t.Run(sb.ID, func(t *testing.T) {
			switch sb.Kind {
			case "http":
				runHTTP(t, sb)
			case "unit":
				runUnit(t, sb)
			case "store":
				runStore(t, sb)
			default:
				t.Fatalf("unknown kind %q (want http|unit|store)", sb.Kind)
			}
		})
	}
}
