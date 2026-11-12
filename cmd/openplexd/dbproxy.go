package main

import (
	"context"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dbproxy"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/firewall"
)

// newDBProxy sets up remote access to the shared database servers; the host
// firewall opens each engine's port to the addresses allowed for it.
func newDBProxy(ctx context.Context, cfg *config.Config, store *db.DB, orch *docker.Orchestrator, fw *firewall.Manager, log *slog.Logger) (*dbproxy.Proxy, error) {
	cert, err := dbproxy.LoadCert(filepath.Join(cfg.DataDir, "db-proxy.pem"), dbproxy.HostNames(cfg.PanelDomain))
	if err != nil {
		return nil, err
	}
	listen := map[string]string{}
	if cfg.DBRemoteMariaDB != "" {
		listen[dbproxy.MariaDB] = cfg.DBRemoteMariaDB
	}
	if cfg.DBRemotePostgres != "" {
		listen[dbproxy.Postgres] = cfg.DBRemotePostgres
	}
	return dbproxy.New(ctx, dbproxy.Options{
		Listen: listen,
		Cert:   cert,
		Log:    log,
		Databases: func(ctx context.Context) ([]dbproxy.Access, error) {
			list, err := store.RemoteDatabases(ctx)
			if err != nil {
				return nil, err
			}
			var out []dbproxy.Access
			for _, d := range list {
				if d.Status != db.StatusRunning || (d.Engine != db.EngineMariaDB && d.Engine != db.EnginePostgres) || listen[d.Engine] == "" {
					continue
				}
				allow, err := dbproxy.ParseAllow(strings.Split(d.RemoteAllow, ","))
				if err != nil || len(allow) == 0 {
					log.Warn("remote database access: bad allow-list", "database", d.Name, "err", err)
					continue
				}
				out = append(out, dbproxy.Access{Engine: d.Engine, Database: d.Name, User: d.Username, Allow: allow})
			}
			return out, nil
		},
		Backend: orch.EngineAddr,
		OnSync: func(ctx context.Context, open map[string]int, dbs []dbproxy.Access) error {
			var rules []firewall.Restricted
			for _, engine := range []string{dbproxy.MariaDB, dbproxy.Postgres} {
				port, ok := open[engine]
				if !ok {
					continue
				}
				r := firewall.Restricted{Port: port}
				for _, d := range dbs {
					if d.Engine != engine {
						continue
					}
					for _, p := range d.Allow {
						if !containsPrefix(r.Sources, p) {
							r.Sources = append(r.Sources, p)
						}
					}
				}
				rules = append(rules, r)
			}
			return fw.SetRestrictedPorts(ctx, rules)
		},
	}), nil
}

func containsPrefix(list []netip.Prefix, p netip.Prefix) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}
