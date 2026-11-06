package files

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestJailBasics(t *testing.T) {
	dir := t.TempDir()
	j := Jail{Root: dir}
	if _, err := j.WriteFile("/a/../b.txt", strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	if b, err := j.ReadFile("b.txt"); err != nil || string(b) != "hi" {
		t.Fatalf("read: %q %v", b, err)
	}
	if err := j.Mkdir("x/y"); err != nil {
		t.Fatal(err)
	}
	ents, err := j.List("/")
	if err != nil || len(ents) != 2 || !ents[0].IsDir || ents[0].Name != "x" {
		t.Fatalf("list: %+v %v", ents, err)
	}
	if err := j.Rename("b.txt", "x/c.txt"); err != nil {
		t.Fatal(err)
	}
	if err := j.Remove("/"); err != ErrRoot {
		t.Fatalf("remove root: %v", err)
	}
	if err := j.Chmod("x/c.txt", 0o4755); err == nil {
		t.Fatal("setuid allowed")
	}
}

func TestJailSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	j := Jail{Root: dir}
	if _, err := j.ReadFile("link/secret"); err == nil {
		t.Fatal("read through escaping symlink")
	}
	if _, err := j.WriteFile("link/new", strings.NewReader("x")); err == nil {
		t.Fatal("write through escaping symlink")
	}
}

func TestExtractZipSlip(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"ok/file.txt", "../evil.txt", "/abs.txt"} {
		w, _ := zw.Create(n)
		w.Write([]byte("data"))
	}
	zw.Close()
	j := Jail{Root: filepath.Join(dir, "site")}
	os.MkdirAll(j.Root, 0o755)
	if _, err := j.WriteFile("a.zip", &buf); err != nil {
		t.Fatal(err)
	}
	n, err := j.Extract("a.zip", "out")
	if err != nil || n != 1 {
		t.Fatalf("extract n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("zip slip wrote outside")
	}
	if b, _ := j.ReadFile("out/ok/file.txt"); string(b) != "data" {
		t.Fatal("entry not extracted")
	}
	if err := j.Compress([]string{"out"}, "out.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if n, err := j.Extract("out.tar.gz", "again"); err != nil || n != 1 {
		t.Fatalf("re-extract n=%d err=%v", n, err)
	}
}

func TestModesIgnoreUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions only")
	}
	old := setUmask(0o077)
	defer setUmask(old)
	j := Jail{Root: t.TempDir()}
	if err := j.Mkdir("a/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.WriteFile("a/b/index.php", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{"a": 0o755, "a/b": 0o755, "a/b/index.php": 0o644} {
		st, err := os.Stat(filepath.Join(j.Root, p))
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v (err %v)", p, st.Mode().Perm(), want, err)
		}
	}
}
