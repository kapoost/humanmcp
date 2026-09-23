package mcp

import (
	"strings"
	"testing"
)

// Instrukcje serwera jedzie KAŻDY agent przy initialize. Do 23 września 2026
// mówiły „Read + quote freely with attribution" bez zastrzeżeń, podczas gdy
// trzy utwory są all-rights, a jeden non-commercial. Ten test pilnuje, żeby
// bezwarunkowa obietnica nie wróciła przy okazji edycji tekstu.
func TestServerInstructionsDoNotPromiseBlanketRights(t *testing.T) {
	out := RenderServerInstructions("example.test", 43, 24, 35)

	for _, forbidden := range []string{
		"share freely", "reproduce freely", "use freely without",
	} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("instrukcje obiecują bezwarunkowo %q", forbidden)
		}
	}
	if !strings.Contains(out, "all-rights") && !strings.Contains(out, "non-commercial") {
		t.Error("instrukcje nie wspominają, że część utworów ma węższe warunki")
	}
	if !strings.Contains(out, "each read states its own terms") {
		t.Error("instrukcje nie kierują do warunków konkretnego utworu")
	}
}
