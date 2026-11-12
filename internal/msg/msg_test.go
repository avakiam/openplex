package msg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestMessage(t *testing.T) {
	m := New("{name} needs PHP {version} or later", "name", "Drupal", "version", "8.3")
	if m.Error() != "Drupal needs PHP 8.3 or later" {
		t.Errorf("Error() = %q", m.Error())
	}
	if got, ok := From(m); !ok || got != m {
		t.Error("From(m) failed")
	}
	if _, ok := From(fmt.Errorf("x: %w", m)); ok {
		t.Error("a message wrapped in more text must not keep its template")
	}
	if got, ok := From(fmt.Errorf("%w", m)); !ok || got != m {
		t.Error("a message wrapped without more text keeps its template")
	}
	if _, ok := From(errors.New("plain")); ok {
		t.Error("plain error taken for a message")
	}
}

var (
	newRe = regexp.MustCompile(`msg\.New\(\s*("(?:[^"\\]|\\.)*")`)
	phRe  = regexp.MustCompile(`\{(\w+)\}`)
)

// TestTranslated checks that every template has a Spanish and a Catalan
// translation with the same placeholders.
func TestTranslated(t *testing.T) {
	root := filepath.Join("..", "..")
	templates := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range newRe.FindAllStringSubmatch(string(b), -1) {
			s, err := strconv.Unquote(m[1])
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			templates[s] = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("no msg.New templates found")
	}
	for _, lang := range []string{"es", "ca"} {
		raw, err := os.ReadFile(filepath.Join(root, "web", "locales", lang, "server.json"))
		if err != nil {
			t.Fatal(err)
		}
		var cat map[string]string
		if err := json.Unmarshal(raw, &cat); err != nil {
			t.Fatal(err)
		}
		for tmpl, file := range templates {
			tr, ok := cat[tmpl]
			if !ok {
				t.Errorf("%s: %q has no %s translation in web/locales/%s/server.json", file, tmpl, lang, lang)
				continue
			}
			want := phRe.FindAllString(tmpl, -1)
			for _, ph := range want {
				if !strings.Contains(tr, ph) {
					t.Errorf("%s translation of %q lacks %s", lang, tmpl, ph)
				}
			}
		}
	}
}
