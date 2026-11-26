package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
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

func TestMove(t *testing.T) {
	j := Jail{Root: t.TempDir()}
	for _, d := range []string{"dir", "dest"} {
		if err := j.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a.txt", "b.txt", "dir/c.txt", "dest/b.txt"} {
		if _, err := j.WriteFile(f, strings.NewReader(f)); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Move([]string{"a.txt", "dir"}, "dest"); err != nil {
		t.Fatal(err)
	}
	if b, err := j.ReadFile("dest/dir/c.txt"); err != nil || string(b) != "dir/c.txt" {
		t.Fatalf("moved folder: %q %v", b, err)
	}
	if _, err := j.Stat("a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a.txt still at the root: %v", err)
	}
	var ee *ExistsError
	if err := j.Move([]string{"b.txt"}, "dest"); !errors.As(err, &ee) || ee.Name != "b.txt" || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("existing name: %v", err)
	}
	if b, _ := j.ReadFile("dest/b.txt"); string(b) != "dest/b.txt" {
		t.Fatalf("existing file replaced: %q", b)
	}
	for _, dest := range []string{"dest", "dest/dir"} {
		if err := j.Move([]string{"dest"}, dest); !errors.Is(err, ErrIntoItself) {
			t.Fatalf("into itself (%s): %v", dest, err)
		}
	}
	if err := j.Move([]string{"dest/a.txt"}, "dest/a.txt"); err == nil {
		t.Fatal("moved into a file")
	}
	if err := j.Move([]string{"dest/a.txt"}, "dest"); err != nil { // already there: nothing to do
		t.Fatal(err)
	}
	if err := j.Move([]string{"dest/a.txt"}, "../new/x"); err != nil { // stays in the jail, folder created
		t.Fatal(err)
	}
	if _, err := j.Stat("new/x/a.txt"); err != nil {
		t.Fatalf("move into a new folder: %v", err)
	}
}

func TestCompressNestedKeepsRelativeNames(t *testing.T) {
	j := Jail{Root: t.TempDir()}
	if err := j.Mkdir("public/site/css"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.WriteFile("public/site/css/main.css", strings.NewReader("body{}")); err != nil {
		t.Fatal(err)
	}
	if err := j.Compress([]string{"public/site"}, "public/site.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Extract("public/site.tar.gz", "public/copy"); err != nil {
		t.Fatal(err)
	}
	if b, err := j.ReadFile("public/copy/site/css/main.css"); err != nil || string(b) != "body{}" {
		t.Fatalf("extracted layout: %q %v", b, err)
	}
}
