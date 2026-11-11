package extensions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodManifest = `{
  "schema": 1,
  "id": "avakiam.test",
  "name": {"en": "Test", "es": "Prueba"},
  "version": "1.2.3",
  "publisher": {"name": "Avakiam"},
  "openplex": ">=0.16.0",
  "permissions": ["domains:read", "dns:write"],
  "events": ["domain.created"],
  "backend": {"image": "ghcr.io/avakiam/test:1.2.3", "archives": {"amd64": "images/amd64.tar"}, "port": 8080},
  "settings": [{"key": "api_token", "type": "secret", "label": "API token", "required": true}],
  "pages": [{"id": "main", "title": "Test", "path": "/"}],
  "site_tabs": [{"id": "dns", "title": "Test DNS", "path": "/site", "area": "dns"}]
}`

func TestParseManifest(t *testing.T) {
	m, err := Parse([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Backend.MemoryMB != 128 || m.Backend.CPUs != 0.5 || m.Pages[0].Roles[0] != "admin" || m.Name.In("ca") != "Test" || m.Name.In("es") != "Prueba" {
		t.Fatalf("defaults not applied: %+v", m)
	}
	if !m.Compatible("0.16.0") || !m.Compatible("1.0.0") || m.Compatible("0.15.9") || !m.Compatible("dev") {
		t.Error("version check")
	}
	for name, bad := range map[string]string{
		"bad id":         `"id": "avakiam.test"`,
		"bad permission": `"dns:write"`,
		"traversal":      `"images/amd64.tar"`,
		"bad port":       `"port": 8080`,
		"unknown field":  `"schema": 1`,
		"page path":      `"path": "/site"`,
	} {
		repl := map[string]string{
			"bad id":         `"id": "Avakiam/Test"`,
			"bad permission": `"root:everything"`,
			"traversal":      `"../../etc/passwd"`,
			"bad port":       `"port": 80`,
			"unknown field":  `"schema": 1, "privileged": true`,
			"page path":      `"path": "/../admin"`,
		}[name]
		if _, err := Parse([]byte(strings.Replace(goodManifest, bad, repl, 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func writeExt(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(goodManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "amd64.tar"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackAndOpen(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	trusted := map[string]ed25519.PublicKey{"test-key": pub}
	dir := writeExt(t)

	var signed, unsigned bytes.Buffer
	if _, err := Pack(dir, &signed, "test-key", priv); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(dir, &unsigned, "", nil); err != nil {
		t.Fatal(err)
	}

	m, signer, err := Open(bytes.NewReader(signed.Bytes()), filepath.Join(t.TempDir(), "a"), trusted)
	if err != nil || signer != "test-key" || m.ID != "avakiam.test" {
		t.Fatalf("signed: %v %q %v", m, signer, err)
	}
	if _, signer, err = Open(bytes.NewReader(unsigned.Bytes()), filepath.Join(t.TempDir(), "b"), trusted); err != nil || signer != "" {
		t.Fatalf("unsigned: %q %v", signer, err)
	}
	// Signed with a key nobody trusts: installable, but not verified.
	if _, signer, err = Open(bytes.NewReader(signed.Bytes()), filepath.Join(t.TempDir(), "c"), map[string]ed25519.PublicKey{}); err != nil || signer != "" {
		t.Fatalf("unknown key: %q %v", signer, err)
	}
	// Changing the package after signing breaks the signature.
	tampered := retar(t, signed.Bytes(), func(name string, b []byte) []byte {
		if name == innerName {
			b[len(b)/2] ^= 0xff
		}
		return b
	})
	if _, _, err = Open(bytes.NewReader(tampered), filepath.Join(t.TempDir(), "d"), trusted); err != ErrBadSignature {
		t.Fatalf("tampered: %v", err)
	}
}

func TestOpenRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../evil", "/abs", "a/../../evil"} {
		var inner bytes.Buffer
		gz := gzip.NewWriter(&inner)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: ManifestFile, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(goodManifest))})
		_, _ = tw.Write([]byte(goodManifest))
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
		_ = tw.Close()
		_ = gz.Close()
		var outer bytes.Buffer
		ow := tar.NewWriter(&outer)
		_ = ow.WriteHeader(&tar.Header{Name: innerName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(inner.Len())})
		_, _ = ow.Write(inner.Bytes())
		_ = ow.Close()
		dest := filepath.Join(t.TempDir(), "x")
		if _, _, err := Open(&outer, dest, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Errorf("%s: left files behind", name)
		}
	}
	// A symlink entry is refused too.
	var inner bytes.Buffer
	gz := gzip.NewWriter(&inner)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gz.Close()
	var outer bytes.Buffer
	ow := tar.NewWriter(&outer)
	_ = ow.WriteHeader(&tar.Header{Name: innerName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(inner.Len())})
	_, _ = ow.Write(inner.Bytes())
	_ = ow.Close()
	if _, _, err := Open(&outer, filepath.Join(t.TempDir(), "y"), nil); err == nil {
		t.Error("symlink accepted")
	}
}

// retar rewrites an outer package tar, passing each file through fn.
func retar(t *testing.T, b []byte, fn func(string, []byte) []byte) []byte {
	tr := tar.NewReader(bytes.NewReader(b))
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		var data bytes.Buffer
		_, _ = data.ReadFrom(tr)
		nb := fn(h.Name, data.Bytes())
		h.Size = int64(len(nb))
		_ = tw.WriteHeader(h)
		_, _ = tw.Write(nb)
	}
	_ = tw.Close()
	return out.Bytes()
}
