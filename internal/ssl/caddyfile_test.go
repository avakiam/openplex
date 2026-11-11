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

func TestRenderEdgePolicy(t *testing.T) {
	c := Config{
		Sites:           []Site{{ID: 1, Name: "shop.example.com", Aliases: []string{"tienda.example.com"}, Backend: BackendStatic, Upstream: "opx-site-1:8080", SSL: true, ForceHTTPS: true}},
		TrustedProxies:  []string{"173.245.48.0/20", "2400:cb00::/32"},
		ClientIPHeaders: []string{"CF-Connecting-IP", "X-Forwarded-For"},
		Guards:          map[string][]string{"tienda.example.com": {"173.245.48.0/20"}},
		GuardExempt:     []string{"172.18.0.1/32"},
	}
	out, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"trusted_proxies static 173.245.48.0/20 2400:cb00::/32\n",
		"client_ip_headers CF-Connecting-IP X-Forwarded-For\n",
		"@opx_guard1 {\n\t\thost tienda.example.com\n\t\tnot remote_ip 173.245.48.0/20 172.18.0.1/32\n",
		"respond @opx_guard1 ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "@opx_guard0") {
		t.Error("an unrestricted host was guarded")
	}
	// Anything not canonical is refused, so a bad row can't inject directives.
	for _, bad := range []Config{
		{TrustedProxies: []string{"10.0.0.0/8\n}"}},
		{TrustedProxies: []string{"10.0.0.1/8"}},
		{TrustedProxies: []string{"10.0.0.0/8"}, ClientIPHeaders: []string{"X\nY"}},
		{Sites: c.Sites, Guards: map[string][]string{"shop.example.com": {"1.0.0.0/1"}}},
		{Sites: c.Sites, Guards: map[string][]string{"shop.example.com": {}}},
	} {
		if _, err := Render(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	for in, want := range map[string]string{"173.245.48.7/20": "173.245.48.0/20", "198.51.100.7": "198.51.100.7/32",
		"2606:4700::/32": "2606:4700::/32", " 10.1.2.3/8 ": "10.0.0.0/8"} {
		if got, err := NormalizePrefix(in); err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"0.0.0.0/0", "1.0.0.0/7", "::/0", "2000::/3", "fe80::1%eth0", "::ffff:1.2.3.4/128", "nope", ""} {
		if _, err := NormalizePrefix(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
