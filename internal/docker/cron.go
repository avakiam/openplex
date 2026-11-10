package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// ErrSiteNotRunning means a runtime website's container is down, so a task
// cannot run in its environment.
var ErrSiteNotRunning = errors.New("the website's container is not running")

// RunSiteCommand runs a non-interactive shell command for website d as the
// site's own user: inside its container (same PHP/Node/Python environment as
// the app), or in a throwaway tools container for static sites. stdout and
// stderr go to w. When ctx ends first, the command and every process it
// started are killed and ctx.Err() is returned.
func (o *Orchestrator) RunSiteCommand(ctx context.Context, d *db.Domain, command string, w io.Writer) (int, error) {
	uid := SiteUID(d.ID)
	user := fmt.Sprintf("%d:%d", uid, uid)
	workdir := path.Join(ssl.EdgeSitesRoot, d.Name)

	target := ""
	c, err := o.findContainer(ctx, SiteContainerName(d.ID))
	if err != nil {
		return -1, err
	}
	if c != nil && c.State.Running && d.Runtime != "static" { // a static site's container only has its web server
		target = c.ID
	}
	if target == "" {
		if d.Runtime != "static" {
			return -1, ErrSiteNotRunning
		}
		id, cleanup, err := o.ephemeralShell(ctx, d, user, workdir)
		if err != nil {
			return -1, err
		}
		defer cleanup()
		target = id
	}

	ex, err := o.cli.ContainerExecCreate(ctx, target, container.ExecOptions{
		Cmd: []string{"/bin/sh", "-c", command}, User: user, WorkingDir: workdir,
		AttachStdout: true, AttachStderr: true,
		Env: []string{"HOME=/tmp", "LANG=C.UTF-8", "OPENPLEX_SITE=" + d.Name, "OPENPLEX_CRON=1"},
	})
	if err != nil {
		return -1, fmt.Errorf("start task: %w", err)
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, fmt.Errorf("attach task: %w", err)
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = stdcopy.StdCopy(w, w, att.Reader)
	}()
	select {
	case <-copied:
	case <-ctx.Done():
		kctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if insp, err := o.cli.ContainerExecInspect(kctx, ex.ID); err == nil && insp.Running && insp.Pid > 0 {
			killTree(insp.Pid)
		}
		cancel()
		att.Close()
		<-copied
		return -1, ctx.Err()
	}
	att.Close()
	insp, err := o.cli.ContainerExecInspect(context.Background(), ex.ID)
	if err != nil {
		return -1, fmt.Errorf("inspect task: %w", err)
	}
	return insp.ExitCode, nil
}
