package backup

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"openplex.dev/openplex/internal/db"
)

// Where the backup repository is, compared with the data it protects.
const (
	OffsiteRemote     = "remote"      // another machine (S3 elsewhere, a network filesystem)
	OffsiteOtherDisk  = "other_disk"  // another disk of this server
	OffsiteSameDisk   = "same_disk"   // the disk that holds the data
	OffsiteSameServer = "same_server" // S3 served by this server
	OffsiteUnknown    = "unknown"
)

// Offsite says how safe the backup location is from losing the data.
type Offsite struct {
	Level  string `json:"level"`
	Detail string `json:"detail"` // file system type, disks or host, for humans
}

// remoteFS are file systems whose data lives on another machine.
var remoteFS = []string{"nfs", "nfs4", "cifs", "smb3", "smbfs", "9p", "ceph", "glusterfs", "fuse.sshfs", "fuse.rclone", "fuse.s3fs", "fuse.glusterfs", "davfs", "fuse.davfs2"}

type mountEntry struct {
	majMin, point, fstype, source string
}

// readMounts parses /proc/self/mountinfo.
func readMounts(root string) ([]mountEntry, error) {
	f, err := os.Open(filepath.Join(root, "proc/self/mountinfo"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []mountEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		pre, post, ok := strings.Cut(sc.Text(), " - ")
		a, b := strings.Fields(pre), strings.Fields(post)
		if !ok || len(a) < 5 || len(b) < 2 {
			continue
		}
		out = append(out, mountEntry{majMin: a[2], point: unescapeMount(a[4]), fstype: b[0], source: unescapeMount(b[1])})
	}
	return out, sc.Err()
}

// unescapeMount undoes mountinfo's octal escapes (\040 for a space…).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountOf returns the mount holding path (the deepest mount point above it).
// path need not exist yet: its nearest existing parent decides.
func mountOf(root string, mounts []mountEntry, path string) (mountEntry, bool) {
	p := filepath.Clean(path)
	for {
		if real, err := filepath.EvalSymlinks(filepath.Join(root, p)); err == nil {
			rel, err := filepath.Rel(filepath.Clean(root), real)
			if err == nil && !strings.HasPrefix(rel, "..") {
				p = "/" + filepath.ToSlash(rel)
				if rel == "." {
					p = "/"
				}
			}
			break
		}
		if p == "/" || p == "." {
			break
		}
		p = filepath.Dir(p)
	}
	var best mountEntry
	found := false
	for _, m := range mounts { // later entries shadow earlier ones on the same point
		if m.point == p || m.point == "/" || strings.HasPrefix(p, strings.TrimSuffix(m.point, "/")+"/") {
			if !found || len(m.point) >= len(best.point) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// physicalDisks follows a block device (partition, LVM, RAID, dm-crypt) down
// to the disks under it.
func physicalDisks(root, name string, depth int) []string {
	real, err := filepath.EvalSymlinks(filepath.Join(root, "sys/class/block", name))
	if err != nil || depth > 8 {
		return []string{name}
	}
	if _, err := os.Stat(filepath.Join(real, "partition")); err == nil {
		return []string{filepath.Base(filepath.Dir(real))}
	}
	slaves, _ := os.ReadDir(filepath.Join(real, "slaves"))
	if len(slaves) == 0 {
		return []string{filepath.Base(real)}
	}
	var out []string
	for _, s := range slaves {
		for _, d := range physicalDisks(root, s.Name(), depth+1) {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	slices.Sort(out)
	return out
}

// storage identifies what holds a path: remote, or a set of disks.
type storage struct {
	remote bool
	fstype string
	disks  []string // physical disks, or a pool ("zfs:tank")
	source string
}

func storageOf(root string, mounts []mountEntry, path string) (storage, bool) {
	m, ok := mountOf(root, mounts, path)
	if !ok {
		return storage{}, false
	}
	st := storage{fstype: m.fstype, source: m.source}
	switch {
	case slices.Contains(remoteFS, m.fstype):
		st.remote = true
	case m.fstype == "zfs":
		pool, _, _ := strings.Cut(m.source, "/")
		st.disks = []string{"zfs:" + pool}
	case strings.HasPrefix(m.majMin, "0:"):
		// Btrfs and other anonymous devices: the mount source names the device.
		if !strings.HasPrefix(m.source, "/dev/") {
			return st, false // tmpfs, overlay…: nothing to compare
		}
		real, err := filepath.EvalSymlinks(filepath.Join(root, m.source))
		if err != nil {
			return st, false
		}
		st.disks = physicalDisks(root, filepath.Base(real), 0)
	default:
		real, err := filepath.EvalSymlinks(filepath.Join(root, "sys/dev/block", m.majMin))
		if err != nil {
			return st, false
		}
		st.disks = physicalDisks(root, filepath.Base(real), 0)
	}
	return st, true
}

// LocalOffsite compares a local repository with the data directory.
func LocalOffsite(root, repo, data string) Offsite {
	mounts, err := readMounts(root)
	if err != nil {
		return Offsite{Level: OffsiteUnknown}
	}
	r, ok := storageOf(root, mounts, repo)
	if !ok {
		return Offsite{Level: OffsiteUnknown}
	}
	if r.remote {
		return Offsite{Level: OffsiteRemote, Detail: r.fstype + " " + r.source}
	}
	d, ok := storageOf(root, mounts, data)
	if !ok || d.remote {
		return Offsite{Level: OffsiteUnknown}
	}
	for _, x := range r.disks {
		if slices.Contains(d.disks, x) {
			return Offsite{Level: OffsiteSameDisk, Detail: strings.Join(r.disks, ", ")}
		}
	}
	return Offsite{Level: OffsiteOtherDisk, Detail: strings.Join(r.disks, ", ")}
}

// S3Offsite tells whether an S3 endpoint is served by this server.
func S3Offsite(ctx context.Context, endpoint string, local []netip.Addr) Offsite {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return Offsite{Level: OffsiteUnknown}
	}
	host := u.Hostname()
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return Offsite{Level: OffsiteUnknown, Detail: host}
		}
		addrs = ips
	}
	for _, a := range addrs {
		a = a.Unmap()
		if a.IsLoopback() || slices.Contains(local, a) {
			return Offsite{Level: OffsiteSameServer, Detail: host}
		}
	}
	return Offsite{Level: OffsiteRemote, Detail: host}
}

// hostAddrs lists the addresses of this server's interfaces.
func hostAddrs() []netip.Addr {
	var out []netip.Addr
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				out = append(out, ip.Unmap())
			}
		}
	}
	return out
}

// Offsite checks the configured repository.
func (m *Manager) Offsite(ctx context.Context, s *db.BackupSettings) Offsite {
	if s.Target == "s3" {
		return S3Offsite(ctx, s.S3Endpoint, hostAddrs())
	}
	return LocalOffsite("/", m.LocalRepoPath(s), m.cfg.DataDir)
}
