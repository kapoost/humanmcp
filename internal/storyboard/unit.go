package storyboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
)

// unitFuncs maps the YAML `function:` string to a Go closure that takes
// a single input (from `cases[].input`) and returns a comparable string
// (matched against `cases[].expect`).
//
// To expose a new function: add an entry here and reference it by name in
// a storyboard. The closure does its own type narrowing — that keeps the
// YAML schema small and the dispatch table explicit.
var unitFuncs = map[string]func(input interface{}) (string, error){
	"content.CallerFromUA": func(in interface{}) (string, error) {
		s, ok := in.(string)
		if !ok {
			return "", fmt.Errorf("input must be string, got %T", in)
		}
		return string(content.CallerFromUA(s)), nil
	},
	"mcp.NormalizePoem": func(in interface{}) (string, error) {
		s, ok := in.(string)
		if !ok {
			return "", fmt.Errorf("input must be string, got %T", in)
		}
		return mcp.NormalizePoem(strings.ToLower(s)), nil
	},
}

func runUnit(t *testing.T, sb Storyboard) {
	t.Helper()
	fn, ok := unitFuncs[sb.Function]
	if !ok {
		t.Fatalf("unknown unit function %q — register it in internal/storyboard/unit.go", sb.Function)
	}
	for _, c := range sb.UnitCases {
		c := c
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("%v", c.Input)
			if len(name) > 60 {
				name = name[:60] + "..."
			}
		}
		t.Run(name, func(t *testing.T) {
			got, err := fn(c.Input)
			if err != nil {
				t.Fatalf("function returned error: %v", err)
			}
			want := fmt.Sprintf("%v", c.Expect)
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
