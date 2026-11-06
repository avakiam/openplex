// Package docker drives the Docker Engine: the edge proxy, per-site runtime
// containers and their lifecycle. The panel database is the source of truth;
// Reconcile converges Docker towards it.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
)

// Labels and names.
const (
	LabelManaged  = "openplex.managed"
	LabelKind     = "openplex.kind"
	LabelDomainID = "openplex.domain_id"
	LabelSpec     = "openplex.spec"

	EdgeNetwork   = "openplex_edge"
	EdgeContainer = "openplex-edge"
	EdgeImage     = "caddy:2.10-alpine"
)

// SiteContainerName returns the container name for a website.
func SiteContainerName(domainID int64) string { return fmt.Sprintf("opx-site-%d", domainID) }

// SiteUID is the unprivileged uid/gid a site's container and files run as.
func SiteUID(domainID int64) int { return 100000 + int(domainID) }

// Orchestrator owns all Docker interactions.
type Orchestrator struct {
	cli *client.Client
	db  *db.DB
	cfg *config.Config
	log *slog.Logger

	edgeMu sync.Mutex // serializes edge config writes/reloads
	locks  keyedMutex // per-site lifecycle locks
}

// New connects to the Docker Engine and verifies it responds.
func New(ctx context.Context, cfg *config.Config, store *db.DB, log *slog.Logger) (*Orchestrator, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if cfg.DockerHost != "" {
		opts = append(opts, client.WithHost(cfg.DockerHost))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker engine unreachable: %w", err)
	}
	return &Orchestrator{cli: cli, db: store, cfg: cfg, log: log}, nil
}

// Close releases the client.
func (o *Orchestrator) Close() error { return o.cli.Close() }

// Client exposes the raw client for terminal/stats helpers.
func (o *Orchestrator) Client() *client.Client { return o.cli }

// Info returns engine version information.
func (o *Orchestrator) Info(ctx context.Context) (map[string]any, error) {
	v, err := o.cli.ServerVersion(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": v.Version, "api_version": v.APIVersion, "os": v.Os, "arch": v.Arch, "kernel": v.KernelVersion}, nil
}

func managedLabels(kind string) map[string]string {
	return map[string]string{LabelManaged: "true", LabelKind: kind}
}

// ensureNetwork creates a labelled bridge network if missing.
func (o *Orchestrator) ensureNetwork(ctx context.Context, name string, internal bool) error {
	if _, err := o.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	_, err := o.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge", Internal: internal, Labels: managedLabels("network"),
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create network %s: %w", name, err)
	}
	return nil
}

// ensureImage pulls ref if it is not present locally.
func (o *Orchestrator) ensureImage(ctx context.Context, ref string) error {
	if _, err := o.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	o.log.Info("pulling image", "image", ref)
	rc, err := o.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer rc.Close()
	// The pull only completes once the progress stream is drained.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	return nil
}

// findContainer returns the container named name, or nil if absent.
func (o *Orchestrator) findContainer(ctx context.Context, name string) (*container.InspectResponse, error) {
	c, err := o.cli.ContainerInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect %s: %w", name, err)
	}
	if c.Config == nil || c.Config.Labels[LabelManaged] != "true" {
		return nil, fmt.Errorf("container %s exists but is not managed by OpenPlex; refusing to touch it", name)
	}
	return &c, nil
}

// listManaged lists managed containers of a kind.
func (o *Orchestrator) listManaged(ctx context.Context, kind string) ([]container.Summary, error) {
	f := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelKind+"="+kind))
	return o.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
}

// Exec runs cmd inside a container and returns combined output and exit code.
func (o *Orchestrator) Exec(ctx context.Context, containerID string, cmd []string, user string) (string, int, error) {
	ex, err := o.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd: cmd, User: user, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return "", -1, fmt.Errorf("exec create: %w", err)
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", -1, fmt.Errorf("exec attach: %w", err)
	}
	defer att.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, att.Reader); err != nil {
		return out.String(), -1, fmt.Errorf("exec read: %w", err)
	}
	insp, err := o.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return out.String(), -1, fmt.Errorf("exec inspect: %w", err)
	}
	return out.String(), insp.ExitCode, nil
}

// keyedMutex provides one mutex per int64 key.
type keyedMutex struct {
	mu sync.Mutex
	m  map[int64]*sync.Mutex
}

func (k *keyedMutex) lock(id int64) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[int64]*sync.Mutex{}
	}
	l, ok := k.m[id]
	if !ok {
		l = &sync.Mutex{}
		k.m[id] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}
