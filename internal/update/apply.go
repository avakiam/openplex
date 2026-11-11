package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ApplyOptions tune Apply (tests replace systemctl and shorten the wait).
type ApplyOptions struct {
	Systemctl func(args ...string) error
	Wait      time.Duration // how long the new version has to confirm
	Poll      time.Duration
	Log       func(format string, args ...any)
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apply installs the pending update: stop the daemon, back up its binary and
// database, put the new binary in place, start it and wait for it to
// confirm; otherwise restore the backup. It runs as root outside the
// daemon's sandbox ("openplexd update apply", started by Install).
func Apply(dataDir string, o ApplyOptions) error {
	if o.Systemctl == nil {
		o.Systemctl = systemctl
	}
	if o.Wait == 0 {
		o.Wait = 3 * time.Minute
	}
	if o.Poll == 0 {
		o.Poll = 2 * time.Second
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	dir := filepath.Join(dataDir, "updates")
	pendingPath := filepath.Join(dir, "pending.json")
	p, err := readPending(pendingPath)
	if err != nil {
		return fmt.Errorf("no pending update: %w", err)
	}
	finish := func(result, msg string) {
		_ = writeJSON(filepath.Join(dir, "last.json"), Outcome{From: p.From, To: p.To, Result: result, Message: msg, Time: time.Now().UTC()})
		_ = os.Remove(pendingPath)
		_ = os.Remove(p.Binary)
	}
	backup := filepath.Join(dir, "backup-"+p.From)
	dbFiles := []string{p.DB, p.DB + "-wal", p.DB + "-shm"}

	o.Log("stopping %s", p.Unit)
	if err := o.Systemctl("stop", p.Unit); err != nil {
		finish("failed", err.Error())
		_ = o.Systemctl("start", p.Unit)
		return err
	}
	// From here on the daemon is down: every failure starts it again.
	if err := backupFiles(backup, p.Exe, dbFiles); err != nil {
		finish("failed", "backup: "+err.Error())
		_ = o.Systemctl("start", p.Unit)
		return err
	}
	o.Log("installing %s over %s", p.To, p.Exe)
	if err := copyFile(p.Binary, p.Exe, 0o755); err != nil {
		finish("failed", "install: "+err.Error())
		_ = o.Systemctl("start", p.Unit)
		return err
	}
	confirmed := false
	if err := o.Systemctl("start", p.Unit); err == nil {
		for deadline := time.Now().Add(o.Wait); time.Now().Before(deadline); time.Sleep(o.Poll) {
			if cur, err := readPending(pendingPath); err == nil && cur.Confirmed {
				confirmed = true
				break
			}
		}
	}
	if confirmed {
		o.Log("%s confirmed", p.To)
		finish("updated", "")
		pruneBackups(dir, 2)
		return nil
	}
	o.Log("%s did not confirm in %s: restoring %s", p.To, o.Wait, p.From)
	_ = o.Systemctl("stop", p.Unit)
	err = restoreFiles(backup, p.Exe, dbFiles)
	msg := fmt.Sprintf("version %s did not start correctly within %s; version %s was restored", p.To, o.Wait.Round(time.Second), p.From)
	if err != nil {
		msg += " (restore error: " + err.Error() + ")"
	}
	finish("rolled_back", msg)
	if serr := o.Systemctl("start", p.Unit); err == nil {
		err = serr
	}
	return err
}

// backupFiles copies the binary and the database files into dir.
func backupFiles(dir, exe string, db []string) error {
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := copyFile(exe, filepath.Join(dir, "openplexd"), 0o755); err != nil {
		return err
	}
	for _, f := range db {
		st, err := os.Stat(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := copyFile(f, filepath.Join(dir, filepath.Base(f)), st.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// restoreFiles puts the backup in place again; database files that weren't
// backed up (no -wal at the time) are removed.
func restoreFiles(dir, exe string, db []string) error {
	var errs []error
	errs = append(errs, copyFile(filepath.Join(dir, "openplexd"), exe, 0o755))
	for _, f := range db {
		src := filepath.Join(dir, filepath.Base(f))
		st, err := os.Stat(src)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, copyFile(src, f, st.Mode().Perm()))
	}
	return errors.Join(errs...)
}

// pruneBackups keeps the newest keep backup-* directories.
func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type b struct {
		path string
		mod  time.Time
	}
	var bs []b
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "backup-") {
			if info, err := e.Info(); err == nil {
				bs = append(bs, b{filepath.Join(dir, e.Name()), info.ModTime()})
			}
		}
	}
	slices.SortFunc(bs, func(x, y b) int { return y.mod.Compare(x.mod) })
	for i, x := range bs {
		if i >= keep {
			_ = os.RemoveAll(x.path)
		}
	}
}
