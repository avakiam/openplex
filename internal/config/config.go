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

	Secret []byte // HMAC key for JWTs, loaded from DataDir/secret.key
}

// Paths derived from DataDir.
func (c *Config) DBPath() string       { return filepath.Join(c.DataDir, "openplex.db") }
func (c *Config) SitesDir() string     { return filepath.Join(c.DataDir, "sites") }
func (c *Config) CaddyDir() string     { return filepath.Join(c.DataDir, "caddy") }
func (c *Config) BackupsDir() string   { return filepath.Join(c.DataDir, "backups") }
func (c *Config) LogsDir() string      { return filepath.Join(c.DataDir, "logs") }
func (c *Config) RunDir() string       { return filepath.Join(c.DataDir, "run") }
func (c *Config) PanelSocket() string  { return filepath.Join(c.RunDir(), "openplexd.sock") }
func (c *Config) secretPath() string   { return filepath.Join(c.DataDir, "secret.key") }
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
	fsFlags.DurationVar(&c.AccessTTL, "access-ttl", 15*time.Minute, "access token lifetime")
	fsFlags.DurationVar(&c.RefreshTTL, "refresh-ttl", 30*24*time.Hour, "refresh token lifetime")
	if err := fsFlags.Parse(args); err != nil {
		return nil, err
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
	if err := os.WriteFile(path, b, 0o600); err != nil {
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
