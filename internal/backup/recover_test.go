package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRepo(t *testing.T) {
	r, err := ParseRepo("s3:https://s3.example.com/my-bucket/openplex/vps1")
	if err != nil || r.S3Endpoint != "https://s3.example.com" || r.S3Bucket != "my-bucket" || r.S3Prefix != "openplex/vps1" {
		t.Errorf("s3 = %+v %v", r, err)
	}
	if r, err := ParseRepo("s3:https://s3.example.com/b-1"); err != nil || r.S3Prefix != "" {
		t.Errorf("s3 without prefix = %+v %v", r, err)
	}
	for _, bad := range []string{"", "relative/dir", "/", "s3:ftp://x/b", "s3:https://x", "s3:https://x/b/../c"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) accepted", bad)
		}
	}
	dir := t.TempDir()
	if _, err := ParseRepo(dir); err == nil {
		t.Error("directory without a repository accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "config"), []byte("x"), 0o600)
	if r, err := ParseRepo(dir); err != nil || r.Local != dir {
		t.Errorf("local = %+v %v", r, err)
	}
	r = Repo{S3Endpoint: "https://s3.example.com/", S3Bucket: "b", S3Prefix: "p", S3AccessKey: "k", S3Secret: "s", Password: "pw"}
	env := r.env()
	if env[1] != "RESTIC_REPOSITORY=s3:https://s3.example.com/b/p" {
		t.Errorf("env = %v", env)
	}
}

func TestPick(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 10, h, 0, 0, 0, time.UTC) }
	snaps := []Snapshot{{ID: "a", Time: at(1)}, {ID: "b", Time: at(3)}, {ID: "c", Time: at(5)}}
	if s := pick(snaps, at(4)); s.ID != "b" {
		t.Errorf("same run = %s, want b", s.ID)
	}
	if s := pick(snaps, at(0)); s.ID != "c" {
		t.Errorf("nothing before the panel = %s, want the newest", s.ID)
	}
	if pick(nil, at(4)) != nil {
		t.Error("no snapshots should give nil")
	}
}
