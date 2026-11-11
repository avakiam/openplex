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
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// ErrNoContainer is returned for lifecycle actions on sites without a container.
var ErrNoContainer = errors.New("this site has no runtime container")

// SiteDir is the host directory of a site.
func (o *Orchestrator) SiteDir(name string) string { return filepath.Join(o.cfg.SitesDir(), name) }

// SiteLogDir is the host directory receiving the edge access log of a site.
func (o *Orchestrator) SiteLogDir(name string) string { return filepath.Join(o.cfg.LogsDir(), name) }

// PrepareSiteDir creates the site directory tree owned by the site uid.
func (o *Orchestrator) PrepareSiteDir(d *db.Domain) error { return PrepareSiteDir(o.cfg, d) }

// PrepareSiteDir creates the site (and log) directories of d, owned by the
// site uid, with a placeholder index page on first creation. It does not need Docker.
// Everything below the site directory is tenant-controlled, so it is only
// touched through an os.Root: a symlink there can't make the panel (root)
// create, chmod or chown anything outside the site.
func PrepareSiteDir(cfg *config.Config, d *db.Domain) error {
	root := filepath.Join(cfg.SitesDir(), d.Name)
	uid := SiteUID(d.ID)
	if err := os.MkdirAll(cfg.SitesDir(), 0o750); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create site dir: %w", err)
	}
	if fi, err := os.Lstat(root); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("site dir %s is not a directory", root)
	}
	if err := os.Chmod(root, 0o755); err != nil { // independent of the process umask
		return err
	}
	_ = os.Lchown(root, uid, uid) // best effort: fails harmlessly when not root (dev)
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	docroot := filepath.FromSlash(d.DocumentRoot)
	created := false
	if _, err := r.Stat(docroot); errors.Is(err, os.ErrNotExist) {
		created = true
		if err := r.MkdirAll(docroot, 0o755); err != nil {
			return fmt.Errorf("create document root: %w", err)
		}
		if err := r.Chmod(docroot, 0o755); err != nil {
			return err
		}
		_ = r.Lchown(docroot, uid, uid)
	} else if err != nil {
		return fmt.Errorf("document root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.LogsDir(), d.Name), 0o755); err != nil {
		return err
	}
	if created {
		idx := filepath.Join(docroot, "index.html")
		page := fmt.Sprintf("<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title></head>"+
			"<body style=\"font-family:system-ui;margin:4rem\"><h1>%s</h1><p>This site is hosted with OpenPlex. Upload your content in the file manager.</p></body></html>\n",
			d.Name, d.Name)
		f, err := r.OpenFile(idx, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(page)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if err := r.Chmod(idx, 0o644); err != nil {
			return err
		}
		_ = r.Lchown(idx, uid, uid)
	}
	return nil
}

// WebPort is where a static or PHP site's own web server listens. Those sites
// serve their files from inside their container, as the site's uid: the edge
// never reads tenant files, so a symlink in a site can't reach another site's
// files or the edge's TLS keys.
const WebPort = 8080

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
	ReadOnly bool
	Networks []string
	// PHP settings of the website (omitted when it has none, so the hash of
	// websites without settings is unchanged): the host directory mounted at
	// phpSettingsDir and the ini content, so a change recreates the container.
	PHPConf string `json:",omitempty"`
	PHPIni  string `json:",omitempty"`
}

// phpSettingsDir is where a PHP container finds its website's PHP settings.
const phpSettingsDir = "/etc/openplex/site-php"

// PHPSettingsDir is the host directory holding a website's PHP settings file.
func (o *Orchestrator) PHPSettingsDir(id int64) string {
	return filepath.Join(o.cfg.DataDir, "php", strconv.FormatInt(id, 10))
}

// applyPHPSettings writes the website's PHP settings (if any) and adds them
// to its container spec. The directory belongs to root, outside the
// website's files: the website can read its settings but not change them.
func (o *Orchestrator) applyPHPSettings(ctx context.Context, d *db.Domain, spec *siteSpec) error {
	settings, err := o.db.GetPHPSettings(ctx, d.ID)
	if err != nil {
		return err
	}
	dir := o.PHPSettingsDir(d.ID)
	if len(settings) == 0 {
		return os.RemoveAll(dir)
	}
	ini := PHPIni(settings, d.MemoryMB)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("PHP settings dir: %w", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".zz-site.ini.tmp")
	if err := os.WriteFile(tmp, []byte(ini), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "zz-site.ini")); err != nil {
		return err
	}
	spec.PHPConf, spec.PHPIni = dir, ini
	// A leading ":" keeps the image's own conf.d and adds the website's after it.
	spec.Env = append(spec.Env, "PHP_INI_SCAN_DIR=:"+phpSettingsDir)
	return nil
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
	inner := path.Join(ssl.EdgeSitesRoot, d.Name) // the site's directory inside its container
	docroot := path.Join(inner, d.DocumentRoot)
	if rt.Name == "static" {
		mem := d.MemoryMB
		if mem <= 0 || mem > 128 {
			mem = 128
		}
		return &siteSpec{
			Image: StaticImage(), WorkDir: inner, MemoryMB: mem, CPUs: d.CPUs, UID: SiteUID(d.ID),
			Mount: o.SiteDir(d.Name), ReadOnly: true, Networks: []string{TenantNetwork(d.OwnerID)},
			Env: []string{"HOME=/tmp", "OPENPLEX_DOCROOT=" + docroot},
		}, nil
	}
	if rt.ImageFormat == "" {
		return nil, ErrNoContainer
	}
	env := []string{"HOME=/tmp", "OPENPLEX_DOMAIN=" + d.Name}
	if rt.Build {
		env = append(env, "OPENPLEX_DOCROOT="+docroot)
	}
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
		UID: SiteUID(d.ID), Mount: o.SiteDir(d.Name), Networks: []string{TenantNetwork(d.OwnerID)},
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
	if err != nil {
		return err
	}
	if Runtimes[d.Runtime].Build {
		if err := o.applyPHPSettings(ctx, d, spec); err != nil {
			return err
		}
	}
	if _, err := o.ensureTenantNetwork(ctx, d.OwnerID); err != nil {
		return err
	}
	if rt := Runtimes[d.Runtime]; rt.Build {
		if _, err := o.cli.ImageInspect(ctx, spec.Image); err != nil {
			_ = o.db.SetDomainStatus(ctx, d.ID, db.StatusPending, "building the PHP "+d.RuntimeVersion+" runtime (first use, a few minutes)", d.ContainerID)
		}
		if err := o.ensurePHPImage(ctx, d.RuntimeVersion); err != nil {
			return err
		}
	} else if rt.Name == "static" {
		if err := o.EnsureStaticImage(ctx); err != nil {
			return err
		}
	} else if err := o.ensureImage(ctx, spec.Image); err != nil {
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
		Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: spec.Mount, Target: spec.WorkDir, ReadOnly: spec.ReadOnly}},
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
	if spec.PHPConf != "" {
		hc.Mounts = append(hc.Mounts, mount.Mount{Type: mount.TypeBind, Source: spec.PHPConf, Target: phpSettingsDir, ReadOnly: true})
	}
	eps := map[string]*network.EndpointSettings{}
	for _, n := range spec.Networks {
		eps[n] = &network.EndpointSettings{Aliases: []string{name}}
	}
	nc := &network.NetworkingConfig{EndpointsConfig: eps}
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

// HaltSite stops every container that can write to the site's files (its
// runtime and any open shell) without changing its status: used while a
// restore rewrites the files. DeploySite starts it again.
func (o *Orchestrator) HaltSite(ctx context.Context, domainID int64) error {
	unlock := o.locks.lock(domainID)
	defer unlock()
	f := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelDomainID+"="+strconv.FormatInt(domainID, 10)))
	list, err := o.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Labels[LabelKind] == "shell" {
			if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				return fmt.Errorf("close shell: %w", err)
			}
			continue
		}
		timeout := 15
		if err := o.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	return nil
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
	if err := os.RemoveAll(o.PHPSettingsDir(d.ID)); err != nil {
		o.log.Warn("remove PHP settings", "domain", d.Name, "err", err)
	}
	if err := o.ApplyEdge(ctx); err != nil {
		return err
	}
	if err := o.pruneTenantNetworks(ctx); err != nil {
		o.log.Warn("prune account networks", "err", err)
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
	if err := o.pruneTenantNetworks(ctx); err != nil {
		errs = append(errs, fmt.Errorf("account networks: %w", err))
	}
	o.log.Info("reconcile finished", "sites", len(want), "took", time.Since(start).Round(time.Millisecond), "errors", len(errs))
	return errors.Join(errs...)
}
