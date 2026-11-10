package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// Shell is an interactive TTY session inside a site's environment.
type Shell struct {
	o       *Orchestrator
	execID  string
	conn    types.HijackedResponse
	cleanup func()
	// Ephemeral is true when the shell runs in a throwaway container (static sites).
	Ephemeral bool
}

// Reader streams the terminal output.
func (s *Shell) Reader() io.Reader { return s.conn.Reader }

// Write sends keystrokes to the terminal.
func (s *Shell) Write(p []byte) (int, error) { return s.conn.Conn.Write(p) }

// Resize changes the TTY size.
func (s *Shell) Resize(ctx context.Context, cols, rows uint) error {
	if cols == 0 || rows == 0 || cols > 1000 || rows > 1000 {
		return nil
	}
	return s.o.cli.ContainerExecResize(ctx, s.execID, container.ResizeOptions{Width: cols, Height: rows})
}

// Close ends the session and removes any throwaway container. Docker does
// not hang up a TTY exec when the attach stream closes, so the shell's whole
// session (including jobs it started) is signalled explicitly.
func (s *Shell) Close() {
	if s.execID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if insp, err := s.o.cli.ContainerExecInspect(ctx, s.execID); err == nil && insp.Running && insp.Pid > 0 {
			killSession(insp.Pid)
		}
		cancel()
	}
	s.conn.Close()
	if s.cleanup != nil {
		s.cleanup()
	}
}

// shellCmd prefers bash (login shell) and falls back to sh.
var shellCmd = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}

// OpenShell starts a shell for website d. Runtime sites get a shell inside
// their own container (same environment as the app); static sites get a
// throwaway tools container with the site directory mounted. The shell runs
// as the site's uid unless asRoot (admins only) is set.
func (o *Orchestrator) OpenShell(ctx context.Context, d *db.Domain, asRoot bool, cols, rows uint) (*Shell, error) {
	uid := SiteUID(d.ID)
	user := fmt.Sprintf("%d:%d", uid, uid)
	if asRoot {
		user = "0:0"
	}
	workdir := path.Join(ssl.EdgeSitesRoot, d.Name)
	sh := &Shell{o: o}

	target := ""
	if c, err := o.findContainer(ctx, SiteContainerName(d.ID)); err != nil {
		return nil, err
	} else if c != nil && c.State.Running && d.Runtime != "static" { // a static site's container only has its web server
		target = c.ID
	}
	if target == "" {
		if d.Runtime != "static" {
			return nil, fmt.Errorf("the website's container is not running; start or redeploy it first")
		}
		id, cleanup, err := o.ephemeralShell(ctx, d, user, workdir)
		if err != nil {
			return nil, err
		}
		target, sh.cleanup, sh.Ephemeral = id, cleanup, true
	}

	size := [2]uint{rows, cols}
	if rows == 0 || cols == 0 {
		size = [2]uint{24, 80}
	}
	ex, err := o.cli.ContainerExecCreate(ctx, target, container.ExecOptions{
		Cmd: shellCmd, User: user, WorkingDir: workdir, Tty: true, ConsoleSize: &size,
		AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "LANG=C.UTF-8", "HOME=/tmp",
			"OPENPLEX_SITE=" + d.Name, "PS1=\\[\\e[1;34m\\]" + d.Name + ":\\w\\[\\e[0m\\] \\$ "},
	})
	if err != nil {
		if sh.cleanup != nil {
			sh.cleanup()
		}
		return nil, fmt.Errorf("start shell: %w", err)
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{Tty: true, ConsoleSize: &size})
	if err != nil {
		if sh.cleanup != nil {
			sh.cleanup()
		}
		return nil, fmt.Errorf("attach shell: %w", err)
	}
	sh.execID, sh.conn = ex.ID, att
	return sh, nil
}

func (o *Orchestrator) ephemeralShell(ctx context.Context, d *db.Domain, user, workdir string) (string, func(), error) {
	if err := o.EnsureShellImage(ctx); err != nil {
		return "", nil, err
	}
	tenantNet, err := o.ensureTenantNetwork(ctx, d.OwnerID)
	if err != nil {
		return "", nil, err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := fmt.Sprintf("opx-shell-%d-%s", d.ID, hex.EncodeToString(b))
	labels := managedLabels("shell")
	labels[LabelDomainID] = fmt.Sprint(d.ID)
	pids := int64(256)
	cc := &container.Config{
		Image: ShellImage(), Cmd: []string{"sleep", "infinity"}, User: user, WorkingDir: workdir,
		Labels: labels, Hostname: d.Name,
	}
	hc := &container.HostConfig{
		Mounts:      []mount.Mount{{Type: mount.TypeBind, Source: o.SiteDir(d.Name), Target: workdir}},
		Tmpfs:       map[string]string{"/tmp": "rw,nosuid,nodev,size=256m"},
		Resources:   container.Resources{Memory: 512 << 20, NanoCPUs: 1e9, PidsLimit: &pids},
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Init:        boolPtr(true),
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{tenantNet: {}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, name)
	if err != nil {
		return "", nil, fmt.Errorf("create shell container: %w", err)
	}
	cleanup := func() {
		_ = o.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("start shell container: %w", err)
	}
	return created.ID, cleanup, nil
}

// RemoveStaleShells deletes throwaway shell containers left by a crash.
func (o *Orchestrator) RemoveStaleShells(ctx context.Context) {
	list, err := o.listManaged(ctx, "shell")
	if err != nil {
		return
	}
	for _, c := range list {
		_ = o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
	}
}

func boolPtr(b bool) *bool { return &b }
