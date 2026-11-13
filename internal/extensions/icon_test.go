package extensions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIconData(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 20)
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string // prefix; "" for none
	}{
		{"none", nil, ""},
		{"svg", map[string]string{"icon.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`}, "data:image/svg+xml;base64,"},
		{"png", map[string]string{"icon.png": png}, "data:image/png;base64,"},
		{"svg wins", map[string]string{"icon.svg": "<svg/>", "icon.png": png}, "data:image/svg+xml;base64,"},
		{"not a png", map[string]string{"icon.png": "hello"}, ""},
		{"too big", map[string]string{"icon.svg": "<svg>" + strings.Repeat(" ", MaxIconSize)}, ""},
		{"broken svg, good png", map[string]string{"icon.svg": "nope", "icon.png": png}, "data:image/png;base64,"},
	} {
		dir := t.TempDir()
		for n, b := range c.files {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got := iconData(dir)
		if (c.want == "") != (got == "") || !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %.40q", c.name, got)
		}
	}
}
