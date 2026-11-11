package extensions

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A package (.opx) is a tar holding:
//
//	package.tar.gz   the extension: openplex-extension.json, image archives…
//	signature.json   optional: {"key_id": "...", "signature": base64(Ed25519(SHA-256(package.tar.gz)))}
const (
	innerName = "package.tar.gz"
	sigName   = "signature.json"

	// MaxPackageSize bounds uploads and downloads.
	MaxPackageSize = 1 << 30
	maxFiles       = 2000
)

type signature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

// SigningKey is a private key file made by `openplexd ext keygen`.
type SigningKey struct {
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"` // base64 of the 64-byte Ed25519 key
}

// LoadSigningKey reads a key file.
func LoadSigningKey(file string) (string, ed25519.PrivateKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", nil, err
	}
	var k SigningKey
	if err := json.Unmarshal(b, &k); err != nil {
		return "", nil, fmt.Errorf("signing key: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(k.PrivateKey)
	if err != nil || len(raw) != ed25519.PrivateKeySize || k.KeyID == "" {
		return "", nil, errors.New("signing key: invalid file")
	}
	return k.KeyID, ed25519.PrivateKey(raw), nil
}

// Pack writes the extension in dir as a package to w, signed when key is set.
// It validates the manifest and the files it references first.
func Pack(dir string, w io.Writer, keyID string, key ed25519.PrivateKey) (*Manifest, error) {
	mb, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	m, err := Parse(mb)
	if err != nil {
		return nil, err
	}
	for arch, f := range m.Backend.Archives {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("backend.archives.%s: %s not found", arch, f)
		}
	}
	tmp, err := os.CreateTemp("", "opx-pack-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(tmp, h))
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(path.Base(rel), ".") { // .git, .DS_Store…
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		case fi.Mode().IsRegular():
			if err := tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: 0o644, Size: fi.Size()}); err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return fmt.Errorf("%s: only regular files and directories can be packed", rel)
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err != nil {
		return nil, err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	out := tar.NewWriter(w)
	if err := out.WriteHeader(&tar.Header{Name: innerName, Typeflag: tar.TypeReg, Mode: 0o644, Size: size}); err != nil {
		return nil, err
	}
	if _, err := io.Copy(out, tmp); err != nil {
		return nil, err
	}
	if key != nil {
		sig, _ := json.Marshal(signature{KeyID: keyID, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, h.Sum(nil)))})
		if err := out.WriteHeader(&tar.Header{Name: sigName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(sig))}); err != nil {
			return nil, err
		}
		if _, err := out.Write(sig); err != nil {
			return nil, err
		}
	}
	return m, out.Close()
}

// ErrBadSignature: the package claims a trusted key but the signature is wrong.
var ErrBadSignature = errors.New("the package's signature is invalid (it was changed after signing)")

// Open reads a package from r into dest (which must not exist), checking its
// signature against trusted. signer is the trusted key id that signed it, or
// "" when the package is unsigned or signed with an unknown key.
func Open(r io.Reader, dest string, trusted map[string]ed25519.PublicKey) (m *Manifest, signer string, err error) {
	if err := os.Mkdir(dest, 0o700); err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	inner := filepath.Join(dest, ".package.tar.gz")
	var sig *signature
	var sum []byte
	tr := tar.NewReader(io.LimitReader(r, MaxPackageSize+1<<20))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", errors.New("not an OpenPlex extension package")
		}
		switch hdr.Name {
		case innerName:
			f, err := os.OpenFile(inner, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return nil, "", err
			}
			h := sha256.New()
			_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(tr, MaxPackageSize))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return nil, "", err
			}
			sum = h.Sum(nil)
		case sigName:
			sig = &signature{}
			if err := json.NewDecoder(io.LimitReader(tr, 4096)).Decode(sig); err != nil {
				return nil, "", errors.New("unreadable signature.json")
			}
		}
	}
	if sum == nil {
		return nil, "", errors.New("not an OpenPlex extension package (no package.tar.gz)")
	}
	if sig != nil {
		if pub, ok := trusted[sig.KeyID]; ok {
			raw, err := base64.StdEncoding.DecodeString(sig.Signature)
			if err != nil || !ed25519.Verify(pub, sum, raw) {
				return nil, "", ErrBadSignature
			}
			signer = sig.KeyID
		}
	}
	if err := extract(inner, filepath.Join(dest, "pkg")); err != nil {
		return nil, "", err
	}
	_ = os.Remove(inner)
	mb, err := os.ReadFile(filepath.Join(dest, "pkg", ManifestFile))
	if err != nil {
		return nil, "", fmt.Errorf("the package has no %s", ManifestFile)
	}
	if m, err = Parse(mb); err != nil {
		return nil, "", err
	}
	for arch, f := range m.Backend.Archives {
		if fi, err := os.Lstat(filepath.Join(dest, "pkg", filepath.FromSlash(f))); err != nil || !fi.Mode().IsRegular() {
			return nil, "", fmt.Errorf("backend.archives.%s: %s is missing from the package", arch, f)
		}
	}
	return m, signer, nil
}

// extract unpacks a tar.gz into dir: regular files and directories only,
// names that stay inside dir, bounded size and count.
func extract(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("package.tar.gz is not gzip")
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("package.tar.gz: %w", err)
		}
		if n > maxFiles {
			return errors.New("too many files in the package")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if !cleanRel(name) {
			return fmt.Errorf("unsafe file name %q in the package", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if total += hdr.Size; total > MaxPackageSize*4 {
				return errors.New("the package is too big once unpacked")
			}
			if d := path.Dir(name); d != "." {
				if err := root.MkdirAll(d, 0o755); err != nil {
					return err
				}
			}
			out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, hdr.Size))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: only regular files and directories are allowed", hdr.Name)
		}
	}
}
