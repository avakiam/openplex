// Package files implements the web file manager. Every operation goes
// through os.Root, which makes it impossible to escape the site directory via
// "..", absolute paths or symlinks.
package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Limits.
const (
	MaxEditSize     = 5 << 20  // files larger than this cannot be opened in the editor
	MaxExtractBytes = 10 << 30 // total uncompressed size per extraction
	MaxExtractFiles = 200_000
)

// ErrTooLarge is returned when a file or archive exceeds limits.
var ErrTooLarge = errors.New("file too large")

// ErrRoot is returned for destructive operations on the jail root itself.
var ErrRoot = errors.New("operation not allowed on the site root")

// Jail is a site directory owned by UID/GID.
type Jail struct {
	Root string
	UID  int
	GID  int
}

// Entry is a directory listing item.
type Entry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink"`
	Size      int64     `json:"size"`
	Mode      string    `json:"mode"` // octal permission bits, e.g. "0644"
	ModTime   time.Time `json:"mod_time"`
}

// Clean normalizes a user-supplied path to a root-relative form ("." for root).
func Clean(p string) string {
	p = path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	if p == "/" {
		return "."
	}
	return p[1:]
}

func (j Jail) open() (*os.Root, error) {
	r, err := os.OpenRoot(j.Root)
	if err != nil {
		return nil, fmt.Errorf("open site root: %w", err)
	}
	return r, nil
}

func (j Jail) own(r *os.Root, p string) error {
	if os.Geteuid() != 0 {
		return nil // not root (development): ownership cannot be changed
	}
	return r.Lchown(p, j.UID, j.GID)
}

// List returns the entries of directory p, directories first.
func (j Jail) List(p string) ([]Entry, error) {
	r, err := j.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	p = Clean(p)
	f, err := r.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name: de.Name(), Path: Clean(path.Join(p, de.Name())), IsDir: info.IsDir(),
			IsSymlink: info.Mode()&fs.ModeSymlink != 0, Size: info.Size(),
			Mode: fmt.Sprintf("%04o", info.Mode().Perm()), ModTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].IsDir != out[b].IsDir {
			return out[a].IsDir
		}
		return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
	})
	return out, nil
}

// Stat returns info about p.
func (j Jail) Stat(p string) (fs.FileInfo, error) {
	r, err := j.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Stat(Clean(p))
}

// ReadFile returns the content of a file for the editor.
func (j Jail) ReadFile(p string) ([]byte, error) {
	r, err := j.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	st, err := r.Stat(Clean(p))
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("is a directory")
	}
	if st.Size() > MaxEditSize {
		return nil, ErrTooLarge
	}
	return r.ReadFile(Clean(p))
}

// Open opens a file for download. The caller must close the jail-backed file.
func (j Jail) Open(p string) (*os.File, fs.FileInfo, error) {
	r, err := j.open()
	if err != nil {
		return nil, nil, err
	}
	defer r.Close() // the open *os.File stays valid after the Root is closed
	f, err := r.Open(Clean(p))
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		if err == nil {
			err = errors.New("is a directory")
		}
		return nil, nil, err
	}
	return f, st, nil
}

// WriteFile creates or replaces a file with data from src (preserving mode if it exists).
func (j Jail) WriteFile(p string, src io.Reader) (int64, error) {
	r, err := j.open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	p = Clean(p)
	if p == "." {
		return 0, ErrRoot
	}
	mode := fs.FileMode(0o644)
	if st, err := r.Stat(p); err == nil {
		if st.IsDir() {
			return 0, errors.New("is a directory")
		}
		mode = st.Mode().Perm()
	}
	tmp := path.Join(path.Dir(p), fmt.Sprintf(".opx-upload-%d", time.Now().UnixNano()))
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = r.Remove(tmp)
		return 0, err
	}
	if err := r.Rename(tmp, p); err != nil {
		_ = r.Remove(tmp)
		return 0, err
	}
	return n, j.own(r, p)
}

// Mkdir creates a directory (and parents).
func (j Jail) Mkdir(p string) error {
	r, err := j.open()
	if err != nil {
		return err
	}
	defer r.Close()
	p = Clean(p)
	if err := r.MkdirAll(p, 0o755); err != nil {
		return err
	}
	return j.chownTree(r, p)
}

// chownTree chowns p and each ancestor up to the root.
func (j Jail) chownTree(r *os.Root, p string) error {
	for ; p != "." && p != "/"; p = path.Dir(p) {
		if err := j.own(r, p); err != nil {
			return err
		}
	}
	return nil
}

// Rename moves from → to.
func (j Jail) Rename(from, to string) error {
	r, err := j.open()
	if err != nil {
		return err
	}
	defer r.Close()
	from, to = Clean(from), Clean(to)
	if from == "." || to == "." {
		return ErrRoot
	}
	if _, err := r.Lstat(to); err == nil {
		return fs.ErrExist
	}
	return r.Rename(from, to)
}

// Remove deletes a file or directory tree.
func (j Jail) Remove(p string) error {
	r, err := j.open()
	if err != nil {
		return err
	}
	defer r.Close()
	p = Clean(p)
	if p == "." {
		return ErrRoot
	}
	if _, err := r.Lstat(p); err != nil {
		return err
	}
	return r.RemoveAll(p)
}

// Chmod sets permission bits (setuid/setgid/sticky are never allowed).
func (j Jail) Chmod(p string, mode uint32) error {
	if mode > 0o777 {
		return errors.New("mode must be between 0000 and 0777")
	}
	r, err := j.open()
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Chmod(Clean(p), fs.FileMode(mode))
}

// Extract unpacks a .zip, .tar.gz/.tgz or .tar archive located at archivePath
// into directory dest. Symlinks, hard links and device files are skipped.
func (j Jail) Extract(archivePath, dest string) (int, error) {
	r, err := j.open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	dest = Clean(dest)
	if err := r.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	dr, err := r.OpenRoot(dest)
	if err != nil {
		return 0, err
	}
	defer dr.Close()
	f, err := r.Open(Clean(archivePath))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	x := &extractor{root: dr, jail: j}
	name := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(name, ".zip"):
		err = x.zip(f, st.Size())
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		gz, gerr := gzip.NewReader(f)
		if gerr != nil {
			return 0, gerr
		}
		defer gz.Close()
		err = x.tar(gz)
	case strings.HasSuffix(name, ".tar"):
		err = x.tar(f)
	default:
		return 0, errors.New("unsupported archive type (use .zip, .tar.gz, .tgz or .tar)")
	}
	return x.files, err
}

type extractor struct {
	root  *os.Root
	jail  Jail
	files int
	bytes int64
}

func (x *extractor) entryPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
		return "", false // zip-slip attempt: skip
	}
	p := Clean(name)
	return p, p != "."
}

func (x *extractor) dir(p string) error {
	if err := x.root.MkdirAll(p, 0o755); err != nil {
		return err
	}
	return x.jail.chownTreeIn(x.root, p)
}

func (x *extractor) file(p string, mode fs.FileMode, src io.Reader) error {
	x.files++
	if x.files > MaxExtractFiles {
		return fmt.Errorf("%w: more than %d entries", ErrTooLarge, MaxExtractFiles)
	}
	if d := path.Dir(p); d != "." {
		if err := x.dir(d); err != nil {
			return err
		}
	}
	perm := mode.Perm() & 0o755
	if perm == 0 {
		perm = 0o644
	}
	f, err := x.root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm|0o600)
	if err != nil {
		return err
	}
	remaining := MaxExtractBytes - x.bytes
	n, err := io.Copy(f, io.LimitReader(src, remaining+1))
	x.bytes += n
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if x.bytes > MaxExtractBytes {
		return fmt.Errorf("%w: archive expands beyond %d bytes", ErrTooLarge, int64(MaxExtractBytes))
	}
	return x.jail.own(x.root, p)
}

func (x *extractor) zip(ra io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		p, ok := x.entryPath(zf.Name)
		if !ok {
			continue
		}
		mode := zf.Mode()
		switch {
		case mode.IsDir():
			if err := x.dir(p); err != nil {
				return err
			}
		case mode.IsRegular():
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = x.file(p, mode, rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", zf.Name, err)
			}
		}
	}
	return nil
}

func (x *extractor) tar(src io.Reader) error {
	tr := tar.NewReader(src)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		p, ok := x.entryPath(h.Name)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := x.dir(p); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := x.file(p, fs.FileMode(h.Mode), tr); err != nil {
				return fmt.Errorf("%s: %w", h.Name, err)
			}
		}
	}
}

// chownTreeIn chowns p and its ancestors inside root r.
func (j Jail) chownTreeIn(r *os.Root, p string) error { return j.chownTree(r, p) }

// Compress writes a .tar.gz of the given root-relative paths to dst (inside the jail).
func (j Jail) Compress(paths []string, dst string) error {
	r, err := j.open()
	if err != nil {
		return err
	}
	defer r.Close()
	dst = Clean(dst)
	out, err := r.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	werr := func() error {
		fsys := r.FS()
		for _, p := range paths {
			p = Clean(p)
			if p == dst {
				continue
			}
			err := fs.WalkDir(fsys, p, func(fp string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if fp == dst {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				if !info.IsDir() && !info.Mode().IsRegular() {
					return nil
				}
				h, err := tar.FileInfoHeader(info, "")
				if err != nil {
					return err
				}
				h.Name = fp
				h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
				if err := tw.WriteHeader(h); err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				f, err := fsys.Open(fp)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = io.Copy(tw, f)
				return err
			})
			if err != nil {
				return err
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gz.Close()
	}()
	if cerr := out.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = r.Remove(dst)
		return werr
	}
	return j.own(r, dst)
}
