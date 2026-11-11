package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"0.18.0", "0.17.1", true}, {"v1.0.0", "0.99.9", true}, {"0.17.1", "0.17.1", false},
		{"0.17.0", "0.17.1", false}, {"0.18.0", "dev", false}, {"0.18.0-rc1", "0.17.0", false}, {"0.10.0", "0.9.0", true},
	} {
		if got := Newer(tt.a, tt.b); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func testKey(t *testing.T) (map[string]ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]ed25519.PublicKey{"test-key": pub}, priv
}

func TestVerifySums(t *testing.T) {
	keys, priv := testKey(t)
	sums := []byte("abc  openplexd-linux-amd64\n")
	sig, _ := Sign(sums, "test-key", priv)
	if err := VerifySums(sums, sig, keys); err != nil {
		t.Fatal(err)
	}
	if err := VerifySums([]byte("abd  openplexd-linux-amd64\n"), sig, keys); !errors.Is(err, ErrBadRelease) {
		t.Fatalf("tampered sums: %v", err)
	}
	other, _ := Sign(sums, "other-key", priv)
	if err := VerifySums(sums, other, keys); !errors.Is(err, ErrBadRelease) {
		t.Fatalf("unknown key: %v", err)
	}
	if err := VerifySums(sums, sig, TrustedKeys); !errors.Is(err, ErrBadRelease) {
		t.Fatal("a test key passed as a release key")
	}
}

// fakeGitHub serves a release whose binary is a script printing version.
func fakeGitHub(t *testing.T, version string, priv ed25519.PrivateKey, mutate func(name string, b []byte) []byte) *httptest.Server {
	bin := []byte("#!/bin/sh\necho " + version + "\n")
	sum := sha256.Sum256(bin)
	name := "openplexd-linux-" + runtime.GOARCH
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	sig, _ := Sign(sums, "test-key", priv)
	files := map[string][]byte{name: bin, "SHA256SUMS": sums, "SHA256SUMS.sig": sig}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			var assets []map[string]string
			for n := range files {
				assets = append(assets, map[string]string{"name": n, "browser_download_url": srv.URL + "/dl/" + n})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "name": "OpenPlex " + version, "body": "notes",
				"html_url": "https://example.test", "published_at": time.Now(), "assets": assets})
			return
		}
		n := strings.TrimPrefix(r.URL.Path, "/dl/")
		b := files[n]
		if mutate != nil {
			b = mutate(n, b)
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("runs the downloaded script")
	}
	keys, priv := testKey(t)
	ctx := context.Background()
	srv := fakeGitHub(t, "0.18.0", priv, nil)
	dir := t.TempDir()
	var started []string
	m := New(Options{Current: "0.17.1", DataDir: dir, DBPath: filepath.Join(dir, "openplex.db"), URL: srv.URL + "/latest", Keys: keys,
		Start: func(_ context.Context, runner string, args []string) error {
			started = append([]string{runner}, args...)
			return nil
		}})
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	st := m.Status(ctx)
	if !st.Available || !st.CanInstall || st.Latest.Version != "0.18.0" || st.Latest.Notes != "notes" {
		t.Fatalf("status: %+v", st)
	}
	if err := m.Install(ctx, "0.17.9"); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("wrong version: %v", err)
	}
	if err := m.Install(ctx, "0.18.0"); err != nil {
		t.Fatal(err)
	}
	if len(started) != 5 || started[1] != "update" || started[2] != "apply" {
		t.Fatalf("apply not started: %v", started)
	}
	p, err := m.readPending()
	if err != nil || p.From != "0.17.1" || p.To != "0.18.0" {
		t.Fatalf("pending: %+v %v", p, err)
	}
	if m.Status(ctx).State != "installing" {
		t.Error("state not installing")
	}
	if err := m.Install(ctx, "0.18.0"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second install: %v", err)
	}
	// The new version confirms.
	m2 := New(Options{Current: "0.18.0", DataDir: dir})
	m2.Confirm()
	if p, _ := m2.readPending(); !p.Confirmed {
		t.Fatal("not confirmed")
	}
	// Dev builds can't install.
	dev := New(Options{Current: "abc123-dirty", DataDir: t.TempDir(), URL: srv.URL + "/latest", Keys: keys})
	dev.Check(ctx)
	if st := dev.Status(ctx); st.CanInstall || st.Reason == "" {
		t.Fatalf("dev build: %+v", st)
	}
}

func TestInstallRejectsTampering(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	keys, priv := testKey(t)
	ctx := context.Background()
	for what, mutate := range map[string]func(string, []byte) []byte{
		"binary": func(n string, b []byte) []byte {
			if strings.HasPrefix(n, "openplexd-") {
				return append(b, "# evil\n"...)
			}
			return b
		},
		"sums": func(n string, b []byte) []byte {
			if n == "SHA256SUMS" {
				return append(b, "x\n"...)
			}
			return b
		},
	} {
		srv := fakeGitHub(t, "0.18.0", priv, mutate)
		dir := t.TempDir()
		m := New(Options{Current: "0.17.1", DataDir: dir, URL: srv.URL + "/latest", Keys: keys,
			Start: func(context.Context, string, []string) error { t.Fatal("started"); return nil }})
		m.Check(ctx)
		if err := m.Install(ctx, "0.18.0"); !errors.Is(err, ErrBadRelease) {
			t.Errorf("%s tampered: %v", what, err)
		}
		if o := m.LastOutcome(); o == nil || o.Result != "failed" {
			t.Errorf("%s: outcome %+v", what, o)
		}
		if _, err := os.Stat(filepath.Join(dir, "updates", "pending.json")); err == nil {
			t.Errorf("%s: pending left behind", what)
		}
	}
}

// applyFixture prepares an installed binary, a database and a pending update.
func applyFixture(t *testing.T) (dir, exe, dbPath string) {
	dir = t.TempDir()
	exe = filepath.Join(dir, "bin", "openplexd")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("old binary"), 0o755)
	dbPath = filepath.Join(dir, "openplex.db")
	os.WriteFile(dbPath, []byte("old db"), 0o600)
	os.MkdirAll(filepath.Join(dir, "updates"), 0o700)
	newBin := filepath.Join(dir, "updates", "openplexd-0.18.0")
	os.WriteFile(newBin, []byte("new binary"), 0o700)
	if err := writeJSON(filepath.Join(dir, "updates", "pending.json"), pending{From: "0.17.1", To: "0.18.0", Binary: newBin, Exe: exe, DB: dbPath,
		Unit: "openplexd.service", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return dir, exe, dbPath
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestApplyConfirmed(t *testing.T) {
	dir, exe, dbPath := applyFixture(t)
	var calls []string
	err := Apply(dir, ApplyOptions{Wait: 2 * time.Second, Poll: 10 * time.Millisecond, Systemctl: func(a ...string) error {
		calls = append(calls, strings.Join(a, " "))
		if a[0] == "start" { // the new version starts, migrates and confirms
			os.WriteFile(dbPath, []byte("migrated db"), 0o600)
			New(Options{Current: "0.18.0", DataDir: dir}).Confirm()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if read(exe) != "new binary" || read(dbPath) != "migrated db" {
		t.Fatal("not installed")
	}
	if read(filepath.Join(dir, "updates", "backup-0.17.1", "openplexd")) != "old binary" ||
		read(filepath.Join(dir, "updates", "backup-0.17.1", "openplex.db")) != "old db" {
		t.Fatal("no backup")
	}
	if o := New(Options{DataDir: dir}).LastOutcome(); o == nil || o.Result != "updated" {
		t.Fatalf("outcome: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(dir, "updates", "pending.json")); err == nil {
		t.Fatal("pending left")
	}
	if fmt.Sprint(calls) != "[stop openplexd.service start openplexd.service]" {
		t.Fatalf("systemctl: %v", calls)
	}
}

func TestApplyRollsBack(t *testing.T) {
	dir, exe, dbPath := applyFixture(t)
	starts := 0
	err := Apply(dir, ApplyOptions{Wait: 200 * time.Millisecond, Poll: 10 * time.Millisecond, Systemctl: func(a ...string) error {
		if a[0] == "start" {
			starts++
			if starts == 1 { // the new version migrates the database, then fails
				os.WriteFile(dbPath, []byte("half-migrated db"), 0o600)
				os.WriteFile(dbPath+"-wal", []byte("wal"), 0o600)
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if read(exe) != "old binary" || read(dbPath) != "old db" {
		t.Fatalf("not restored: %q %q", read(exe), read(dbPath))
	}
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		t.Fatal("the new version's WAL was kept")
	}
	if starts != 2 {
		t.Fatalf("service started %d times", starts)
	}
	o := New(Options{DataDir: dir}).LastOutcome()
	if o == nil || o.Result != "rolled_back" || !strings.Contains(o.Message, "0.17.1 was restored") {
		t.Fatalf("outcome: %+v", o)
	}
}
