package config

import (
	"os"
	"testing"
)

// CONTACT_EMAIL jest deklarowany w fly.toml i decyduje o tym, czy odpowiedź
// ask_human pokaże agentowi drogę mailową dla jego człowieka. Testy zachowania
// ustawiają pole struktury wprost, więc bez tego przypadku nic nie pilnuje
// ogniwa pomiędzy: literówka w nazwie zmiennej wyłączyłaby furtkę po cichu,
// a wszystkie pozostałe testy nadal by przechodziły.
func TestContactEmailComesFromEnv(t *testing.T) {
	t.Setenv("CONTACT_EMAIL", "kapoost@example.test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContactEmail != "kapoost@example.test" {
		t.Errorf("ContactEmail = %q, wanted the value from CONTACT_EMAIL", cfg.ContactEmail)
	}
}

func TestContactEmailEmptyByDefault(t *testing.T) {
	os.Unsetenv("CONTACT_EMAIL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContactEmail != "" {
		t.Errorf("ContactEmail = %q with no env var set; the email option must stay hidden by default", cfg.ContactEmail)
	}
}
