package web_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestNoOrphanTemplateRoutes walks every template file under
// internal/web/templates/ collecting paths referenced from href / action /
// data-href, then walks every .go file under internal/web/ collecting paths
// registered via mux.Handle / mux.HandleFunc. A template route that has no
// matching handler is an "orphan" — a button or link that returns 404
// (or worse — POST→GET via 301).
//
// 2026-06-08: the listings_edit_returns_form / listings_delete storyboards
// were born from this exact class of bug. This test exists so the next
// orphan is caught at `go test` time, not by Łukasz noticing a dead button.
func TestNoOrphanTemplateRoutes(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	templates, err := scanTemplateRoutes(filepath.Join(repoRoot, "internal/web/templates"))
	if err != nil {
		t.Fatalf("scan templates: %v", err)
	}
	handlers, err := scanHandlerRoutes(filepath.Join(repoRoot, "internal/web"))
	if err != nil {
		t.Fatalf("scan handlers: %v", err)
	}

	// allowedOrphans — routes that look like template→handler mismatches
	// but are either legitimate exceptions or known issues being worked on.
	// Each entry MUST have a comment explaining why; the goal is to drive
	// this map back to empty. Adding a NEW entry without fixing the root
	// cause is the exact anti-pattern this test exists to prevent.
	allowedOrphans := map[string]string{
		// none — all 5 initial orphans were resolved on 2026-06-08.
	}

	var orphans []string
	for _, tr := range templates {
		if _, ok := allowedOrphans[tr.path]; ok {
			continue
		}
		if !matchesAnyHandler(tr.path, handlers) {
			orphans = append(orphans, fmt.Sprintf("  %-40s  (referenced in %s)", tr.path, tr.source))
		}
	}

	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("found %d template route(s) with no matching handler:\n%s",
			len(orphans), strings.Join(orphans, "\n"))
	}
}

type routeRef struct {
	path   string
	source string // file:line
}

// templateRouteRE captures href / action / data-href attributes whose value
// starts with "/" (i.e. internal paths, not external URLs, mailto, anchors).
var templateRouteRE = regexp.MustCompile(`(?:action|href|data-href)="(/[^"#{]*)"`)

// scanTemplateRoutes walks dir, reads every regular file, and collects
// internal route references with line numbers.
func scanTemplateRoutes(dir string) ([]routeRef, error) {
	var refs []routeRef
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, path)
		for lineNo, line := range strings.Split(string(data), "\n") {
			for _, m := range templateRouteRE.FindAllStringSubmatch(line, -1) {
				p := normalizeTemplatePath(m[1])
				if p == "" {
					continue
				}
				refs = append(refs, routeRef{
					path:   p,
					source: fmt.Sprintf("templates/%s:%d", rel, lineNo+1),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dedupRefs(refs), nil
}

// normalizeTemplatePath strips query strings and collapses double slashes
// left over from removed {{...}} substitutions. We accept paths that still
// contain `{{` (means the template path itself doesn't compose cleanly —
// caller can decide whether that's an issue), but we filter them out so
// the matcher doesn't try to compare placeholders.
func normalizeTemplatePath(p string) string {
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	if strings.Contains(p, "{{") {
		// Path has unresolved placeholder — likely composed inline like
		// /edit/{{.Slug}}. Strip placeholders.
		re := regexp.MustCompile(`\{\{[^}]+\}\}`)
		p = re.ReplaceAllString(p, "x")
	}
	// collapse double slashes
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if p == "" {
		return ""
	}
	return p
}

func dedupRefs(refs []routeRef) []routeRef {
	seen := map[string]string{}
	for _, r := range refs {
		if _, ok := seen[r.path]; !ok {
			seen[r.path] = r.source
		}
	}
	out := make([]routeRef, 0, len(seen))
	for p, s := range seen {
		out = append(out, routeRef{path: p, source: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// handlerRouteRE captures the first string argument to mux.Handle /
// mux.HandleFunc, which is the registered path pattern.
var handlerRouteRE = regexp.MustCompile(`mux\.(?:Handle|HandleFunc)\("([^"]+)"`)

func scanHandlerRoutes(dir string) (map[string]bool, error) {
	routes := map[string]bool{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range handlerRouteRE.FindAllStringSubmatch(string(data), -1) {
			routes[m[1]] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return routes, nil
}

// matchesAnyHandler decides whether a template path is covered by some
// registered handler.
//
//   - Exact match (handler "/login" matches template "/login"). Always wins.
//   - Prefix match for handlers ending in "/" — "/edit/" handles
//     "/edit/anything". Note: Go's ServeMux also matches the stripped
//     form ("/edit") via a 301 redirect, but that's fragile (POST→GET)
//     so we don't credit it as a clean match.
//   - The literal "/" handler is treated as a catch-all ONLY for the path
//     "/" itself. Otherwise routes would silently match the root index.
func matchesAnyHandler(tpath string, handlers map[string]bool) bool {
	if handlers[tpath] {
		return true
	}
	if tpath == "/" && handlers["/"] {
		return true
	}
	for h := range handlers {
		if h == "/" {
			continue
		}
		if strings.HasSuffix(h, "/") && strings.HasPrefix(tpath, h) {
			return true
		}
	}
	return false
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent")
		}
		dir = parent
	}
}
