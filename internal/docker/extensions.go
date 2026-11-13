package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// ExtensionSpec is everything that determines an extension's container.
type ExtensionSpec struct {
	ID        string
	Image     string // reference (pulled unless Archive provides it)
	Archive   string // `docker save` file to load the image from, or ""
	Port      int
	MemoryMB  int
	CPUs      float64
	UID       int
	DataDir   string // mounted at /data
	SocketDir string // mounted at /run/openplex (holds panel.sock)
	SitesDir  string // files:read: the websites' files, mounted read-only at /sites ("" = not mounted)
	Env       []string
}

const (
	labelExtension = "openplex.extension"
	labelExtPort   = "openplex.port"
)

// ExtensionContainerName is the container of extension id.
func ExtensionContainerName(id string) string { return "opx-ext-" + id }

// ExtensionNetwork is the private network of extension id.
func ExtensionNetwork(id string) string { return "openplex_x_" + id }

// EnsureExtension (re)creates and starts the extension's container. It runs
// as its own uid with no capabilities, a read-only root filesystem and only
// its own network: it reaches the internet, and the panel through the socket.
func (o *Orchestrator) EnsureExtension(ctx context.Context, s ExtensionSpec) error {
	if err := o.extensionImage(ctx, s.Image, s.Archive); err != nil {
		return err
	}
	net := ExtensionNetwork(s.ID)
	if err := o.ensureExtensionNetwork(ctx, net, s.ID); err != nil {
		return err
	}
	env := append([]string{
		"PORT=" + strconv.Itoa(s.Port), "HOME=/tmp",
		"OPENPLEX_SOCKET=/run/openplex/panel.sock", "OPENPLEX_DATA=/data",
	}, s.Env...)
	img, err := o.cli.ImageInspect(ctx, s.Image)
	if err != nil {
		return fmt.Errorf("inspect extension image: %w", err)
	}
	// The image ID is part of the spec: a package rebuilt under the same tag
	// replaces the container too.
	h := siteSpec{Image: s.Image + "@" + img.ID, Env: env, MemoryMB: s.MemoryMB, CPUs: s.CPUs, UID: s.UID, Mount: s.DataDir + "|" + s.SocketDir + "|" + s.SitesDir,
		Networks: []string{net}}.hash()
	name := ExtensionContainerName(s.ID)
	existing, err := o.findContainer(ctx, name)
	if err != nil {
		return err
	}
	if existing != nil && existing.Config.Labels[LabelSpec] == h {
		if !existing.State.Running {
			return o.cli.ContainerStart(ctx, existing.ID, container.StartOptions{})
		}
		return nil
	}
	if existing != nil {
		if err := o.cli.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove old extension container: %w", err)
		}
	}
	labels := managedLabels("extension")
	labels[LabelSpec] = h
	labels[labelExtension] = s.ID
	labels[labelExtPort] = strconv.Itoa(s.Port)
	pids := int64(256)
	ro := true
	cc := &container.Config{Image: s.Image, Env: env, Labels: labels, User: fmt.Sprintf("%d:%d", s.UID, s.UID), Hostname: "ext-" + strings.ReplaceAll(s.ID, ".", "-")}
	hc := &container.HostConfig{
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Binds:          []string{s.DataDir + ":/data", s.SocketDir + ":/run/openplex"},
		Tmpfs:          map[string]string{"/tmp": "rw,nosuid,nodev,size=64m"},
		ReadonlyRootfs: ro,
		Resources: container.Resources{
			Memory: int64(s.MemoryMB) << 20, NanoCPUs: int64(s.CPUs * 1e9), PidsLimit: &pids,
		},
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	if s.SitesDir != "" {
		// Read-only, and the root group only to get through the sites
		// directory (0750 root:root): no capability is added.
		hc.Binds = append(hc.Binds, s.SitesDir+":/sites:ro")
		hc.GroupAdd = []string{"0"}
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{net: {}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, name)
	if err != nil {
		return fmt.Errorf("create extension container: %w", err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start extension container: %w", err)
	}
	o.log.Info("extension started", "id", s.ID, "image", s.Image)
	return nil
}

// extensionImage makes ref available: loaded from the package's archive
// (whose tag must be ref) or pulled from its registry.
func (o *Orchestrator) extensionImage(ctx context.Context, ref, archive string) error {
	if archive != "" {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		res, err := o.cli.ImageLoad(ctx, f)
		if err != nil {
			return fmt.Errorf("load extension image: %w", err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if _, err := o.cli.ImageInspect(ctx, ref); err != nil {
			return fmt.Errorf("the image archive does not contain %s (tag the image with backend.image before saving it)", ref)
		}
		return nil
	}
	return o.ensureImage(ctx, ref)
}

func (o *Orchestrator) ensureExtensionNetwork(ctx context.Context, name, id string) error {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	if _, err := o.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return err
	}
	subnet, err := o.freeTenantSubnet(ctx)
	if err != nil {
		return err
	}
	labels := managedLabels("extension-network")
	labels[labelExtension] = id
	_, err = o.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge", Labels: labels,
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: subnet}}}})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create %s: %w", name, err)
	}
	return nil
}

// RemoveExtension deletes the extension's container and network (not its data).
func (o *Orchestrator) RemoveExtension(ctx context.Context, id string) error {
	if c, err := o.findContainer(ctx, ExtensionContainerName(id)); err != nil {
		return err
	} else if c != nil {
		if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove extension container: %w", err)
		}
	}
	if err := o.cli.NetworkRemove(ctx, ExtensionNetwork(id)); err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove extension network: %w", err)
	}
	return nil
}

// ExtensionAddr returns ip:port of the extension's backend, reachable from
// the host.
func (o *Orchestrator) ExtensionAddr(ctx context.Context, id string) (string, error) {
	c, err := o.findContainer(ctx, ExtensionContainerName(id))
	if err != nil {
		return "", err
	}
	if c == nil || !c.State.Running {
		return "", fmt.Errorf("extension %s is not running", id)
	}
	ep := c.NetworkSettings.Networks[ExtensionNetwork(id)]
	if ep == nil || ep.IPAddress == "" {
		return "", fmt.Errorf("extension %s has no address", id)
	}
	return ep.IPAddress + ":" + c.Config.Labels[labelExtPort], nil
}

// ExtensionIDs lists the extensions that have a container.
func (o *Orchestrator) ExtensionIDs(ctx context.Context) ([]string, error) {
	list, err := o.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelKind+"=extension"))})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range list {
		if id := c.Labels[labelExtension]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
