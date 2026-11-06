package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// ErrNoContainer is returned for lifecycle actions on container-less (static/alias) sites.
var ErrNoContainer = errors.New("this site has no runtime container")

// SiteDir is the host directory of a site.
func (o *Orchestrator) SiteDir(name string) string { return filepath.Join(o.cfg.SitesDir(), name) }

// SiteLogDir is the host directory receiving the edge access log of a site.
func (o *Orchestrator) SiteLogDir(name string) string { return filepath.Join(o.cfg.LogsDir(), name) }

// PrepareSiteDir creates the site directory tree owned by the site uid.
func (o *Orchestrator) PrepareSiteDir(d *db.Domain) error { return PrepareSiteDir(o.cfg, d) }

// PrepareSiteDir creates the site (and log) directories of d, owned by the
// site uid, with a placeholder index page on first creation. It does not need Docker.
func PrepareSiteDir(cfg *config.Config, d *db.Domain) error {
	root := filepath.Join(cfg.SitesDir(), d.Name)
	uid := SiteUID(d.ID)
	docroot := filepath.Join(root, filepath.FromSlash(d.DocumentRoot))
	created := false
	if _, err := os.Stat(docroot); errors.Is(err, os.ErrNotExist) {
		created = true
	}
	for _, dir := range []string{root, docroot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create site dir: %w", err)
		}
		if err := os.Chmod(dir, 0o755); err != nil { // independent of the process umask
			return err
		}
		_ = os.Lchown(dir, uid, uid) // best effort: fails harmlessly when not root (dev)
	}
	if err := os.MkdirAll(filepath.Join(cfg.LogsDir(), d.Name), 0o755); err != nil {
		return err
	}
	if created {
		idx := filepath.Join(docroot, "index.html")
		page := fmt.Sprintf("<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title></head>"+
			"<body style=\"font-family:system-ui;margin:4rem\"><h1>%s</h1><p>This site is hosted with OpenPlex. Upload your content in the file manager.</p></body></html>\n",
			d.Name, d.Name)
		if err := os.WriteFile(idx, []byte(page), 0o644); err != nil {
			return err
		}
		if err := os.Chmod(idx, 0o644); err != nil {
			return err
		}
		_ = os.Lchown(idx, uid, uid)
	}
	return nil
}

// siteSpec is everything that determines a site container; its hash decides
// whether an existing container must be recreated.
type siteSpec struct {
	Image    string
	Cmd      []string
	Env      []string
	WorkDir  string
	MemoryMB int
	CPUs     float64
	UID      int
	Mount    string
}

func (s siteSpec) hash() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (o *Orchestrator) specFor(d *db.Domain) (*siteSpec, error) {
	rt, ver, err := ResolveRuntime(d.Runtime, d.RuntimeVersion)
	if err != nil {
		return nil, err
	}
	if rt.ImageFormat == "" {
		return nil, ErrNoContainer
	}
	inner := path.Join(ssl.EdgeSitesRoot, d.Name) // identical path in edge and runtime (php_fastcgi needs this)
	env := []string{"HOME=/tmp", "OPENPLEX_DOMAIN=" + d.Name}
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+d.Env[k])
	}
	spec := &siteSpec{
		Image: rt.Image(ver), Env: env, WorkDir: inner, MemoryMB: d.MemoryMB, CPUs: d.CPUs,
		UID: SiteUID(d.ID), Mount: o.SiteDir(d.Name),
	}
	if rt.NeedsCommand {
		if d.StartCommand == "" || d.AppPort <= 0 {
			return nil, fmt.Errorf("runtime %s requires a start command and an app port", rt.Name)
		}
		spec.Cmd = []string{"/bin/sh", "-c", d.StartCommand}
		spec.Env = append(spec.Env, "PORT="+strconv.Itoa(d.AppPort))
		if rt.Name == "go" {
			spec.Env = append(spec.Env, "GOPATH=/tmp/go", "GOCACHE=/tmp/go-cache", "CGO_ENABLED=0")
		}
	}
	return spec, nil
}

// DeploySite creates (or recreates when its spec changed) and starts the
// site container, then updates the edge. Status is recorded in the DB.
func (o *Orchestrator) DeploySite(ctx context.Context, d *db.Domain) error {
	unlock := o.locks.lock(d.ID)
	defer unlock()
	err := o.deploy(ctx, d)
	if err != nil && !errors.Is(err, ErrNoContainer) {
		_ = o.db.SetDomainStatus(ctx, d.ID, db.StatusError, err.Error(), d.ContainerID)
		return err
	}
	if edgeErr := o.ApplyEdge(ctx); edgeErr != nil {
		_ = o.db.SetDomainStatus(ctx, d.ID, db.StatusError, "edge: "+edgeErr.Error(), d.ContainerID)
		return edgeErr
	}
	return nil
}

func (o *Orchestrator) deploy(ctx context.Context, d *db.Domain) error {
	if err := o.PrepareSiteDir(d); err != nil {
		return err
	}
	spec, err := o.specFor(d)
	if errors.Is(err, ErrNoContainer) {
		// Static site: make sure no stale runtime container lingers.
		if err := o.removeSiteContainer(ctx, d.ID); err != nil {
			return err
		}
		return o.db.SetDomainStatus(ctx, d.ID, db.StatusRunning, "", "")
	}
	if err != nil {
		return err
	}
	if err := o.ensureNetwork(ctx, EdgeNetwork, false); err != nil {
		return err
	}
	if err := o.ensureImage(ctx, spec.Image); err != nil {
		return err
	}
	name := SiteContainerName(d.ID)
	existing, err := o.findContainer(ctx, name)
	if err != nil {
		return err
	}
	h := spec.hash()
	if existing != nil && existing.Config.Labels[LabelSpec] == h {
		if !existing.State.Running {
			if err := o.cli.ContainerStart(ctx, existing.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("start: %w", err)
			}
		}
		return o.db.SetDomainStatus(ctx, d.ID, db.StatusRunning, "", existing.ID)
	}
	if existing != nil {
		if err := o.cli.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove outdated container: %w", err)
		}
	}

	labels := managedLabels("site")
	labels[LabelDomainID] = strconv.FormatInt(d.ID, 10)
	labels[LabelSpec] = h
	pids := int64(512)
	cc := &container.Config{
		Image: spec.Image, Env: spec.Env, WorkingDir: spec.WorkDir, Labels: labels,
		User:     fmt.Sprintf("%d:%d", spec.UID, spec.UID),
		Hostname: name,
	}
	if len(spec.Cmd) > 0 {
		cc.Cmd = spec.Cmd
	}
	hc := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: spec.Mount, Target: spec.WorkDir}},
		Tmpfs:         map[string]string{"/tmp": "rw,nosuid,nodev,size=512m"},
		Resources: container.Resources{
			Memory:    int64(spec.MemoryMB) * 1024 * 1024,
			NanoCPUs:  int64(spec.CPUs * 1e9),
			PidsLimit: &pids,
		},
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{EdgeNetwork: {Aliases: []string{name}}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, name)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	o.log.Info("site deployed", "domain", d.Name, "image", spec.Image, "container", created.ID[:12])
	return o.db.SetDomainStatus(ctx, d.ID, db.StatusRunning, "", created.ID)
}

// StartSite starts a stopped site.
func (o *Orchestrator) StartSite(ctx context.Context, d *db.Domain) error {
	d.Status = db.StatusRunning
	return o.DeploySite(ctx, d)
}

// StopSite stops the container (if any) and serves a maintenance page.
func (o *Orchestrator) StopSite(ctx context.Context, d *db.Domain) error {
	unlock := o.locks.lock(d.ID)
	defer unlock()
	if c, err := o.findContainer(ctx, SiteContainerName(d.ID)); err != nil {
		return err
	} else if c != nil && c.State.Running {
		timeout := 15
		if err := o.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	if err := o.db.SetDomainStatus(ctx, d.ID, db.StatusStopped, "", d.ContainerID); err != nil {
		return err
	}
	return o.ApplyEdge(ctx)
}

// RestartSite restarts the runtime container.
func (o *Orchestrator) RestartSite(ctx context.Context, d *db.Domain) error {
	unlock := o.locks.lock(d.ID)
	defer unlock()
	c, err := o.findContainer(ctx, SiteContainerName(d.ID))
	if err != nil {
		return err
	}
	if c == nil {
		return ErrNoContainer
	}
	timeout := 15
	return o.cli.ContainerRestart(ctx, c.ID, container.StopOptions{Timeout: &timeout})
}

// RemoveSite deletes the container and edge routing. Files are kept unless purge is set.
func (o *Orchestrator) RemoveSite(ctx context.Context, d *db.Domain, purge bool) error {
	unlock := o.locks.lock(d.ID)
	defer unlock()
	if err := o.removeSiteContainer(ctx, d.ID); err != nil {
		return err
	}
	if err := o.ApplyEdge(ctx); err != nil {
		return err
	}
	if purge {
		if err := os.RemoveAll(o.SiteDir(d.Name)); err != nil {
			return err
		}
		return os.RemoveAll(o.SiteLogDir(d.Name))
	}
	return nil
}

func (o *Orchestrator) removeSiteContainer(ctx context.Context, id int64) error {
	c, err := o.findContainer(ctx, SiteContainerName(id))
	if err != nil || c == nil {
		return err
	}
	if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// Reconcile converges Docker to the DB: edge up, every active site deployed,
// orphaned site containers removed, edge config applied.
func (o *Orchestrator) Reconcile(ctx context.Context) error {
	start := time.Now()
	if err := o.EnsureEdge(ctx); err != nil {
		return fmt.Errorf("edge: %w", err)
	}
	domains, err := o.db.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return err
	}
	want := map[string]bool{}
	var errs []error
	for _, d := range domains {
		if d.Kind == db.KindAlias {
			continue
		}
		want[SiteContainerName(d.ID)] = true
		if d.Status == db.StatusDisabled || d.Status == db.StatusStopped {
			continue
		}
		unlock := o.locks.lock(d.ID)
		err := o.deploy(ctx, d)
		unlock()
		if err != nil && !errors.Is(err, ErrNoContainer) {
			_ = o.db.SetDomainStatus(ctx, d.ID, db.StatusError, err.Error(), d.ContainerID)
			errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
		}
	}
	existing, err := o.listManaged(ctx, "site")
	if err != nil {
		return err
	}
	for _, c := range existing {
		if len(c.Names) == 0 {
			continue
		}
		name := c.Names[0][1:] // strip leading "/"
		if !want[name] {
			o.log.Info("removing orphaned site container", "name", name)
			if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := o.ApplyEdge(ctx); err != nil {
		errs = append(errs, fmt.Errorf("edge config: %w", err))
	}
	o.log.Info("reconcile finished", "sites", len(want), "took", time.Since(start).Round(time.Millisecond), "errors", len(errs))
	return errors.Join(errs...)
}
