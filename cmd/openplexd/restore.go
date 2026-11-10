package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openplex.dev/openplex/internal/backup"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

const restoreUsage = `usage: openplexd restore-panel --repo REPO [options]

Rebuilds this server from the backups of another one. Run it on a fresh
install with the service stopped (systemctl stop openplexd), then start the
service: websites, databases and mail are restored in the background.

  --repo REPO        /path/to/repository or s3:https://s3.example.com/bucket/prefix
  --snapshot ID      panel backup to use (default: the newest)
  --ipv4 ADDR        this server's public IPv4 (default: detected)
  --ipv6 ADDR        this server's public IPv6 (default: detected)
  --data-dir DIR     state directory (default /var/lib/openplex)
  --env-file FILE    panel environment file (default /etc/openplex/openplex.env)
  --force            replace a panel that already has websites

The recovery key (Backups → Recovery key on the old panel) is read from
OPENPLEX_RECOVERY_KEY or, if unset, from the first line of standard input.
S3 credentials come from AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and
AWS_DEFAULT_REGION.`

func restorePanel(args []string) error {
	fl := flag.NewFlagSet("restore-panel", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprintln(os.Stderr, restoreUsage) }
	repoSpec := fl.String("repo", "", "")
	snapID := fl.String("snapshot", "", "")
	ip4 := fl.String("ipv4", "", "")
	ip6 := fl.String("ipv6", "", "")
	dataDir := fl.String("data-dir", envOr("OPENPLEX_DATA_DIR", "/var/lib/openplex"), "")
	envFile := fl.String("env-file", "/etc/openplex/openplex.env", "")
	force := fl.Bool("force", false, "")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *repoSpec == "" {
		fl.Usage()
		return errors.New("--repo is required")
	}
	repo, err := backup.ParseRepo(*repoSpec)
	if err != nil {
		return err
	}
	if repo.Password, err = recoveryKey(); err != nil {
		return err
	}
	if repo.Local == "" {
		repo.S3AccessKey, repo.S3Secret, repo.S3Region = os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_DEFAULT_REGION")
		if repo.S3AccessKey == "" || repo.S3Secret == "" {
			return errors.New("set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY for the S3 repository")
		}
	}
	v4, v6, err := hostAddresses(*ip4, *ip6)
	if err != nil {
		return err
	}

	cfg, err := config.Load([]string{"--data-dir", *dataDir})
	if err != nil {
		return err
	}
	if running(cfg) {
		return errors.New("openplexd is running: stop it first (systemctl stop openplexd)")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	store, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	sites, err := store.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		store.Close()
		return err
	}
	if len(sites) > 0 && !*force {
		store.Close()
		return fmt.Errorf("this panel already has %d website(s); add --force to replace it with the backup", len(sites))
	}
	orch, err := docker.New(ctx, cfg, store, log)
	if err != nil {
		store.Close()
		return err
	}
	staging := filepath.Join(cfg.BackupsDir(), "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	fmt.Println("Looking for the panel backup…")
	snap, err := backup.FindPanelSnapshot(ctx, orch, repo, staging, *snapID)
	if err == nil {
		fmt.Printf("Restoring the panel backup %s of %s\n", snap.ShortID, snap.Time.Local().Format("2006-01-02 15:04"))
		err = backup.RestorePanelFiles(ctx, orch, repo, staging, "recover-panel", snap)
	}
	orch.Close()
	store.Close()
	if err != nil {
		return err
	}
	src := filepath.Join(staging, "recover-panel")
	defer os.RemoveAll(src)
	if _, err := os.Stat(filepath.Join(src, "openplex.db")); err != nil {
		return errors.New("the backup has no panel database")
	}

	// Keep what this install had, then put the backup in place.
	aside := filepath.Join(cfg.DataDir, "before-restore-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return err
	}
	places := []struct{ from, to string }{
		{"openplex.db", cfg.DBPath()},
		{"secret.key", filepath.Join(cfg.DataDir, "secret.key")},
		{"secrets", filepath.Join(cfg.DataDir, "secrets")},
		{"caddy-data", filepath.Join(cfg.CaddyDir(), "data")},
		{"webmail-keys.json", filepath.Join(cfg.DataDir, "webmail", "keys.json")},
		{"openplex.env", *envFile},
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Rename(cfg.DBPath()+suffix, filepath.Join(aside, "openplex.db"+suffix))
	}
	for _, p := range places {
		if _, err := os.Stat(filepath.Join(src, p.from)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := os.Stat(p.to); err == nil {
			if err := os.Rename(p.to, filepath.Join(aside, filepath.Base(p.to))); err != nil {
				return fmt.Errorf("keep %s: %w", p.to, err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(p.to), 0o700); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(src, p.from), p.to); err != nil {
			return fmt.Errorf("place %s: %w", p.to, err)
		}
	}

	// Reopen with the restored signing secret and adapt the panel to this server.
	if cfg, err = config.Load([]string{"--data-dir", *dataDir}); err != nil {
		return err
	}
	store, err = db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer store.Close()
	box, err := secrets.New(cfg.Secret, "database-passwords")
	if err != nil {
		return err
	}
	if err := pointBackupsAt(ctx, store, box, cfg, repo); err != nil {
		return err
	}
	fw, err := store.GetFirewall(ctx)
	if err != nil {
		return err
	}
	if fw.Enabled {
		// Rules were written for the old server's SSH and network; turn the
		// firewall back on from the panel once this server is checked.
		if err := store.SaveFirewall(ctx, false, fw.Rules); err != nil {
			return err
		}
		fmt.Println("The firewall was on in the backup: it is off now. Review its rules and turn it on in the panel.")
	}
	if err := moveAddresses(ctx, store, v4, v6); err != nil {
		return err
	}
	if err := backup.WriteRecovery(cfg.DataDir, backup.Recovery{PanelSnapshot: snap.ID, PanelTime: snap.Time}); err != nil {
		return err
	}
	fmt.Printf(`
The panel is restored (this install's previous files are in %s).
Start it now:

  systemctl start openplexd

Websites, databases and mail are then restored in the background; follow it in
Backups → Activity. Sign in with the accounts of the old panel.
If this server's address changed, point your domains (or their nameservers'
glue records) at it; the DNS zones hosted here already use the new address.
`, aside)
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func recoveryKey() (string, error) {
	if k := strings.TrimSpace(os.Getenv("OPENPLEX_RECOVERY_KEY")); k != "" {
		return k, nil
	}
	fmt.Fprint(os.Stderr, "Recovery key: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if k := strings.TrimSpace(line); k != "" {
		return k, nil
	}
	if err != nil {
		return "", fmt.Errorf("read the recovery key: %w", err)
	}
	return "", errors.New("the recovery key is empty")
}

// running reports whether a daemon answers on the panel socket or port.
func running(cfg *config.Config) bool {
	for _, a := range [][2]string{{"unix", cfg.PanelSocket()}, {"tcp", cfg.Listen}} {
		if c, err := net.DialTimeout(a[0], a[1], time.Second); err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// pointBackupsAt makes the restored panel keep backing up to the repository
// it was restored from (its path or credentials may differ on this server).
func pointBackupsAt(ctx context.Context, store *db.DB, box *secrets.Box, cfg *config.Config, r backup.Repo) error {
	s, err := store.GetBackupSettings(ctx)
	if err != nil {
		return err
	}
	if s.RepoPasswordEnc, err = box.Seal(r.Password); err != nil {
		return err
	}
	if r.Local != "" {
		s.Target, s.LocalPath = "local", r.Local
		if r.Local == filepath.Join(cfg.BackupsDir(), "repo") {
			s.LocalPath = ""
		}
	} else {
		s.Target, s.S3Endpoint, s.S3Bucket, s.S3Prefix, s.S3Region, s.S3AccessKey = "s3", r.S3Endpoint, r.S3Bucket, r.S3Prefix, r.S3Region, r.S3AccessKey
		if s.S3SecretEnc, err = box.Seal(r.S3Secret); err != nil {
			return err
		}
	}
	return store.SaveBackupSettings(ctx, s)
}

// moveAddresses points the DNS settings and records at this server.
func moveAddresses(ctx context.Context, store *db.DB, v4, v6 string) error {
	ds, err := store.GetDNSSettings(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, p := range []struct {
		typ string
		old *string
		new string
	}{{"A", &ds.IPv4, v4}, {"AAAA", &ds.IPv6, v6}} {
		if p.new == "" || *p.old == p.new {
			continue
		}
		if *p.old != "" {
			n, err := store.ReplaceDNSAddress(ctx, p.typ, *p.old, p.new)
			if err != nil {
				return err
			}
			fmt.Printf("DNS: %s → %s (%d record(s))\n", *p.old, p.new, n)
		}
		*p.old, changed = p.new, true
	}
	if changed {
		return store.SaveDNSSettings(ctx, ds)
	}
	return nil
}

// hostAddresses returns the given addresses or, when unambiguous, this
// host's own public ones.
func hostAddresses(v4, v6 string) (string, string, error) {
	for _, a := range []*string{&v4, &v6} {
		if *a != "" {
			ip, err := netip.ParseAddr(*a)
			if err != nil {
				return "", "", fmt.Errorf("invalid address %q", *a)
			}
			*a = ip.String()
		}
	}
	addrs, _ := net.InterfaceAddrs()
	var found4, found6 []string
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := pfx.Addr()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if ip.Is4() {
			found4 = append(found4, ip.String())
		} else {
			found6 = append(found6, ip.String())
		}
	}
	if v4 == "" && len(found4) == 1 {
		v4 = found4[0]
	}
	if v6 == "" && len(found6) == 1 {
		v6 = found6[0]
	}
	return v4, v6, nil
}
