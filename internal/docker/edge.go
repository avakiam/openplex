package docker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

func (o *Orchestrator) caddyEtcDir() string   { return filepath.Join(o.cfg.CaddyDir(), "etc") }
func (o *Orchestrator) caddyfilePath() string { return filepath.Join(o.caddyEtcDir(), "Caddyfile") }

// BuildEdgeConfig derives the edge configuration from the database.
func (o *Orchestrator) BuildEdgeConfig(ctx context.Context) (ssl.Config, error) {
	domains, err := o.db.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return ssl.Config{}, err
	}
	redirects, err := o.db.ListRedirects(ctx, nil)
	if err != nil {
		return ssl.Config{}, err
	}
	aliases := map[int64][]string{}
	for _, d := range domains {
		if d.Kind == db.KindAlias && d.ParentID != nil {
			aliases[*d.ParentID] = append(aliases[*d.ParentID], d.Name)
		}
	}
	rules := map[int64][]ssl.Redirect{}
	for _, r := range redirects {
		rules[r.DomainID] = append(rules[r.DomainID], ssl.Redirect{Path: r.SourcePath, Target: r.TargetURL, Code: r.StatusCode})
	}
	cfg := ssl.Config{ACMEEmail: o.cfg.ACMEEmail, PanelDomain: o.cfg.PanelDomain}
	for _, d := range domains {
		if d.Kind == db.KindAlias {
			continue
		}
		rt, _, err := ResolveRuntime(d.Runtime, d.RuntimeVersion)
		if err != nil {
			o.log.Warn("skipping site with invalid runtime", "domain", d.Name, "err", err)
			continue
		}
		s := ssl.Site{
			ID: d.ID, Name: d.Name, Aliases: aliases[d.ID], DocumentRoot: d.DocumentRoot,
			Backend: rt.Backend, SSL: d.SSLEnabled, ForceHTTPS: d.ForceHTTPS, Redirects: rules[d.ID],
		}
		switch rt.Backend {
		case ssl.BackendFastCGI:
			s.Upstream = fmt.Sprintf("%s:%d", SiteContainerName(d.ID), rt.DefaultPort)
		case ssl.BackendProxy:
			s.Upstream = fmt.Sprintf("%s:%d", SiteContainerName(d.ID), d.AppPort)
		}
		if d.Status == db.StatusDisabled || d.Status == db.StatusStopped {
			s.Backend = ssl.BackendDisabled
		}
		cfg.Sites = append(cfg.Sites, s)
	}
	return cfg, nil
}

// ApplyEdge renders the Caddyfile from the DB and hot-reloads the edge.
// On a failed reload the previous file is restored and Caddy keeps serving
// its previous (valid) configuration.
func (o *Orchestrator) ApplyEdge(ctx context.Context) error {
	o.edgeMu.Lock()
	defer o.edgeMu.Unlock()

	cfg, err := o.BuildEdgeConfig(ctx)
	if err != nil {
		return fmt.Errorf("build edge config: %w", err)
	}
	text, err := ssl.Render(cfg)
	if err != nil {
		return fmt.Errorf("render edge config: %w", err)
	}
	for _, s := range cfg.Sites {
		if err := os.MkdirAll(filepath.Join(o.cfg.LogsDir(), s.Name), 0o755); err != nil {
			return err
		}
	}
	prev, prevErr := os.ReadFile(o.caddyfilePath())
	if prevErr == nil && string(prev) == text {
		return nil
	}
	if err := writeFileAtomic(o.caddyfilePath(), []byte(text), 0o644); err != nil {
		return err
	}
	edge, err := o.findContainer(ctx, EdgeContainer)
	if err != nil {
		return err
	}
	if edge == nil || !edge.State.Running {
		return nil // picked up when the edge (re)starts
	}
	out, code, err := o.Exec(ctx, edge.ID, []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"}, "")
	if err == nil && code != 0 {
		err = fmt.Errorf("caddy reload exited %d: %s", code, strings.TrimSpace(out))
	}
	if err != nil {
		if prevErr == nil {
			_ = writeFileAtomic(o.caddyfilePath(), prev, 0o644)
		}
		return err
	}
	o.log.Info("edge configuration reloaded", "sites", len(cfg.Sites))
	return nil
}

// EnsureEdge makes sure the edge network and Caddy container exist and run.
func (o *Orchestrator) EnsureEdge(ctx context.Context) error {
	if err := o.ensureNetwork(ctx, EdgeNetwork, false); err != nil {
		return err
	}
	for _, d := range []string{o.caddyEtcDir(), filepath.Join(o.cfg.CaddyDir(), "data"), filepath.Join(o.cfg.CaddyDir(), "config")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	if _, err := os.Stat(o.caddyfilePath()); errors.Is(err, fs.ErrNotExist) {
		text, err := ssl.Render(ssl.Config{ACMEEmail: o.cfg.ACMEEmail, PanelDomain: o.cfg.PanelDomain})
		if err != nil {
			return err
		}
		if err := writeFileAtomic(o.caddyfilePath(), []byte(text), 0o644); err != nil {
			return err
		}
	}
	if err := o.ensureImage(ctx, EdgeImage); err != nil {
		return err
	}
	existing, err := o.findContainer(ctx, EdgeContainer)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.Config.Image == EdgeImage {
			if !existing.State.Running {
				return o.cli.ContainerStart(ctx, existing.ID, container.StartOptions{})
			}
			return nil
		}
		o.log.Info("upgrading edge container", "from", existing.Config.Image, "to", EdgeImage)
		if err := o.cli.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove old edge: %w", err)
		}
	}

	ports := nat.PortMap{
		"80/tcp":  {{HostPort: "80"}},
		"443/tcp": {{HostPort: "443"}},
		"443/udp": {{HostPort: "443"}},
	}
	exposed := nat.PortSet{"80/tcp": {}, "443/tcp": {}, "443/udp": {}}
	hc := &container.HostConfig{
		PortBindings:  ports,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: o.caddyEtcDir(), Target: "/etc/caddy", ReadOnly: true},
			{Type: mount.TypeBind, Source: filepath.Join(o.cfg.CaddyDir(), "data"), Target: "/data"},
			{Type: mount.TypeBind, Source: filepath.Join(o.cfg.CaddyDir(), "config"), Target: "/config"},
			{Type: mount.TypeBind, Source: o.cfg.SitesDir(), Target: ssl.EdgeSitesRoot, ReadOnly: true},
			{Type: mount.TypeBind, Source: o.cfg.LogsDir(), Target: ssl.EdgeLogsRoot},
			{Type: mount.TypeBind, Source: o.cfg.RunDir(), Target: filepath.Dir(ssl.EdgePanelSock)},
		},
		CapDrop:     []string{"ALL"},
		CapAdd:      []string{"NET_BIND_SERVICE"},
		SecurityOpt: []string{"no-new-privileges:true"},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "20m", "max-file": "3"}},
	}
	cc := &container.Config{
		Image:        EdgeImage,
		ExposedPorts: exposed,
		Labels:       managedLabels("edge"),
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{EdgeNetwork: {}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, EdgeContainer)
	if err != nil {
		return fmt.Errorf("create edge: %w", err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start edge: %w", err)
	}
	o.log.Info("edge proxy started", "image", EdgeImage)
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
