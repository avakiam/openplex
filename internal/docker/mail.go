package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// Mail server (docker-mailserver: Postfix, Dovecot, Rspamd).
const (
	MailImage     = "mailserver/docker-mailserver:16.0.1"
	MailContainer = "openplex-mail"
)

// MailPorts are published on every host address: SMTP, submissions,
// submission, IMAP(S), POP3(S), ManageSieve.
var MailPorts = []string{"25", "465", "587", "143", "993", "110", "995", "4190"}

// EnsureMail runs docker-mailserver for hostname with its configuration in
// configDir (on the host). It is recreated when the image or hostname change;
// account and alias changes are picked up by the container itself.
func (o *Orchestrator) EnsureMail(ctx context.Context, hostname, configDir string) error {
	env := []string{
		"OVERRIDE_HOSTNAME=" + hostname,
		"POSTMASTER_ADDRESS=postmaster@" + hostname,
		"LOG_LEVEL=warn",
		"ONE_DIR=1",
		"PERMIT_DOCKER=none", // nothing on Docker networks is trusted to relay
		"SPOOF_PROTECTION=1",
		"ENABLE_SRS=1", // forwarded mail keeps passing SPF
		"ENABLE_RSPAMD=1", "ENABLE_RSPAMD_REDIS=1", "RSPAMD_LEARN=1", "MOVE_SPAM_TO_JUNK=1",
		"ENABLE_OPENDKIM=0", "ENABLE_OPENDMARC=0", "ENABLE_POLICYD_SPF=0", "ENABLE_AMAVIS=0", "ENABLE_SPAMASSASSIN=0",
		"ENABLE_CLAMAV=0", "ENABLE_FAIL2BAN=0", "ENABLE_UPDATE_CHECK=0",
		"ENABLE_QUOTAS=1", "ENABLE_POP3=1", "ENABLE_MANAGESIEVE=1",
		"POSTFIX_MESSAGE_SIZE_LIMIT=52428800",
		"SSL_TYPE=manual", "SSL_CERT_PATH=/tmp/docker-mailserver/ssl/cert.pem", "SSL_KEY_PATH=/tmp/docker-mailserver/ssl/key.pem",
	}
	sum := sha256.Sum256([]byte(MailImage + "|" + hostname + "|" + configDir + "|" + strings.Join(env, ",")))
	hash := hex.EncodeToString(sum[:8])
	c, err := o.findContainer(ctx, MailContainer)
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
			return fmt.Errorf("remove old mail server: %w", err)
		}
	}
	if err := o.ensureImage(ctx, MailImage); err != nil {
		return err
	}
	ports, exposed := nat.PortMap{}, nat.PortSet{}
	for _, p := range MailPorts {
		port := nat.Port(p + "/tcp")
		exposed[port] = struct{}{}
		ports[port] = []nat.PortBinding{{HostPort: p}}
	}
	labels := managedLabels("mail")
	labels[labelConfig] = hash
	cc := &container.Config{Image: MailImage, Hostname: hostname, Env: env, Labels: labels, ExposedPorts: exposed, StopTimeout: ptrInt(30)}
	hc := &container.HostConfig{
		PortBindings:  ports,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: "openplex-mail-data", Target: "/var/mail"},
			{Type: mount.TypeVolume, Source: "openplex-mail-state", Target: "/var/mail-state"},
			{Type: mount.TypeVolume, Source: "openplex-mail-logs", Target: "/var/log/mail"},
			{Type: mount.TypeBind, Source: configDir, Target: "/tmp/docker-mailserver"},
		},
		SecurityOpt: []string{"no-new-privileges:true"},
		Resources:   container.Resources{Memory: 1536 << 20},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "20m", "max-file": "3"}},
	}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nil, nil, MailContainer)
	if err != nil {
		return fmt.Errorf("create mail server: %w", err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = o.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start mail server: %w", err)
	}
	o.log.Info("mail server started", "image", MailImage, "hostname", hostname)
	return nil
}

func ptrInt(n int) *int { return &n }

// StopMail removes the mail container (mailboxes stay in their volume).
func (o *Orchestrator) StopMail(ctx context.Context) error {
	c, err := o.findContainer(ctx, MailContainer)
	if err != nil || c == nil {
		return err
	}
	return o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
}

// MailStatus reports whether the mail container runs and its health.
func (o *Orchestrator) MailStatus(ctx context.Context) (running bool, health string) {
	c, err := o.findContainer(ctx, MailContainer)
	if err != nil || c == nil || c.State == nil {
		return false, ""
	}
	if c.State.Health != nil {
		health = c.State.Health.Status
	}
	return c.State.Running, health
}

// MailExec runs a command in the mail container and returns its output.
func (o *Orchestrator) MailExec(ctx context.Context, cmd ...string) (string, error) {
	ex, err := o.cli.ContainerExecCreate(ctx, MailContainer, container.ExecOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return "", err
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer att.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, io.LimitReader(att.Reader, 4<<20)); err != nil {
		return out.String(), err
	}
	insp, err := o.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return out.String(), err
	}
	if insp.ExitCode != 0 {
		return out.String(), fmt.Errorf("exit %d: %s", insp.ExitCode, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}
