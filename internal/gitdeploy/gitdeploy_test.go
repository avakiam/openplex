package gitdeploy

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestValidRepo(t *testing.T) {
	for _, tt := range []struct {
		url string
		ok  bool
	}{
		{"git@github.com:avakiam/openplex.git", true},
		{"git@gitlab.example.com:group/sub/repo.git", true},
		{"ssh://git@git.example.com:2222/owner/repo.git", true},
		{"https://github.com/avakiam/openplex.git", true},
		{"https://codeberg.org/owner/repo", true},
		{"https://user:token@github.com/a/b.git", false},
		{"https://token@github.com/a/b.git", false},
		{"http://github.com/a/b.git", false},
		{"file:///etc", false},
		{"/srv/repo.git", false},
		{"ext::sh -c touch% /tmp/x", false},
		{"-oProxyCommand=x", false},
		{"git@github.com:a/b.git; rm -rf /", false},
		{"git@github.com:../etc", false},
		{"ssh://-oProxyCommand=x/a", false},
		{"https://github.com/a/b?x=1", false},
		{"", false},
	} {
		if err := ValidRepo(tt.url); (err == nil) != tt.ok {
			t.Errorf("%q: err %v, want ok %v", tt.url, err, tt.ok)
		}
	}
}

func TestValidBranchAndTarget(t *testing.T) {
	for b, ok := range map[string]bool{"main": true, "release/2.0": true, "feature_x-1": true, "-x": false, "a..b": false,
		"a b": false, "x.lock": false, "/main": false, "a//b": false, "": false} {
		if (ValidBranch(b) == nil) != ok {
			t.Errorf("branch %q", b)
		}
	}
	for in, want := range map[string]string{"": "", "public": "public", "/app/": "app", "./a/b": "a/b", `app\sub`: "app/sub"} {
		if got, err := CleanTarget(in); err != nil || got != want {
			t.Errorf("target %q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"../x", "a/../../b", "logs", "logs/x"} {
		if _, err := CleanTarget(bad); err == nil {
			t.Errorf("target %q accepted", bad)
		}
	}
}

func TestNewKeyAndResult(t *testing.T) {
	priv, pub, err := NewKey("openplex-site-7")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " openplex-site-7") ||
		!strings.Contains(pub, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))) {
		t.Fatalf("public key %q does not match", pub)
	}
	c, s := Result("==> fetching\nOPX_COMMIT=0123abcd\nOPX_SUBJECT=Fix the header\n")
	if c != "0123abcd" || s != "Fix the header" {
		t.Fatalf("result %q %q", c, s)
	}
	if PushedBranch("refs/heads/main") != "main" || PushedBranch("refs/tags/v1") != "" || PushedBranch("") != "" {
		t.Fatal("PushedBranch")
	}
}
