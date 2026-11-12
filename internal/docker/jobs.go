package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"

	"openplex.dev/openplex/internal/db"
)

// Job is a one-shot container (e.g. a restic command).
type Job struct {
	Image  string
	Cmd    []string
	Env    []string
	Mounts []mount.Mount
	// MemoryMB limits the job (0 = 1024).
	MemoryMB int
	// Isolated: no network and only the capabilities to copy files as root
	// (chown, read and write regardless of permissions).
	Isolated bool
	// User runs the job as "uid:gid" instead of the image's user, with no
	// capabilities.
	User string
}

// JobResult holds a finished job's output.
type JobResult struct {
	Stdout string
	Stderr string
	Exit   int
}

// RunJob runs a job to completion and removes its container.
func (o *Orchestrator) RunJob(ctx context.Context, j Job) (*JobResult, error) {
	if err := o.ensureImage(ctx, j.Image); err != nil {
		return nil, err
	}
	mem := j.MemoryMB
	if mem == 0 {
		mem = 1024
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "opx-job-" + hex.EncodeToString(suffix)
	cc := &container.Config{Image: j.Image, Cmd: j.Cmd, Env: j.Env, Labels: managedLabels("job"), User: j.User}
	hc := &container.HostConfig{
		Mounts:      j.Mounts,
		Resources:   container.Resources{Memory: int64(mem) << 20},
		SecurityOpt: []string{"no-new-privileges:true"},
	}
	if j.User != "" {
		hc.CapDrop = []string{"ALL"}
	}
	if j.Isolated {
		hc.NetworkMode = "none"
		hc.CapDrop = []string{"ALL"}
		hc.CapAdd = []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"}
	}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nil, nil, name)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	// Always clean up, even if ctx was cancelled.
	defer o.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})

	waitC, errC := o.cli.ContainerWait(ctx, created.ID, container.WaitConditionNextExit)
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start job: %w", err)
	}
	var exit int64
	select {
	case w := <-waitC:
		exit = w.StatusCode
		if w.Error != nil {
			return nil, errors.New(w.Error.Message)
		}
	case err := <-errC:
		return nil, fmt.Errorf("wait job: %w", err)
	}
	logs, err := o.cli.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return nil, err
	}
	defer logs.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return nil, err
	}
	return &JobResult{Stdout: stdout.String(), Stderr: stderr.String(), Exit: int(exit)}, nil
}

// ExecToWriter runs cmd in a container streaming its stdout into w; stderr is returned.
func (o *Orchestrator) ExecToWriter(ctx context.Context, containerID string, cmd, env []string, w io.Writer) (string, error) {
	ex, err := o.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{Cmd: cmd, Env: env, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return "", err
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer att.Close()
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(w, &stderr, att.Reader); err != nil {
		return stderr.String(), err
	}
	insp, err := o.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return stderr.String(), err
	}
	if insp.ExitCode != 0 {
		return stderr.String(), fmt.Errorf("%s exited %d: %s", cmd[0], insp.ExitCode, strings.TrimSpace(lastN(stderr.String(), 500)))
	}
	return stderr.String(), nil
}

// ExecFromReader runs cmd in a container with r as its stdin.
func (o *Orchestrator) ExecFromReader(ctx context.Context, containerID string, cmd, env []string, r io.Reader) error {
	ex, err := o.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd: cmd, Env: env, AttachStdin: true, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return err
	}
	att, err := o.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer att.Close()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&out, &out, att.Reader)
		done <- err
	}()
	if _, err := io.Copy(att.Conn, r); err != nil {
		return fmt.Errorf("stream stdin: %w", err)
	}
	if err := att.CloseWrite(); err != nil {
		return err
	}
	if err := <-done; err != nil {
		return err
	}
	insp, err := o.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return err
	}
	if insp.ExitCode != 0 {
		return fmt.Errorf("%s exited %d: %s", cmd[0], insp.ExitCode, strings.TrimSpace(lastN(out.String(), 800)))
	}
	return nil
}

// EngineContainer returns the running container ID of a shared engine,
// starting it if necessary.
func (o *Orchestrator) EngineContainer(ctx context.Context, engine string) (string, error) {
	return o.EnsureEngine(ctx, engine)
}

// RootEnv returns the exec environment that authenticates as the engine superuser.
func (o *Orchestrator) RootEnv(engine string) ([]string, error) {
	if engine != db.EngineMariaDB {
		return nil, nil // postgres: local socket trust
	}
	pw, err := o.rootPassword(engine)
	if err != nil {
		return nil, err
	}
	return []string{"MYSQL_PWD=" + pw}, nil
}

// RedisContainerID returns the running Redis container of a database, or "".
func (o *Orchestrator) RedisContainerID(ctx context.Context, id int64) (string, error) {
	c, err := o.findContainer(ctx, RedisContainerName(id))
	if err != nil || c == nil {
		return "", err
	}
	return c.ID, nil
}

// StopRedis stops (and removes) a Redis instance; RestartRedis recreates it.
func (o *Orchestrator) StopRedis(ctx context.Context, id int64) error {
	c, err := o.findContainer(ctx, RedisContainerName(id))
	if err != nil || c == nil {
		return err
	}
	return o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
}

// RestartRedis recreates a Redis instance with its password.
func (o *Orchestrator) RestartRedis(ctx context.Context, d *db.Database, password string) error {
	return o.deployRedis(ctx, d, password)
}

// RedisDataDir is the host directory holding a Redis instance's data.
func (o *Orchestrator) RedisDataDir(id int64) string {
	return o.dbDataDir("redis", fmt.Sprint(id))
}

func lastN(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
