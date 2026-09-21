package v2_test

import (
	"strings"
	"testing"
)

// Certyfikat i request_license opisują TĘ SAMĄ licencję. 21 września 2026
// rozjechały się: certyfikat przestał zapraszać do kupna pełni praw, a
// request_license nadal kazał „negotiate rights". Agent dostawał sprzeczne
// odpowiedzi zależnie od tego, które narzędzie zawołał.
func TestAllRightsSaysTheSameInBothTools(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"o-ludziach", "O ludziach", "public", "Treść.", "all-rights"})

	cert := callV2Tool(t, h, "get_certificate", map[string]any{"slug": "o-ludziach"}, nil)
	lic := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "o-ludziach", "intended_use": "quote in a newsletter", "caller_id": "ana",
	}, nil)

	for name, out := range map[string]string{"certyfikat": cert, "request_license": lic} {
		for _, forbidden := range []string{"negotiate rights", "open to selling", "Full IP transfer"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s nadal mówi %q:\n%s", name, forbidden, out)
			}
		}
		if !strings.Contains(out, "NOT for sale") {
			t.Errorf("%s nie mówi, że pełnia praw nie jest na sprzedaż:\n%s", name, out)
		}
	}
	if !strings.Contains(lic, "ask_human") {
		t.Errorf("request_license nie kieruje do kanału z odpowiedzią:\n%s", lic)
	}
}

// Odpowiedź na licencję CC-BY jest ostateczna. „Logged for audit purposes"
// brzmiało jak sprawa w toku — Ana Adams złożyła ten sam wniosek na utwór
// CC-BY trzy razy w ciągu sześciu dni, za każdym razem dostawszy „Permitted".
func TestCCBYLicenseAnswerSaysItIsFinal(t *testing.T) {
	h, _ := gateFixtureWithPieces(t,
		[5]string{"deka", "deka-log", "public", "Wspólny mianownik.", "cc-by"})

	out := callV2Tool(t, h, "request_license", map[string]any{
		"slug": "deka", "intended_use": "quote in the Harbor Point Refresh newsletter",
		"caller_id": "ana-adams",
	}, nil)

	if !strings.Contains(out, "Permitted with attribution") {
		t.Fatalf("CC-BY nie zostało od razu dozwolone:\n%s", out)
	}
	if !strings.Contains(out, "final") || !strings.Contains(out, "no human sign-off") {
		t.Errorf("odpowiedź nie mówi, że nic nie jest w toku:\n%s", out)
	}
}
