package sftpd

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"openplex.dev/openplex/internal/db"
)

// siteTTL is how long a session trusts its list of websites before asking again.
const siteTTL = 15 * time.Second

// vfs is the filesystem of one SFTP session: "/" lists the websites, and
// "/<website>/…" is that website's directory.
type vfs struct {
	ctx   context.Context
	user  *db.User
	sites func(context.Context, *db.User) ([]Site, error)

	mu      sync.Mutex
	cache   []Site
	fetched time.Time
}

func newFS(ctx context.Context, u *db.User, sites func(context.Context, *db.User) ([]Site, error)) *vfs {
	return &vfs{ctx: ctx, user: u, sites: sites}
}

func (v *vfs) list() ([]Site, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cache != nil && time.Since(v.fetched) < siteTTL {
		return v.cache, nil
	}
	sites, err := v.sites(v.ctx, v.user)
	if err != nil {
		return nil, err
	}
	if sites == nil {
		sites = []Site{}
	}
	v.cache, v.fetched = sites, time.Now()
	return sites, nil
}

// target is a resolved path: the root (site == nil) or a path inside a website.
type target struct {
	site *Site
	rel  string // "." for the website's own directory
}

func (v *vfs) resolve(p string) (target, error) {
	p = path.Clean("/" + p)
	if p == "/" {
		return target{}, nil
	}
	name, rest, _ := strings.Cut(p[1:], "/")
	sites, err := v.list()
	if err != nil {
		return target{}, err
	}
	i := slices.IndexFunc(sites, func(s Site) bool { return s.Name == name })
	if i < 0 {
		return target{}, os.ErrNotExist
	}
	if rest == "" {
		rest = "."
	}
	return target{site: &sites[i], rel: rest}, nil
}

func (t target) open() (*os.Root, error) { return os.OpenRoot(t.site.Root) }

// writable resolves p for a change: inside a website, not its directory itself, not read-only.
func (v *vfs) writable(p string) (target, error) {
	t, err := v.resolve(p)
	if err != nil {
		return t, err
	}
	if t.site == nil || t.rel == "." || t.site.ReadOnly {
		return t, os.ErrPermission
	}
	return t, nil
}

func (t target) chown(r *os.Root, rel string) {
	if os.Geteuid() == 0 {
		_ = r.Lchown(rel, t.site.UID, t.site.UID)
	}
}

// Fileread opens a file for download.
func (v *vfs) Fileread(req *sftp.Request) (io.ReaderAt, error) {
	t, err := v.resolve(req.Filepath)
	if err != nil {
		return nil, err
	}
	if t.site == nil {
		return nil, os.ErrPermission
	}
	r, err := t.open()
	if err != nil {
		return nil, err
	}
	defer r.Close() // the open file stays valid
	f, err := r.Open(t.rel)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, errors.Join(err, os.ErrPermission)
	}
	return f, nil
}

// Filewrite opens (creating it if asked) a file for upload.
func (v *vfs) Filewrite(req *sftp.Request) (io.WriterAt, error) {
	t, err := v.writable(req.Filepath)
	if err != nil {
		return nil, err
	}
	r, err := t.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	pf := req.Pflags()
	flags := os.O_WRONLY
	if pf.Read {
		flags = os.O_RDWR
	}
	if pf.Creat {
		flags |= os.O_CREATE
	}
	if pf.Trunc {
		flags |= os.O_TRUNC
	}
	if pf.Excl {
		flags |= os.O_EXCL
	}
	if pf.Append {
		flags |= os.O_APPEND
	}
	_, statErr := r.Lstat(t.rel)
	created := errors.Is(statErr, fs.ErrNotExist)
	f, err := r.OpenFile(t.rel, flags, 0o644)
	if err != nil {
		return nil, err
	}
	if created {
		_ = r.Chmod(t.rel, 0o644) // OpenFile's mode is reduced by the umask
		t.chown(r, t.rel)
	}
	return f, nil
}

// Filecmd runs the commands that change the tree.
func (v *vfs) Filecmd(req *sftp.Request) error {
	switch req.Method {
	case "Setstat":
		return v.setstat(req)
	case "Rename", "PosixRename":
		from, err := v.writable(req.Filepath)
		if err != nil {
			return err
		}
		to, err := v.writable(req.Target)
		if err != nil {
			return err
		}
		if from.site.Name != to.site.Name {
			return sftp.ErrSSHFxOpUnsupported // across websites: copy instead
		}
		r, err := from.open()
		if err != nil {
			return err
		}
		defer r.Close()
		if req.Method == "Rename" {
			if _, err := r.Lstat(to.rel); err == nil {
				return fs.ErrExist
			}
		}
		return r.Rename(from.rel, to.rel)
	case "Rmdir", "Remove":
		t, err := v.writable(req.Filepath)
		if err != nil {
			return err
		}
		r, err := t.open()
		if err != nil {
			return err
		}
		defer r.Close()
		st, err := r.Lstat(t.rel)
		if err != nil {
			return err
		}
		if (req.Method == "Rmdir") != st.IsDir() {
			return os.ErrInvalid
		}
		return r.Remove(t.rel) // a directory must be empty
	case "Mkdir":
		t, err := v.writable(req.Filepath)
		if err != nil {
			return err
		}
		r, err := t.open()
		if err != nil {
			return err
		}
		defer r.Close()
		if err := r.Mkdir(t.rel, 0o755); err != nil {
			return err
		}
		_ = r.Chmod(t.rel, 0o755)
		t.chown(r, t.rel)
		return nil
	}
	// Links could point anywhere: not offered.
	return sftp.ErrSSHFxOpUnsupported
}

func (v *vfs) setstat(req *sftp.Request) error {
	t, err := v.writable(req.Filepath)
	if err != nil {
		if t.site != nil && t.rel == "." && !t.site.ReadOnly {
			return nil // clients touch the directory's times after an upload: ignore
		}
		return err
	}
	r, err := t.open()
	if err != nil {
		return err
	}
	defer r.Close()
	flags, attrs := req.AttrFlags(), req.Attributes()
	if flags.Permissions {
		// Permission bits only: never setuid, setgid or sticky.
		if err := r.Chmod(t.rel, fs.FileMode(attrs.Mode&0o777)); err != nil {
			return err
		}
	}
	if flags.Size {
		f, err := r.OpenFile(t.rel, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		err = f.Truncate(int64(attrs.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	if flags.Acmodtime {
		if err := r.Chtimes(t.rel, time.Unix(int64(attrs.Atime), 0), time.Unix(int64(attrs.Mtime), 0)); err != nil {
			return err
		}
	}
	return nil // ownership never changes
}

// Filelist answers List, Stat, Lstat and Readlink.
func (v *vfs) Filelist(req *sftp.Request) (sftp.ListerAt, error) {
	t, err := v.resolve(req.Filepath)
	if err != nil {
		return nil, err
	}
	if t.site == nil { // the root: one directory per website
		switch req.Method {
		case "List":
			sites, err := v.list()
			if err != nil {
				return nil, err
			}
			out := make([]fs.FileInfo, 0, len(sites))
			for _, s := range sites {
				d := dirInfo{name: s.Name, readOnly: s.ReadOnly}
				if st, err := os.Stat(s.Root); err == nil {
					d.mtime = st.ModTime()
				}
				out = append(out, d)
			}
			return lister(out), nil
		case "Stat", "Lstat":
			return lister{dirInfo{name: "/", readOnly: true}}, nil
		}
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	r, err := t.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	switch req.Method {
	case "List":
		d, err := r.Open(t.rel)
		if err != nil {
			return nil, err
		}
		defer d.Close()
		entries, err := d.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		out := make([]fs.FileInfo, 0, len(entries))
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				out = append(out, fi)
			}
		}
		return lister(out), nil
	case "Stat":
		fi, err := r.Stat(t.rel)
		if err != nil {
			return nil, err
		}
		if t.rel == "." {
			fi = renamed{fi, t.site.Name}
		}
		return lister{fi}, nil
	case "Lstat":
		fi, err := r.Lstat(t.rel)
		if err != nil {
			return nil, err
		}
		if t.rel == "." {
			fi = renamed{fi, t.site.Name}
		}
		return lister{fi}, nil
	case "Readlink":
		dst, err := r.Readlink(t.rel)
		if err != nil {
			return nil, err
		}
		return lister{dirInfo{name: dst}}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

type lister []fs.FileInfo

func (l lister) ListAt(dst []fs.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if n < len(dst) || off+int64(n) >= int64(len(l)) {
		return n, io.EOF
	}
	return n, nil
}

// dirInfo describes the virtual directories (the root and its websites).
type dirInfo struct {
	name     string
	readOnly bool
	mtime    time.Time
}

func (d dirInfo) Name() string { return d.name }
func (d dirInfo) Size() int64  { return 0 }
func (d dirInfo) Mode() fs.FileMode {
	if d.readOnly {
		return fs.ModeDir | 0o555
	}
	return fs.ModeDir | 0o755
}
func (d dirInfo) ModTime() time.Time { return d.mtime }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return nil }

type renamed struct {
	fs.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }
