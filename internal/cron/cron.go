// Package cron schedules and runs per-website tasks. Tasks run inside the
// website's own environment as its user (see docker.RunSiteCommand); the
// schedule is evaluated in the server's local time zone.
package cron

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
)

// Limits.
const (
	MaxJobsPerSite = 20
	MaxCommandLen  = 4000
	MaxNameLen     = 100
	MinTimeout     = 10
	MaxTimeout     = 6 * 3600
	DefaultTimeout = 3600
	KeepRuns       = 50
	MaxOutput      = 64 << 10
	maxConcurrent  = 4
	missedGrace    = 2 * time.Minute
)

// ErrBusy is returned when a task is already running.
var ErrBusy = errors.New("this task is already running")

// Runner executes a command in a website's environment.
type Runner interface {
	RunSiteCommand(ctx context.Context, d *db.Domain, command string, w io.Writer) (int, error)
}

var parser = robfig.NewParser(robfig.Minute | robfig.Hour | robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)

// ErrSchedule is the validation error for a bad schedule expression.
var ErrSchedule = errors.New("schedule must be 5 cron fields (minute hour day month weekday) or @hourly, @daily, @weekly, @monthly, @yearly")

// Parse validates a schedule expression.
func Parse(expr string) (robfig.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" || len(expr) > 100 || strings.HasPrefix(expr, "@every") || strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, ErrSchedule
	}
	s, err := parser.Parse(expr)
	if err != nil {
		return nil, ErrSchedule
	}
	return s, nil
}

// NextRuns returns the next n run times of expr after from.
func NextRuns(expr string, from time.Time, n int) ([]time.Time, error) {
	s, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	t := from
	for range n {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out, nil
}

// Next returns the first run time after from, or nil for a disabled job.
func Next(j *db.CronJob, from time.Time) (*time.Time, error) {
	s, err := Parse(j.Schedule)
	if err != nil {
		return nil, err
	}
	if !j.Enabled {
		return nil, nil
	}
	t := s.Next(from.In(time.Local)).UTC()
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}

// Manager runs due tasks and manual "run now" requests.
type Manager struct {
	db   *db.DB
	run  Runner // nil when Docker is unavailable
	log  *slog.Logger
	sem  chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	busy map[int64]bool
	base context.Context
}

// New creates a Manager; runner may be nil.
func New(store *db.DB, runner Runner, log *slog.Logger) *Manager {
	return &Manager{db: store, run: runner, log: log, sem: make(chan struct{}, maxConcurrent), busy: map[int64]bool{}, base: context.Background()}
}

// Running reports whether a job is executing right now.
func (m *Manager) Running(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy[id]
}

// Run drives the scheduler until ctx ends, then waits for running tasks.
func (m *Manager) Run(ctx context.Context) {
	m.base = ctx
	if err := m.db.FailInterruptedCronRuns(ctx); err != nil {
		m.log.Error("cron: close interrupted runs", "err", err)
	}
	for {
		now := time.Now()
		wait := now.Truncate(time.Minute).Add(time.Minute + time.Second).Sub(now)
		select {
		case <-ctx.Done():
			m.wg.Wait()
			return
		case <-time.After(wait):
			m.tick(ctx, time.Now())
		}
	}
}

func (m *Manager) tick(ctx context.Context, now time.Time) {
	jobs, err := m.db.ListDueCronJobs(ctx, now)
	if err != nil {
		m.log.Error("cron: list due tasks", "err", err)
		return
	}
	for _, j := range jobs {
		missed := j.NextRunAt != nil && now.Sub(*j.NextRunAt) > missedGrace
		next, err := Next(j, now)
		if err != nil {
			next = nil // unparsable (should not happen): park it
		}
		if err := m.db.SetCronNextRun(ctx, j.ID, next); err != nil {
			m.log.Error("cron: reschedule", "job", j.ID, "err", err)
			continue
		}
		if missed { // the panel was down at that time; cron semantics: skip
			m.log.Info("cron: skipped a missed run", "job", j.ID, "due", j.NextRunAt)
			continue
		}
		_ = m.start(j, "schedule")
	}
}

// RunNow starts a job immediately (in the background).
func (m *Manager) RunNow(j *db.CronJob) error { return m.start(j, "manual") }

func (m *Manager) start(j *db.CronJob, trigger string) error {
	m.mu.Lock()
	if m.busy[j.ID] {
		m.mu.Unlock()
		if trigger == "schedule" {
			m.record(j, trigger, db.CronSkipped, nil, "Skipped: the previous run was still in progress.")
		}
		return ErrBusy
	}
	m.busy[j.ID] = true
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.busy, j.ID)
			m.mu.Unlock()
		}()
		select {
		case m.sem <- struct{}{}:
		case <-m.base.Done():
			return
		}
		defer func() { <-m.sem }()
		m.execute(j, trigger)
	}()
	return nil
}

// record stores a run that never executed.
func (m *Manager) record(j *db.CronJob, trigger, status string, code *int64, output string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := m.db.StartCronRun(ctx, j.ID, trigger)
	if err != nil {
		return
	}
	r.Status, r.ExitCode, r.Output = status, code, output
	_ = m.db.FinishCronRun(ctx, r, KeepRuns)
}

func (m *Manager) execute(j *db.CronJob, trigger string) {
	ctx := m.base
	r, err := m.db.StartCronRun(ctx, j.ID, trigger)
	if err != nil {
		m.log.Error("cron: start run", "job", j.ID, "err", err)
		return
	}
	finish := func(status string, code *int64, output string) {
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.Status, r.ExitCode, r.Output = status, code, output
		if err := m.db.FinishCronRun(fctx, r, KeepRuns); err != nil {
			m.log.Error("cron: finish run", "job", j.ID, "err", err)
		}
	}
	d, err := m.db.GetDomain(ctx, j.DomainID)
	if err != nil {
		finish(db.CronFailed, nil, "The website no longer exists.")
		return
	}
	switch {
	case d.Status == db.StatusDisabled:
		finish(db.CronSkipped, nil, "Skipped: the website is suspended.")
		return
	case d.Status == db.StatusStopped:
		finish(db.CronSkipped, nil, "Skipped: the website is stopped.")
		return
	case m.run == nil:
		finish(db.CronFailed, nil, "Docker is not available on this server.")
		return
	}
	timeout := j.TimeoutSec
	if timeout < MinTimeout || timeout > MaxTimeout {
		timeout = DefaultTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	out := &tail{max: MaxOutput}
	start := time.Now()
	code, err := m.run.RunSiteCommand(tctx, d, j.Command, out)
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case err != nil && errors.Is(tctx.Err(), context.DeadlineExceeded):
		finish(db.CronTimeout, nil, out.String()+fmt.Sprintf("\n[stopped after the %d s time limit]", timeout))
	case err != nil && ctx.Err() != nil:
		finish(db.CronFailed, nil, out.String()+"\n[interrupted: the panel is shutting down]")
	case errors.Is(err, docker.ErrSiteNotRunning):
		finish(db.CronFailed, nil, "The website's container is not running; start or redeploy it first.")
	case err != nil:
		finish(db.CronFailed, nil, out.String()+"\n["+err.Error()+"]")
	default:
		c := int64(code)
		status := db.CronSuccess
		if code != 0 {
			status = db.CronFailed
		}
		finish(status, &c, out.String())
	}
	m.log.Info("cron task finished", "job", j.ID, "domain", d.Name, "trigger", trigger, "exit", code, "took", took)
}

// tail keeps the last max bytes written to it.
type tail struct {
	max       int
	buf       []byte
	truncated bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tail) String() string {
	s := strings.ToValidUTF8(string(t.buf), "�")
	if t.truncated {
		return "[… earlier output truncated …]\n" + s
	}
	return s
}

// TimeZone names the server's local time zone (e.g. "Europe/Madrid"); Go
// reports "Local" for a zone loaded from /etc/localtime.
func TimeZone() string {
	if n := time.Local.String(); n != "Local" {
		return n
	}
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if p, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if i := strings.Index(p, "zoneinfo/"); i >= 0 {
			return p[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	name, off := time.Now().Zone()
	if name != "" {
		return name
	}
	return fmt.Sprintf("UTC%+03d:%02d", off/3600, (off%3600)/60)
}
