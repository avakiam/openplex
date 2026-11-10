package backup

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestMergeMaildirs(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	// Backup: three messages in INBOX, one in a folder, a sieve script, Dovecot files.
	write(t, src, "info/cur/1000.M1.host:2,S", "kept, flags changed since")
	write(t, src, "info/cur/1001.M2.host:2,S", "deleted since")
	write(t, src, "info/new/1002.M3.host", "moved to cur since")
	write(t, src, "info/.Archive/cur/1003.M4.host:2,", "deleted folder")
	write(t, src, "info/tmp/1004.M5.host", "in flight")
	write(t, src, "info/dovecot-uidlist", "old uids")
	write(t, src, "info/dovecot.index", "old index")
	write(t, src, "info/home/.dovecot.sieve", "old rules")
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(src, "info/cur/1001.M2.host:2,S"), old, old)
	// Now: one message re-flagged, one moved new→cur, one new message, new rules.
	write(t, dst, "info/cur/1000.M1.host:2,RS", "kept")
	write(t, dst, "info/cur/1002.M3.host:2,", "moved")
	write(t, dst, "info/cur/2000.M9.host:2,", "arrived after the backup")
	write(t, dst, "info/dovecot-uidlist", "current uids")
	write(t, dst, "info/home/.dovecot.sieve", "current rules")

	n, err := MergeMaildirs(filepath.Join(src, "info"), filepath.Join(dst, "info"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("restored %d messages, want 2", n)
	}
	want := []string{
		"info/.Archive/cur/1003.M4.host:2,",
		"info/cur/1000.M1.host:2,RS",
		"info/cur/1001.M2.host:2,S",
		"info/cur/1002.M3.host:2,",
		"info/cur/2000.M9.host:2,",
		"info/dovecot-uidlist",
		"info/home/.dovecot.sieve",
	}
	if got := list(t, dst); len(got) != len(want) {
		t.Fatalf("files = %v\nwant    %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("files = %v\nwant    %v", got, want)
				break
			}
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "info/home/.dovecot.sieve")); string(b) != "current rules" {
		t.Error("existing file overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "info/dovecot-uidlist")); string(b) != "current uids" {
		t.Error("Dovecot file overwritten")
	}
	if st, err := os.Stat(filepath.Join(dst, "info/cur/1001.M2.host:2,S")); err != nil || !st.ModTime().Equal(old) {
		t.Errorf("received date not kept: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dst, "info/tmp")); err != nil || !st.IsDir() {
		t.Error("tmp/ folder missing")
	}
	if n, _ := MergeMaildirs(filepath.Join(src, "info"), filepath.Join(dst, "info")); n != 0 {
		t.Errorf("second merge restored %d (duplicates)", n)
	}
}
