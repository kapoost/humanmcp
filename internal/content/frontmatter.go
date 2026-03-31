package content

import (
	"strconv"
	"strings"
	"time"
)

// parseFrontmatter parses a minimal YAML subset from frontmatter lines.
// Supports: string, int, []string (inline: [a, b]), time.Time (date: 2006-01-02)
// Only the fields we need — no reflection, no deps.
func parseFrontmatter(lines []string, p *Piece) {
	for _, line := range lines {
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		switch k {
		case "slug":
			p.Slug = unquote(v)
		case "title":
			p.Title = unquote(v)
		case "type":
			p.Type = unquote(v)
		case "access":
			p.Access = AccessLevel(unquote(v))
		case "gate":
			p.Gate = GateType(unquote(v))
		case "challenge":
			p.Challenge = unquote(v)
		case "answer":
			p.Answer = unquote(v)
		case "description":
			p.Description = unquote(v)
		case "price_sats":
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			p.PriceSats = n
		case "tags":
			p.Tags = parseStringSlice(v)
		case "published":
			t, err := time.Parse("2006-01-02", strings.TrimSpace(v))
			if err == nil {
				p.Published = t
			}
		}
	}
}

// splitKV splits "key: value" → ("key", "value", true)
func splitKV(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	return k, v, k != ""
}

// unquote strips surrounding single or double quotes
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseStringSlice parses "[a, b, c]" or "a, b, c" into []string
func parseStringSlice(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := unquote(strings.TrimSpace(p))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// marshalFrontmatter serialises a Piece back to frontmatter lines
func marshalFrontmatter(p *Piece) string {
	var sb strings.Builder
	wf := func(k, v string) {
		if v != "" {
			sb.WriteString(k + ": " + v + "\n")
		}
	}
	wf("slug", p.Slug)
	wf("title", quoteIfNeeded(p.Title))
	wf("type", p.Type)
	wf("access", string(p.Access))
	if p.Gate != "" {
		wf("gate", string(p.Gate))
	}
	if p.Challenge != "" {
		wf("challenge", quoteIfNeeded(p.Challenge))
	}
	if p.Answer != "" {
		wf("answer", quoteIfNeeded(p.Answer))
	}
	if p.Description != "" {
		wf("description", quoteIfNeeded(p.Description))
	}
	if p.PriceSats > 0 {
		sb.WriteString("price_sats: " + strconv.Itoa(p.PriceSats) + "\n")
	}
	if len(p.Tags) > 0 {
		sb.WriteString("tags: [" + strings.Join(p.Tags, ", ") + "]\n")
	}
	if !p.Published.IsZero() {
		sb.WriteString("published: " + p.Published.Format("2006-01-02") + "\n")
	}
	return sb.String()
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, ":#\"'[]{}|>&!") || strings.Contains(s, ": ") {
		return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
	}
	return s
}
