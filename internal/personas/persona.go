// Package personas holds the Persona type and file-based loaders shared by
// the MCP handler, the rituals worker, and any future consumer that needs
// to read persona markdown files without depending on internal/mcp.
package personas

import (
	"os"
	"path/filepath"
	"strings"
)

// Persona is the parsed representation of a content/personas/<slug>.md file.
// Model is optional frontmatter selecting which Anthropic model generates
// this persona's voice in a narada. Values: "haiku" (fast + cheap, good for
// narrow/reactive roles) or "sonnet" (default, needed for personas that
// synthesise). Missing/unknown → sonnet.
type Persona struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Role  string   `json:"role"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
	Model string   `json:"model,omitempty"`
}

// Parse decodes a persona markdown file's raw contents. Frontmatter must be
// fenced by --- markers; missing frontmatter yields a Persona with only Slug
// (from fallback) and Body populated.
func Parse(raw, fallbackSlug string) Persona {
	p := Persona{Slug: fallbackSlug}
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		p.Body = raw
		return p
	}
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "slug:"):
			p.Slug = strings.TrimSpace(strings.TrimPrefix(line, "slug:"))
		case strings.HasPrefix(line, "title:"):
			p.Title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
		case strings.HasPrefix(line, "role:"):
			p.Role = strings.TrimSpace(strings.TrimPrefix(line, "role:"))
		case strings.HasPrefix(line, "tags:"):
			tagStr := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			tagStr = strings.Trim(tagStr, "[]")
			for _, t := range strings.Split(tagStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					p.Tags = append(p.Tags, t)
				}
			}
		case strings.HasPrefix(line, "model:"):
			p.Model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
		}
	}
	p.Body = strings.TrimSpace(parts[2])
	return p
}

// Load reads content/personas/<slug>.md from contentDir and parses it.
func Load(contentDir, slug string) (Persona, error) {
	path := filepath.Join(contentDir, "personas", slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Persona{}, err
	}
	return Parse(string(data), slug), nil
}

// LoadAll walks content/personas/ and returns every valid persona (skipping
// files with empty slug after parsing).
func LoadAll(contentDir string) []Persona {
	dir := filepath.Join(contentDir, "personas")
	var out []Persona
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		p := Parse(string(data), strings.TrimSuffix(e.Name(), ".md"))
		if p.Slug != "" {
			out = append(out, p)
		}
	}
	return out
}
