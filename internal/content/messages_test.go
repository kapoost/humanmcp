package content

import (
	"strings"
	"testing"
)

func TestMessageSaveAndList(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	m, err := ms.Save("alice", "Hello kapoost!", "")
	if err != nil { t.Fatalf("Save: %v", err) }
	if m.ID == "" { t.Error("ID should not be empty") }
	if m.From != "alice" { t.Errorf("from: got %q", m.From) }
	if m.Text != "Hello kapoost!" { t.Errorf("text: got %q", m.Text) }

	msgs, err := ms.List()
	if err != nil { t.Fatalf("List: %v", err) }
	if len(msgs) != 1 { t.Fatalf("want 1 message, got %d", len(msgs)) }
}

func TestMessageRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)
	_, err := ms.Save("", "", "")
	if err == nil { t.Error("empty text should be rejected") }
}

func TestMessageAllowsLinks(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	// URLs are now welcome in messages
	cases := []string{
		"check out https://kapoost.github.io/humanmcp great site",
		"see https://github.com/kapoost for the code",
		"visit www.example.com for more info",
	}
	for _, text := range cases {
		_, err := ms.Save("test", text, "")
		if err != nil { t.Errorf("links should be allowed: %q — %v", text, err) }
	}
}

func TestMessageRejectsHTML(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	cases := []string{
		"<script>alert(1)</script>",
		"<b>bold</b>",
		"onclick=evil()",
	}
	for _, text := range cases {
		_, err := ms.Save("", text, "")
		if err == nil { t.Errorf("should reject HTML: %q", text) }
	}
}

func TestMessageTruncates(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	long := strings.Repeat("a", 2500)
	m, err := ms.Save("", long, "")
	if err != nil { t.Fatalf("Save: %v", err) }
	if len([]rune(m.Text)) > 2000 { t.Errorf("text should be truncated to 280, got %d", len(m.Text)) }
}

func TestMessageWithRegarding(t *testing.T) {
	dir := t.TempDir()
	ms := NewMessageStore(dir)

	m, err := ms.Save("bob", "Great poem.", "my-slug")
	if err != nil { t.Fatalf("Save: %v", err) }
	if m.Regarding != "my-slug" { t.Errorf("regarding: got %q", m.Regarding) }
}
