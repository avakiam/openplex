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
				Backend: BackendFastCGI, Upstream: "opx-site-1:8080", SSL: true, ForceHTTPS: true,
				Redirects: []Redirect{{Path: "/old", Target: "https://example.com/new", Code: 301}}},
			{ID: 2, Name: "app.example.org", Backend: BackendProxy, Upstream: "opx-site-2:3000", SSL: true},
			{ID: 3, Name: "plain.test", Backend: BackendStatic, DocumentRoot: "public", Upstream: "opx-site-3:8080"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"email ops@example.com",
		"example.com, www.example.com {",
		"reverse_proxy opx-site-1:8080",
		"reverse_proxy opx-site-3:8080",
		"redir /old https://example.com/new 301",
		"http://app.example.org, https://app.example.org {",
		"reverse_proxy opx-site-2:3000",
		"http://plain.test {",
		"reverse_proxy unix//run/openplex/openplexd.sock",
		":80 {\n\timport opx_common\n\trespond \"No website is configured for this address.\" 404",
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

func TestRenderWWW(t *testing.T) {
	out, err := Render(Config{Sites: []Site{
		{ID: 1, Name: "example.com", Aliases: []string{"example.net"}, Backend: BackendStatic, Upstream: "opx-site-1:8080", SSL: true, ForceHTTPS: true, WWW: []string{"example.com", "example.net"}},
		{ID: 2, Name: "plain.test", Backend: BackendStatic, Upstream: "opx-site-2:8080", WWW: []string{"plain.test"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"www.example.com {\n\timport opx_common\n\tredir https://example.com{uri} 308\n}",
		"www.example.net {\n\timport opx_common\n\tredir https://example.com{uri} 308\n}",
		"http://www.plain.test {\n\timport opx_common\n\tredir http://plain.test{uri} 308\n}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := Render(Config{Sites: []Site{{ID: 3, Name: "x.test", Backend: BackendStatic, WWW: []string{"x.test {\n}"}}}}); err == nil {
		t.Error("injected www host rendered")
	}
}
