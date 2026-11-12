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
		for name, get := range map[string]func() (string, error){"install": a.Script, "check": a.CheckScript, "update": a.UpdateScript} {
			s, err := get()
			if err != nil {
				t.Fatalf("%s %s: %v", a.ID, name, err)
			}
			want := "OPX_VERSION="
			if name == "check" {
				want = "OPX_RESULT="
			}
			if !strings.HasPrefix(s, "# Runs as the website's user") || !strings.Contains(s, want) {
				t.Errorf("%s %s: script incomplete", a.ID, name)
			}
			if name == "update" != strings.Contains(s, "\nsite_check\n") {
				t.Errorf("%s %s: the website check is misplaced", a.ID, name)
			}
			if name == "install" != strings.Contains(s, "the document root is not empty") {
				t.Errorf("%s %s: empty document root check misplaced", a.ID, name)
			}
			if strings.Contains(s, "\r") {
				t.Errorf("%s %s: script has CR line endings", a.ID, name)
			}
		}
	}
}

func TestParseUpdates(t *testing.T) {
	out := "==> checking\nnoise\nOPX_RESULT=" +
		`{"version":"6.8.3","core":"7.1.4","items":[{"kind":"plugin","name":"akismet","title":"Akismet\u0007 Anti-spam","version":"5.5","latest":"5.7.2"},{"kind":"theme","name":"","title":"x"},{"kind":"module","name":"token","version":"1.15.0","latest":"1.18.0"}]}` + "\n"
	u, err := ParseUpdates(out)
	if err != nil {
		t.Fatal(err)
	}
	if u.Version != "6.8.3" || u.Core != "7.1.4" || len(u.Items) != 2 || u.Items[0].Title != "Akismet Anti-spam" || u.Items[1].Title != "token" {
		t.Fatalf("got %+v", u)
	}
	if u, err := ParseUpdates(`OPX_RESULT={"version":"5.4.10","core":"","items":null,"manual_extensions":true}`); err != nil || u.Items == nil || !u.ManualExtensions {
		t.Fatalf("empty: %+v %v", u, err)
	}
	for _, bad := range []string{"", "OPX_RESULT=", "OPX_RESULT={not json"} {
		if _, err := ParseUpdates(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	env := strings.Join(UpdateEnv("/sites/a/public", "a.example", true, false), " ")
	if env != "OPX_DOCROOT=/sites/a/public OPX_HOST=a.example OPX_CORE=1 OPX_EXTENSIONS=0" {
		t.Fatalf("env %q", env)
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
