package ssl

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	out, err := Render(Config{
		ACMEEmail:   "ops@example.com",
		PanelDomain: "panel.example.com",
		Sites: []Site{
			{ID: 1, Name: "example.com", Aliases: []string{"www.example.com"}, DocumentRoot: "public",
				Backend: BackendFastCGI, Upstream: "opx-site-1:9000", SSL: true, ForceHTTPS: true,
				Redirects: []Redirect{{Path: "/old", Target: "https://example.com/new", Code: 301}}},
			{ID: 2, Name: "app.example.org", Backend: BackendProxy, Upstream: "opx-site-2:3000", SSL: true},
			{ID: 3, Name: "plain.test", Backend: BackendStatic, DocumentRoot: "public"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"email ops@example.com",
		"example.com, www.example.com {",
		"php_fastcgi opx-site-1:9000",
		"root * /srv/sites/example.com/public",
		"redir /old https://example.com/new 301",
		"http://app.example.org, https://app.example.org {",
		"reverse_proxy opx-site-2:3000",
		"http://plain.test {",
		"reverse_proxy unix//run/openplex/openplexd.sock",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderRejectsInjection(t *testing.T) {
	bad := []Site{
		{Name: "evil.com {\n respond hi\n}", Backend: BackendStatic},
		{Name: "ok.com", DocumentRoot: "../../etc", Backend: BackendStatic},
		{Name: "ok.com", Backend: BackendProxy, Upstream: "x:1\n reverse_proxy y"},
		{Name: "ok.com", Backend: BackendStatic, Redirects: []Redirect{{Path: "/a b", Target: "/c", Code: 301}}},
		{Name: "ok.com", Backend: BackendStatic, Redirects: []Redirect{{Path: "/a", Target: "/c\n}", Code: 301}}},
	}
	for i, s := range bad {
		if _, err := Render(Config{Sites: []Site{s}}); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestValidators(t *testing.T) {
	for h, ok := range map[string]bool{"example.com": true, "*.example.com": true, "a.b.c.io": true,
		"Example.com": false, "-a.com": false, "localhost": false, "a..com": false} {
		if ValidHostname(h) != ok {
			t.Errorf("ValidHostname(%q) != %v", h, ok)
		}
	}
	for p, ok := range map[string]bool{"public": true, "": true, "web/dist": true, "/abs": false, "../x": false, "a/../b": false, "a b": false} {
		if ValidDocumentRoot(p) != ok {
			t.Errorf("ValidDocumentRoot(%q) != %v", p, ok)
		}
	}
}
