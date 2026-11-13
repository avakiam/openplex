package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/backup"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
)

const prepareUsage = `usage: openplexd prepare-image [options]

Prepares a server to become a template (a provider's image): downloads the
container images OpenPlex uses and builds its own, without initialising
OpenPlex. Servers created from the template generate their own secret key,
admin account and certificates on their first start. The installer runs it
with --image.

  --php LIST         PHP versions to build (default: the default version, %s)
  --no-mail          leave out the mail server and webmail images (~1 GB)
  --data-dir DIR     state directory (default /var/lib/openplex)
  --docker-host URL  Docker Engine endpoint (default: environment / socket)
`

// initialised reports whether OpenPlex already ran with this data dir.
func initialised(dir string) bool {
	for _, f := range []string{"secret.key", "openplex.db"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func prepareImage(args []string) error {
	def := docker.Runtimes["php"].Default
	fs := flag.NewFlagSet("prepare-image", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintf(fs.Output(), prepareUsage, def) }
	php := fs.String("php", def, "")
	noMail := fs.Bool("no-mail", false, "")
	dataDir := fs.String("data-dir", envOr("OPENPLEX_DATA_DIR", "/var/lib/openplex"), "")
	dockerHost := fs.String("docker-host", os.Getenv("OPENPLEX_DOCKER_HOST"), "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if initialised(*dataDir) {
		return fmt.Errorf("OpenPlex already ran on this server (%s holds its secret key or database): "+
			"a template made from it would give every new server the same secrets. Prepare the template on a server "+
			"where OpenPlex was installed with --image and never started", *dataDir)
	}
	var versions []string
	for _, v := range strings.Split(*php, ",") {
		if v = strings.TrimSpace(v); v != "" {
			versions = append(versions, v)
		}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()
	orch, err := docker.New(ctx, &config.Config{DockerHost: *dockerHost}, nil, log)
	if err != nil {
		return err
	}
	defer orch.Close()
	start := time.Now()
	if err := orch.PrepareImages(ctx, versions, !*noMail, backup.ResticImage); err != nil {
		return err
	}
	fmt.Printf("Images ready in %s. OpenPlex initialises itself on this server's next start.\n", time.Since(start).Round(time.Second))
	return nil
}

const linkUsage = `usage: openplexd login-link [options]

Prints a one-time sign-in link for a panel account, for example to hand a
new server over to its customer. The link signs in once, within its validity;
it does not work for accounts with two-factor authentication. On a server's
first boot it waits for the panel to create its admin account.

  --user NAME        account to sign in as (default: admin)
  --ttl DURATION     validity of the link (default 24h, at most 168h)
  --data-dir DIR     state directory (default /var/lib/openplex)
`

func loginLink(args []string) error {
	fs := flag.NewFlagSet("login-link", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), linkUsage) }
	user := fs.String("user", "admin", "")
	ttl := fs.Duration("ttl", 24*time.Hour, "")
	dataDir := fs.String("data-dir", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ttl <= 0 || *ttl > 7*24*time.Hour {
		return errors.New("--ttl must be between 1s and 168h")
	}
	var cargs []string
	if *dataDir != "" {
		cargs = []string{"--data-dir", *dataDir}
	}
	cfg, err := config.Load(cargs)
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer store.Close()
	// On a server's first boot (a script run by cloud-init) the panel may not
	// have created its admin account yet: wait for it.
	for deadline := time.Now().Add(2 * time.Minute); ; {
		n, err := store.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("the panel has no accounts yet: is openplexd running? (systemctl status openplexd)")
		}
		time.Sleep(time.Second)
	}
	u, err := store.GetUserByUsername(ctx, *user)
	if err != nil {
		return fmt.Errorf("user %q: %w", *user, err)
	}
	switch {
	case u.Disabled:
		return fmt.Errorf("user %q is disabled", u.Username)
	case u.TOTPEnabled:
		return fmt.Errorf("user %q uses two-factor authentication: links don't sign such accounts in (openplexd reset-password turns it off)", u.Username)
	}
	token := auth.NewRefreshToken()
	if err := store.CreateLoginLink(ctx, u.ID, auth.HashToken(token), time.Now().Add(*ttl), db.LinkHandover); err != nil {
		return err
	}
	_ = store.Audit(ctx, db.AuditEntry{Action: "auth.login_link_created", Target: u.Username, Detail: "valid " + ttl.String() + " (openplexd login-link)"})
	path := "/login/?link=" + token
	if cfg.PanelDomain != "" {
		fmt.Println("https://" + cfg.PanelDomain + path)
	} else {
		fmt.Printf("http://<panel address>%s\n(the panel has no domain: open it through its direct address, e.g. an SSH tunnel to %s)\n", path, cfg.Listen)
	}
	fmt.Fprintf(os.Stderr, "Signs in as %s once, until %s.\n", u.Username, time.Now().Add(*ttl).Format(time.RFC1123))
	return nil
}
