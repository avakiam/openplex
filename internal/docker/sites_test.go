package docker

import (
	"os"
	"path/filepath"
	"testing"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
)

func TestPrepareSiteDirSymlink(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	d := &db.Domain{ID: 1, Name: "a.test", DocumentRoot: "public"}
	if err := PrepareSiteDir(cfg, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesDir(), "a.test", "public", "index.html")); err != nil {
		t.Fatal(err)
	}

	// The tenant swaps its document root for a symlink to a host directory.
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(cfg.SitesDir(), "a.test")
	if err := os.Rename(filepath.Join(site, "public"), filepath.Join(site, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(site, "public")); err != nil {
		t.Fatal(err)
	}
	d.DocumentRoot = "public/.ssh"
	if err := PrepareSiteDir(cfg, d); err == nil {
		t.Fatal("document root through a symlink out of the site was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("created a directory outside the site: %v", err)
	}
	if fi, _ := os.Stat(outside); fi.Mode().Perm() != 0o700 {
		t.Fatalf("outside dir mode changed to %v", fi.Mode().Perm())
	}
}
