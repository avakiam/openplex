// Package config holds the runtime configuration of openplexd.
package config

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Config is the fully-resolved daemon configuration.
type Config struct {
	DataDir      string
	Listen       string
	PublicURL    string // e.g. https://panel.example.com — used for cookies/links
	PanelDomain  string // hostname the edge proxy routes to the panel; empty = none
	ACMEEmail    string
	DockerHost   string // empty = DOCKER_HOST / default socket
	Dev          bool
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	EnableDocker bool
	Firewall     bool // manage the host firewall (nftables)
	Captcha      bool // ask the configured captcha on the login page (false: recovery switch)
	// TenantNetworks is the address pool carved into one /24 Docker network
	// per account, so the websites of different accounts cannot reach each other.
	TenantNetworks string
	// MarketplaceURL is where the Extensions page finds official extensions.
	MarketplaceURL string
	// UpdateURL answers with the latest stable release (GitHub's API format).
	UpdateURL string
	// SFTPListen is where the built-in SFTP server listens ("" = off).
	SFTPListen string
	// DBRemoteMariaDB / DBRemotePostgres are where remote database clients
	// connect, while some database has remote access ("" = never).
	DBRemoteMariaDB  string
	DBRemotePostgres string

	Secret []byte // HMAC key for JWTs, loaded from DataDir/secret.key
}

// Paths derived from DataDir.
func (c *Config) DBPath() string        { return filepath.Join(c.DataDir, "openplex.db") }
func (c *Config) SitesDir() string      { return filepath.Join(c.DataDir, "sites") }
func (c *Config) CaddyDir() string      { return filepath.Join(c.DataDir, "caddy") }
func (c *Config) BackupsDir() string    { return filepath.Join(c.DataDir, "backups") }
func (c *Config) LogsDir() string       { return filepath.Join(c.DataDir, "logs") }
func (c *Config) RunDir() string        { return filepath.Join(c.DataDir, "run") }
func (c *Config) PanelSocket() string   { return filepath.Join(c.RunDir(), "openplexd.sock") }
func (c *Config) secretPath() string    { return filepath.Join(c.DataDir, "secret.key") }
func (c *Config) AdminPassPath() string { return filepath.Join(c.DataDir, "initial-admin-password") }

// Load parses flags (falling back to OPENPLEX_* environment variables) and
// prepares the data directory.
func Load(args []string) (*Config, error) {
	fsFlags := flag.NewFlagSet("openplexd", flag.ContinueOnError)
	c := &Config{}
	fsFlags.StringVar(&c.DataDir, "data-dir", env("OPENPLEX_DATA_DIR", "/var/lib/openplex"), "state directory")
	fsFlags.StringVar(&c.Listen, "listen", env("OPENPLEX_LISTEN", "127.0.0.1:7080"), "HTTP listen address for the panel API/UI")
	fsFlags.StringVar(&c.PublicURL, "public-url", env("OPENPLEX_PUBLIC_URL", ""), "public URL of the panel")
	fsFlags.StringVar(&c.PanelDomain, "panel-domain", env("OPENPLEX_PANEL_DOMAIN", ""), "hostname the edge proxy serves the panel on")
	fsFlags.StringVar(&c.ACMEEmail, "acme-email", env("OPENPLEX_ACME_EMAIL", ""), "contact e-mail for Let's Encrypt / ZeroSSL")
	fsFlags.StringVar(&c.DockerHost, "docker-host", env("OPENPLEX_DOCKER_HOST", ""), "Docker Engine endpoint (default: environment / unix socket)")
	fsFlags.BoolVar(&c.Dev, "dev", envBool("OPENPLEX_DEV", false), "development mode (text logs, permissive CORS)")
	fsFlags.BoolVar(&c.EnableDocker, "docker", envBool("OPENPLEX_DOCKER", true), "enable the Docker orchestrator")
	fsFlags.BoolVar(&c.Firewall, "firewall", envBool("OPENPLEX_FIREWALL", true), "manage the host firewall (nftables table inet openplex)")
	fsFlags.BoolVar(&c.Captcha, "captcha", envBool("OPENPLEX_CAPTCHA", true), "ask the configured captcha on the panel login (false turns it off, e.g. after a wrong key)")
	fsFlags.StringVar(&c.TenantNetworks, "tenant-networks", env("OPENPLEX_TENANT_NETWORKS", "10.213.0.0/16"), "address pool for the per-account website networks (one /24 each)")
	fsFlags.StringVar(&c.SFTPListen, "sftp-listen", env("OPENPLEX_SFTP_LISTEN", ":2222"), "address of the built-in SFTP server for website files (empty: off)")
	fsFlags.StringVar(&c.DBRemoteMariaDB, "db-remote-mariadb", env("OPENPLEX_DB_REMOTE_MARIADB", ":3306"), "address for remote MariaDB clients, open only while some database has remote access (empty: never)")
	fsFlags.StringVar(&c.DBRemotePostgres, "db-remote-postgres", env("OPENPLEX_DB_REMOTE_POSTGRES", ":5432"), "address for remote PostgreSQL clients, open only while some database has remote access (empty: never)")
	fsFlags.StringVar(&c.UpdateURL, "update-url", env("OPENPLEX_UPDATE_URL", "https://api.github.com/repos/avakiam/openplex/releases/latest"), "latest stable release of OpenPlex")
	fsFlags.StringVar(&c.MarketplaceURL, "marketplace-url", env("OPENPLEX_MARKETPLACE_URL", "https://marketplace.openplex.dev"), "extension marketplace (empty: none)")
	fsFlags.DurationVar(&c.AccessTTL, "access-ttl", 15*time.Minute, "access token lifetime")
	fsFlags.DurationVar(&c.RefreshTTL, "refresh-ttl", 30*24*time.Hour, "refresh token lifetime")
	if err := fsFlags.Parse(args); err != nil {
		return nil, err
	}

	if strings.EqualFold(c.PanelDomain, "auto") {
		c.PanelDomain = autoHostname()
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}
	c.DataDir = abs
	for _, d := range []string{c.DataDir, c.SitesDir(), c.CaddyDir(), c.BackupsDir(), c.LogsDir(), c.RunDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	if err := os.Chmod(c.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod data dir: %w", err)
	}
	if c.Secret, err = loadOrCreateSecret(c.secretPath()); err != nil {
		return nil, err
	}
	return c, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) < 32 {
			return nil, fmt.Errorf("%s is too short (%d bytes); delete it to regenerate", path, len(b))
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	b = make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	// Another process starting at the same time (the daemon and a command such
	// as login-link on first boot) may be creating it too: the file appears
	// whole and only once (link fails when it exists), and the loser uses the
	// winner's secret.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, fs.ErrExist) {
		return loadOrCreateSecret(path)
	} else if err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	return b, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// autoHostname is the server's own fully qualified host name, for
// OPENPLEX_PANEL_DOMAIN=auto (a server made from a provider's template gets
// its name on first boot); "" when the host name is not a domain name.
func autoHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	if !strings.Contains(h, ".") {
		// A short name: its full name is in /etc/hosts (cloud-init writes it
		// there), as `hostname -f` finds it.
		if b, err := os.ReadFile("/etc/hosts"); err == nil {
			h = fqdnFromHosts(string(b), h)
		}
	}
	if !validAutoHost(h) {
		return ""
	}
	return h
}

// fqdnFromHosts finds short's full name in a hosts file: the first name on
// a line that lists short and starts with "short.".
func fqdnFromHosts(hosts, short string) string {
	for _, line := range strings.Split(hosts, "\n") {
		line, _, _ = strings.Cut(line, "#")
		f := strings.Fields(strings.ToLower(line))
		if len(f) < 2 || !slices.Contains(f[1:], short) {
			continue
		}
		for _, n := range f[1:] {
			if strings.HasPrefix(n, short+".") {
				return strings.TrimSuffix(n, ".")
			}
		}
	}
	return short
}

// validAutoHost reports whether h can be the panel's domain.
func validAutoHost(h string) bool {
	if !strings.Contains(h, ".") || strings.HasSuffix(h, ".localdomain") || strings.HasSuffix(h, ".local") {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || len(l) > 63 || strings.Trim(l, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
	}
	return true
}
