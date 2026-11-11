package apps

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	wp, _ := Get("wordpress")
	jo, _ := Get("joomla")
	ok := Params{Title: " My site ", AdminUser: "admin", AdminEmail: "me@example.com", AdminPassword: "correct-horse-1", Language: "es"}
	tests := []struct {
		name    string
		app     App
		mod     func(p *Params)
		wantErr bool
		lang    string
	}{
		{"valid", wp, func(p *Params) {}, false, "es"},
		{"language not offered falls back", jo, func(p *Params) {}, false, "en"},
		{"unknown language", wp, func(p *Params) { p.Language = "fr" }, false, "en"},
		{"empty title", wp, func(p *Params) { p.Title = "  " }, true, ""},
		{"control char in title", wp, func(p *Params) { p.Title = "a\nb" }, true, ""},
		{"short user", wp, func(p *Params) { p.AdminUser = "ab" }, true, ""},
		{"user with space", wp, func(p *Params) { p.AdminUser = "my admin" }, true, ""},
		{"user with quote", wp, func(p *Params) { p.AdminUser = "adm'in" }, true, ""},
		{"bad email", wp, func(p *Params) { p.AdminEmail = "Me <me@example.com>" }, true, ""},
		{"short password", wp, func(p *Params) { p.AdminPassword = "short" }, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ok
			tt.mod(&p)
			err := tt.app.Validate(&p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if err == nil && (p.Language != tt.lang || p.Title != "My site") {
				t.Fatalf("got language %q title %q", p.Language, p.Title)
			}
		})
	}
}

func TestSupportsPHP(t *testing.T) {
	jo, _ := Get("joomla")
	wp, _ := Get("wordpress")
	for _, tt := range []struct {
		app  App
		v    string
		want bool
	}{{jo, "8.2", false}, {jo, "8.3", true}, {jo, "8.4", true}, {jo, "10.0", true}, {wp, "7.4", true}} {
		if got := tt.app.SupportsPHP(tt.v); got != tt.want {
			t.Errorf("%s on PHP %s = %v, want %v", tt.app.ID, tt.v, got, tt.want)
		}
	}
}

func TestScripts(t *testing.T) {
	for _, a := range Catalog {
		s, err := a.Script()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(s, "# Runs as the website's user") || !strings.Contains(s, "OPX_VERSION=") {
			t.Errorf("%s: script incomplete", a.ID)
		}
		if strings.Contains(s, "\r") {
			t.Errorf("%s: script has CR line endings", a.ID)
		}
	}
}

func TestVersionAndDocroot(t *testing.T) {
	if v := Version("==> installing\nOPX_VERSION=7.1.4\n"); v != "7.1.4" {
		t.Fatalf("version %q", v)
	}
	idx := func(b string) func() ([]byte, error) { return func() ([]byte, error) { return []byte(b), nil } }
	fail := func() ([]byte, error) { return nil, errors.New("x") }
	cases := []struct {
		names []string
		index func() ([]byte, error)
		want  bool
	}{
		{nil, fail, true},
		{[]string{"index.html"}, idx("<p>This site is hosted with OpenPlex. Upload…</p>"), true},
		{[]string{"index.html"}, idx("<p>my own page</p>"), false},
		{[]string{"index.html"}, fail, false},
		{[]string{"index.php"}, fail, false},
		{[]string{"index.html", "x"}, idx("This site is hosted with OpenPlex"), false},
	}
	for i, c := range cases {
		if got := EmptyDocroot(c.names, c.index); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}
