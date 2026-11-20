package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openplex.env")
	content := "# settings\n" +
		"OPENPLEX_PANEL_DOMAIN=panel.example.com\n" +
		"OPENPLEX_ACME_EMAIL=\"ops@example.com\"\n" +
		"OPENPLEX_LISTEN=0.0.0.0:7080\n" +
		"OTHER=ignored\n" +
		"not a setting\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENPLEX_LISTEN", "127.0.0.1:9000") // the environment wins
	t.Setenv("OPENPLEX_PANEL_DOMAIN", "")
	os.Unsetenv("OPENPLEX_PANEL_DOMAIN")
	t.Setenv("OPENPLEX_ACME_EMAIL", "")
	os.Unsetenv("OPENPLEX_ACME_EMAIL")
	t.Setenv("OTHER", "")
	os.Unsetenv("OTHER")

	if err := ApplyEnvFile(path); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ key, want string }{
		{"OPENPLEX_PANEL_DOMAIN", "panel.example.com"},
		{"OPENPLEX_ACME_EMAIL", "ops@example.com"},
		{"OPENPLEX_LISTEN", "127.0.0.1:9000"},
		{"OTHER", ""},
	}
	for _, tt := range tests {
		if got := os.Getenv(tt.key); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
	if err := ApplyEnvFile(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Errorf("missing file: %v", err)
	}
}
