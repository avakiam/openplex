package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

// Webmail (Roundcube) behind the edge, talking to the mail server over MailNetwork.
const (
	WebmailImage     = "roundcube/roundcubemail:1.7.6-apache"
	WebmailContainer = "openplex-webmail"
	// WebmailUpstream is what the edge proxies webmail hosts to.
	WebmailUpstream = WebmailContainer + ":80"
)

// EnsureWebmail runs Roundcube. dir (on the host) holds config/ (PHP files
// included by the image), plugin/openplex/ and plugin/post-setup.sh; version
// changes whenever the plugin does, so the container is recreated with it.
func (o *Orchestrator) EnsureWebmail(ctx context.Context, dir, version string) error {
	if err := o.ensureNetwork(ctx, EdgeNetwork, false); err != nil {
		return err
	}
	if err := o.ensureNetwork(ctx, MailNetwork, true); err != nil {
		return err
	}
	env := []string{
		"ROUNDCUBEMAIL_DB_TYPE=sqlite",
		"ROUNDCUBEMAIL_SKIN=elastic",
		"ROUNDCUBEMAIL_DEFAULT_HOST=tls://" + MailContainer, "ROUNDCUBEMAIL_DEFAULT_PORT=143",
		"ROUNDCUBEMAIL_SMTP_SERVER=tls://" + MailContainer, "ROUNDCUBEMAIL_SMTP_PORT=587",
		"ROUNDCUBEMAIL_PLUGINS=archive,zipdownload,markasjunk,openplex",
		"ROUNDCUBEMAIL_UPLOAD_MAX_FILESIZE=25M",
	}
	sum := sha256.Sum256([]byte(WebmailImage + "|" + dir + "|" + version + "|" + strings.Join(env, ",")))
	hash := hex.EncodeToString(sum[:8])
	c, err := o.findContainer(ctx, WebmailContainer)
	if err != nil {
		return err
	}
	if c != nil && c.Config.Labels[labelConfig] == hash {
		if !c.State.Running {
			return o.cli.ContainerStart(ctx, c.ID, container.StartOptions{})
		}
		return nil
	}
	if c != nil {
		if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove old webmail: %w", err)
		}
	}
	if err := o.ensureImage(ctx, WebmailImage); err != nil {
		return err
	}
	labels := managedLabels("webmail")
	labels[labelConfig] = hash
	cc := &container.Config{Image: WebmailImage, Env: env, Labels: labels}
	hc := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: "openplex-webmail-db", Target: "/var/roundcube/db"},
			{Type: mount.TypeBind, Source: filepath.Join(dir, "config"), Target: "/var/roundcube/config", ReadOnly: true},
			{Type: mount.TypeBind, Source: filepath.Join(dir, "plugin", "openplex"), Target: "/var/roundcube/openplex", ReadOnly: true},
			{Type: mount.TypeBind, Source: filepath.Join(dir, "plugin", "post-setup.sh"), Target: "/entrypoint-tasks/post-setup/openplex", ReadOnly: true},
		},
		Resources: container.Resources{Memory: 512 << 20},
		LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "20m", "max-file": "3"}},
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{EdgeNetwork: {}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, WebmailContainer)
	if err != nil {
		return fmt.Errorf("create webmail: %w", err)
	}
	if err := o.cli.NetworkConnect(ctx, MailNetwork, created.ID, &network.EndpointSettings{}); err != nil {
		_ = o.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("connect webmail to %s: %w", MailNetwork, err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = o.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start webmail: %w", err)
	}
	o.log.Info("webmail started", "image", WebmailImage)
	return nil
}

// StopWebmail removes the webmail container (its settings stay in a volume).
func (o *Orchestrator) StopWebmail(ctx context.Context) error {
	c, err := o.findContainer(ctx, WebmailContainer)
	if err != nil || c == nil {
		return err
	}
	return o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
}

// WebmailRunning reports whether the webmail container runs.
func (o *Orchestrator) WebmailRunning(ctx context.Context) bool {
	c, err := o.findContainer(ctx, WebmailContainer)
	return err == nil && c != nil && c.State != nil && c.State.Running
}
