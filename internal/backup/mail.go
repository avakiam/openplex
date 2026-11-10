package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/mount"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
)

// Mail data: one snapshot per mail domain, taken from the mail volume (Maildir
// is safe to copy while the server runs). Accounts, aliases and DKIM keys live
// in the panel database and travel with the panel snapshot.
const (
	mMail    = "/data/mail"
	mWebmail = "/data/webmail"
)

// MailTag scopes the mail of a website (by website/domain id).
func MailTag(domainID int64) string { return "mail:" + strconv.FormatInt(domainID, 10) }

var (
	mailDomainRe = regexp.MustCompile(`^[a-z0-9.-]+$`)
	mailLocalRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
)

// mailMounts adds the mail and webmail volumes (read-only) when they exist.
func (m *Manager) mailMounts(ctx context.Context) ([]mount.Mount, error) {
	var ms []mount.Mount
	for name, target := range map[string]string{docker.MailDataVolume: mMail, docker.WebmailDBVolume: mWebmail} {
		p, err := m.orch.VolumePath(ctx, name)
		if err != nil {
			return nil, err
		}
		if p != "" {
			ms = append(ms, mount.Mount{Type: mount.TypeVolume, Source: name, Target: target, ReadOnly: true})
		}
	}
	return ms, nil
}

func (m *Manager) backupMail(ctx context.Context, md *db.MailDomain) (int64, error) {
	if !mailDomainRe.MatchString(md.Name) {
		return 0, fmt.Errorf("invalid mail domain %q", md.Name)
	}
	root, err := m.orch.VolumePath(ctx, docker.MailDataVolume)
	if err != nil {
		return 0, err
	}
	if root == "" {
		return 0, nil // the mail server never ran: nothing stored yet
	}
	if _, err := os.Stat(filepath.Join(root, md.Name)); errors.Is(err, fs.ErrNotExist) {
		return 0, nil // no mail received yet
	}
	s, err := m.Settings(ctx)
	if err != nil {
		return 0, err
	}
	return m.snapshot(ctx, s, path.Join(mMail, md.Name), MailTag(md.DomainID), "mail-name:"+md.Name)
}

// backupWebmail snapshots Roundcube's database (contacts, identities, settings).
func (m *Manager) backupWebmail(ctx context.Context) (int64, error) {
	p, err := m.orch.VolumePath(ctx, docker.WebmailDBVolume)
	if err != nil || p == "" {
		return 0, err
	}
	s, err := m.Settings(ctx)
	if err != nil {
		return 0, err
	}
	return m.snapshot(ctx, s, mWebmail, "webmail")
}

// RestoreMail brings back the mail of a website (or of one mailbox, when
// local is set) from a snapshot. It merges: messages missing now are put back,
// nothing received since is removed and nothing is duplicated.
func (m *Manager) RestoreMail(ctx context.Context, md *db.MailDomain, local, snapID string, userID *int64) (*db.BackupRun, error) {
	if local != "" && !mailLocalRe.MatchString(local) {
		return nil, errors.New("invalid mailbox")
	}
	return m.restore(ctx, MailTag(md.DomainID), snapID, userID, func(ctx context.Context, snap *Snapshot) (string, error) {
		return m.restoreMail(ctx, snap, md, local)
	})
}

func (m *Manager) restoreMail(ctx context.Context, snap *Snapshot, md *db.MailDomain, local string) (string, error) {
	if !mailDomainRe.MatchString(md.Name) {
		return "", fmt.Errorf("invalid mail domain %q", md.Name)
	}
	src := path.Join(mMail, md.Name)
	if !containsPath(snap.Paths, src) {
		return "", fmt.Errorf("snapshot does not contain %s", src)
	}
	root, err := m.orch.VolumePath(ctx, docker.MailDataVolume)
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", errors.New("the mail server has no data volume; turn mail on first")
	}
	sub := src
	target := filepath.Join(root, md.Name)
	if local != "" {
		sub = path.Join(src, local)
		target = filepath.Join(target, local)
	}
	stage := fmt.Sprintf("restore-mail-%d", md.DomainID)
	host := m.stagingDir(stage)
	_ = os.RemoveAll(host)
	defer os.RemoveAll(host)
	s, err := m.Settings(ctx)
	if err != nil {
		return "", err
	}
	if _, err := m.restic(ctx, s, false, "restore", snap.ID+":"+sub, "--target", path.Join(mStaging, stage)); err != nil {
		return "", err
	}
	if _, err := os.Stat(host); errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("this mailbox is not in the backup")
	}
	n, err := MergeMaildirs(host, target)
	if err != nil {
		return "", err
	}
	m.resyncMail(ctx, md.Name, local, target)
	return fmt.Sprintf("restored %d message(s) from snapshot %s", n, snap.ShortID), nil
}

// resyncMail makes Dovecot notice files put back behind its back.
func (m *Manager) resyncMail(ctx context.Context, domain, local, target string) {
	var boxes []string
	if local != "" {
		boxes = []string{local}
	} else if ents, err := os.ReadDir(target); err == nil {
		for _, e := range ents {
			if e.IsDir() && mailLocalRe.MatchString(e.Name()) {
				boxes = append(boxes, e.Name())
			}
		}
	}
	for _, b := range boxes {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := m.orch.MailExec(cctx, "doveadm", "force-resync", "-u", b+"@"+domain, "*"); err != nil {
			m.log.Warn("mail restore: resync", "mailbox", b+"@"+domain, "err", err)
		}
		cancel()
	}
}

func containsPath(paths []string, p string) bool {
	for _, x := range paths {
		if x == p {
			return true
		}
	}
	return false
}

// MergeMaildirs copies into dst what src (a restored tree) has and dst lacks:
// messages (Maildir cur/new files) whose unique name, before the ":2,flags"
// part, is not in that folder any more, and other files (sieve scripts,
// subscriptions) that don't exist. Dovecot's own index and uid files are
// skipped (rebuilt by a resync). Owner, mode and times are kept. It returns
// the number of messages put back.
func MergeMaildirs(src, dst string) (int, error) {
	present := map[string]map[string]bool{} // maildir folder (dst) -> unique names
	uniques := func(folder string) map[string]bool {
		if u, ok := present[folder]; ok {
			return u
		}
		u := map[string]bool{}
		for _, sub := range []string{"cur", "new"} {
			ents, _ := os.ReadDir(filepath.Join(folder, sub))
			for _, e := range ents {
				u[uniqueName(e.Name())] = true
			}
		}
		present[folder] = u
		return u
	}
	restored := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return mkdirLike(out, info) // tmp/ too (the Maildir shape); its in-flight files are skipped
		case !info.Mode().IsRegular():
			return nil // no symlinks or devices from a backup
		case strings.HasPrefix(d.Name(), "dovecot"):
			return nil
		}
		parent := filepath.Base(filepath.Dir(p))
		if parent == "tmp" {
			return nil
		}
		if parent == "cur" || parent == "new" {
			folder := filepath.Dir(filepath.Dir(out))
			u := uniques(folder)
			name := uniqueName(d.Name())
			if u[name] {
				return nil
			}
			if err := copyLike(p, out, info); err != nil {
				return err
			}
			u[name] = true
			restored++
			return nil
		}
		if _, err := os.Lstat(out); err == nil {
			return nil
		}
		return copyLike(p, out, info)
	})
	return restored, err
}

// uniqueName is a Maildir file name without its ":2,flags" (or ",S=…") info.
func uniqueName(n string) string {
	if i := strings.IndexByte(n, ':'); i >= 0 {
		n = n[:i]
	}
	return n
}

func owner(info fs.FileInfo) (int, int, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid), true
	}
	return 0, 0, false
}

func mkdirLike(p string, info fs.FileInfo) error {
	if _, err := os.Lstat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(p, info.Mode().Perm()); err != nil {
		return err
	}
	if uid, gid, ok := owner(info); ok {
		return os.Lchown(p, uid, gid)
	}
	return nil
}

func copyLike(src, dst string, info fs.FileInfo) error {
	if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
		return err
	}
	if uid, gid, ok := owner(info); ok {
		if err := os.Lchown(dst, uid, gid); err != nil {
			return err
		}
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime()) // Dovecot reads the received date from it
}
