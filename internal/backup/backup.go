// Package backup implements scheduled, incremental, encrypted backups and
// one-click restores using restic (run in throwaway containers). Each website,
// database and the panel itself is a separate snapshot identified by tags, so
// any single item can be restored on its own.
package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/mount"

	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/secrets"
)

// ResticImage runs every repository operation.
const ResticImage = "restic/restic:0.18.1"

// ErrBusy is returned when another backup/restore is running.
var ErrBusy = errors.New("another backup or restore is in progress")

// In-container mount points.
const (
	mSites   = "/data/sites"
	mStaging = "/data/staging"
	mRedis   = "/data/redis"
	mRepo    = "/repo"
	mCache   = "/cache"
)

// Manager coordinates backups and restores.
type Manager struct {
	cfg  *config.Config
	db   *db.DB
	orch *docker.Orchestrator
	box  *secrets.Box
	log  *slog.Logger
	mu   sync.Mutex // one operation at a time
}

// New creates a Manager.
func New(cfg *config.Config, store *db.DB, orch *docker.Orchestrator, box *secrets.Box, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, db: store, orch: orch, box: box, log: log}
}

func (m *Manager) stagingDir(parts ...string) string {
	return filepath.Join(append([]string{m.cfg.BackupsDir(), "staging"}, parts...)...)
}

// Settings returns the settings, generating the repository password once.
func (m *Manager) Settings(ctx context.Context) (*db.BackupSettings, error) {
	s, err := m.db.GetBackupSettings(ctx)
	if err != nil {
		return nil, err
	}
	if s.RepoPasswordEnc == "" {
		enc, err := m.box.Seal(secrets.Password(40))
		if err != nil {
			return nil, err
		}
		s.RepoPasswordEnc = enc
		if err := m.db.SaveBackupSettings(ctx, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// RecoveryKey returns the repository password (needed to restore on a new server).
func (m *Manager) RecoveryKey(ctx context.Context) (string, error) {
	s, err := m.Settings(ctx)
	if err != nil {
		return "", err
	}
	return m.box.Open(s.RepoPasswordEnc)
}

// LocalRepoPath is where a local repository lives on the host.
func (m *Manager) LocalRepoPath(s *db.BackupSettings) string {
	if s.LocalPath != "" {
		return s.LocalPath
	}
	return filepath.Join(m.cfg.BackupsDir(), "repo")
}

// Location describes the repository for humans.
func (m *Manager) Location(s *db.BackupSettings) string {
	if s.Target == "s3" {
		return fmt.Sprintf("s3: %s/%s/%s", strings.TrimRight(s.S3Endpoint, "/"), s.S3Bucket, s.S3Prefix)
	}
	return m.LocalRepoPath(s)
}

var (
	timeRe   = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._\-/]{0,200}$`)
	snapRe   = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
)

// Validate checks settings before they are saved.
func (m *Manager) Validate(s *db.BackupSettings) error {
	switch s.Target {
	case "local":
		if s.LocalPath != "" {
			c := filepath.Clean(s.LocalPath)
			if !filepath.IsAbs(c) || c != s.LocalPath || c == "/" || strings.HasPrefix(c+"/", m.cfg.SitesDir()+"/") {
				return errors.New("local_path must be a clean absolute directory outside the sites directory")
			}
		}
	case "s3":
		u, err := url.Parse(s.S3Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" && u.Path != "/" {
			return errors.New("s3_endpoint must look like https://s3.example.com")
		}
		if !bucketRe.MatchString(s.S3Bucket) {
			return errors.New("invalid s3_bucket")
		}
		if !prefixRe.MatchString(s.S3Prefix) || strings.Contains(s.S3Prefix, "..") {
			return errors.New("invalid s3_prefix")
		}
		if s.S3AccessKey == "" || s.S3SecretEnc == "" {
			return errors.New("S3 access key and secret are required")
		}
	default:
		return errors.New("target must be local or s3")
	}
	if !timeRe.MatchString(s.ScheduleTime) {
		return errors.New("schedule_time must be HH:MM")
	}
	for _, k := range []int{s.KeepDaily, s.KeepWeekly, s.KeepMonthly} {
		if k < 0 || k > 1000 {
			return errors.New("retention values must be between 0 and 1000")
		}
	}
	if s.KeepDaily+s.KeepWeekly+s.KeepMonthly == 0 {
		return errors.New("keep at least one backup (daily, weekly or monthly)")
	}
	return nil
}

// ---------- restic plumbing ----------

func (m *Manager) env(s *db.BackupSettings) ([]string, error) {
	pw, err := m.box.Open(s.RepoPasswordEnc)
	if err != nil {
		return nil, err
	}
	env := []string{"RESTIC_PASSWORD=" + pw}
	if s.Target == "s3" {
		secret, err := m.box.Open(s.S3SecretEnc)
		if err != nil {
			return nil, err
		}
		repo := "s3:" + strings.TrimRight(s.S3Endpoint, "/") + "/" + s.S3Bucket
		if p := strings.Trim(s.S3Prefix, "/"); p != "" {
			repo += "/" + p
		}
		env = append(env, "RESTIC_REPOSITORY="+repo, "AWS_ACCESS_KEY_ID="+s.S3AccessKey, "AWS_SECRET_ACCESS_KEY="+secret)
		if s.S3Region != "" {
			env = append(env, "AWS_DEFAULT_REGION="+s.S3Region)
		}
	} else {
		env = append(env, "RESTIC_REPOSITORY="+mRepo)
	}
	return env, nil
}

func (m *Manager) mounts(s *db.BackupSettings, writable bool) ([]mount.Mount, error) {
	for _, d := range []string{m.stagingDir(), filepath.Join(m.cfg.BackupsDir(), "cache"), filepath.Join(m.cfg.DataDir, "databases", "redis")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	ms := []mount.Mount{
		{Type: mount.TypeBind, Source: m.cfg.SitesDir(), Target: mSites, ReadOnly: !writable},
		{Type: mount.TypeBind, Source: m.stagingDir(), Target: mStaging},
		{Type: mount.TypeBind, Source: filepath.Join(m.cfg.DataDir, "databases", "redis"), Target: mRedis, ReadOnly: !writable},
		{Type: mount.TypeBind, Source: filepath.Join(m.cfg.BackupsDir(), "cache"), Target: mCache},
	}
	if s.Target == "local" {
		repo := m.LocalRepoPath(s)
		if err := os.MkdirAll(repo, 0o700); err != nil {
			return nil, err
		}
		ms = append(ms, mount.Mount{Type: mount.TypeBind, Source: repo, Target: mRepo})
	}
	return ms, nil
}

// restic runs one restic command and returns stdout.
func (m *Manager) restic(ctx context.Context, s *db.BackupSettings, writable bool, args ...string) (string, error) {
	env, err := m.env(s)
	if err != nil {
		return "", err
	}
	ms, err := m.mounts(s, writable)
	if err != nil {
		return "", err
	}
	cmd := append([]string{"--cache-dir", mCache}, args...)
	res, err := m.orch.RunJob(ctx, docker.Job{Image: ResticImage, Cmd: cmd, Env: env, Mounts: ms})
	if err != nil {
		return "", err
	}
	// Exit 3 on backup: some files could not be read — the snapshot exists.
	if res.Exit != 0 && !(res.Exit == 3 && len(args) > 0 && args[0] == "backup") {
		return res.Stdout, fmt.Errorf("restic %s: exit %d: %s", args[0], res.Exit, tail(res.Stderr, 600))
	}
	return res.Stdout, nil
}

// EnsureRepository initialises the repository if it does not exist yet.
func (m *Manager) EnsureRepository(ctx context.Context, s *db.BackupSettings) error {
	if _, err := m.restic(ctx, s, false, "cat", "config"); err == nil {
		return nil
	}
	if _, err := m.restic(ctx, s, false, "init"); err != nil {
		return fmt.Errorf("initialise repository at %s: %w", m.Location(s), err)
	}
	m.log.Info("backup repository initialised", "location", m.Location(s))
	return nil
}

type backupSummary struct {
	MessageType string `json:"message_type"`
	DataAdded   int64  `json:"data_added"`
	SnapshotID  string `json:"snapshot_id"`
}

func (m *Manager) snapshot(ctx context.Context, s *db.BackupSettings, p string, tags ...string) (int64, error) {
	args := []string{"backup", "--json", "--host", "openplex", "--tag", strings.Join(append([]string{"opx"}, tags...), ","), p}
	out, err := m.restic(ctx, s, false, args...)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		var sum backupSummary
		if json.Unmarshal([]byte(line), &sum) == nil && sum.MessageType == "summary" {
			return sum.DataAdded, nil
		}
	}
	return 0, nil
}

// Snapshot is one restore point.
type Snapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Paths   []string  `json:"paths"`
	Summary *struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
		DataAdded           int64 `json:"data_added"`
		TotalFilesProcessed int64 `json:"total_files_processed"`
	} `json:"summary,omitempty"`
}

// Snapshots lists snapshots carrying tag (newest first).
func (m *Manager) Snapshots(ctx context.Context, tag string) ([]Snapshot, error) {
	s, err := m.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out, err := m.restic(ctx, s, false, "snapshots", "--no-lock", "--json", "--tag", "opx,"+tag)
	if err != nil {
		if strings.Contains(err.Error(), "repository does not exist") || strings.Contains(err.Error(), "exit 10") {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal([]byte(out), &snaps); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	slices.SortFunc(snaps, func(a, b Snapshot) int { return b.Time.Compare(a.Time) })
	return snaps, nil
}

// ---------- backup ----------

// Scope tags.
func SiteTag(id int64) string     { return "site:" + strconv.FormatInt(id, 10) }
func DatabaseTag(id int64) string { return "db:" + strconv.FormatInt(id, 10) }

// Backup runs a backup of scope ("all", "panel", "site:<id>", "db:<id>").
func (m *Manager) Backup(ctx context.Context, scope, trigger string, userID *int64) (*db.BackupRun, error) {
	if !m.mu.TryLock() {
		return nil, ErrBusy
	}
	run := &db.BackupRun{Kind: "backup", Trigger: trigger, Scope: scope, UserID: userID}
	if err := m.db.CreateBackupRun(ctx, run); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	go func() {
		defer m.mu.Unlock()
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Hour)
		defer cancel()
		added, msg, err := m.doBackup(bctx, scope, trigger == "scheduled")
		status := "success"
		if err != nil {
			status = "error"
			msg = strings.TrimSpace(msg + " " + err.Error())
			m.log.Error("backup failed", "scope", scope, "err", err)
		} else {
			m.log.Info("backup finished", "scope", scope, "added", added, "summary", msg)
		}
		_ = m.db.FinishBackupRun(bctx, run.ID, status, msg, added)
	}()
	return run, nil
}

func (m *Manager) doBackup(ctx context.Context, scope string, prune bool) (int64, string, error) {
	s, err := m.Settings(ctx)
	if err != nil {
		return 0, "", err
	}
	if err := m.EnsureRepository(ctx, s); err != nil {
		return 0, "", err
	}
	var sites []*db.Domain
	var dbs []*db.Database
	panel := scope == "all" || scope == "panel"
	switch {
	case scope == "all":
		all, err := m.db.ListDomains(ctx, db.DomainFilter{})
		if err != nil {
			return 0, "", err
		}
		for _, d := range all {
			if d.Kind != db.KindAlias {
				sites = append(sites, d)
			}
		}
		if dbs, err = m.db.ListDatabases(ctx, nil); err != nil {
			return 0, "", err
		}
	case strings.HasPrefix(scope, "site:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "site:"), 10, 64)
		d, err := m.db.GetDomain(ctx, id)
		if err != nil {
			return 0, "", err
		}
		sites = append(sites, d)
	case strings.HasPrefix(scope, "db:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "db:"), 10, 64)
		d, err := m.db.GetDatabase(ctx, id)
		if err != nil {
			return 0, "", err
		}
		dbs = append(dbs, d)
	case scope == "panel":
	default:
		return 0, "", fmt.Errorf("unknown scope %q", scope)
	}

	var total int64
	var errs []error
	done := 0
	for _, d := range sites {
		n, err := m.snapshot(ctx, s, path.Join(mSites, d.Name), SiteTag(d.ID), "site-name:"+d.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("site %s: %w", d.Name, err))
			continue
		}
		total += n
		done++
	}
	for _, d := range dbs {
		if d.Status != db.StatusRunning {
			continue
		}
		n, err := m.backupDatabase(ctx, s, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("database %s: %w", d.Name, err))
			continue
		}
		total += n
		done++
	}
	if panel {
		n, err := m.backupPanel(ctx, s)
		if err != nil {
			errs = append(errs, fmt.Errorf("panel: %w", err))
		} else {
			total += n
			done++
		}
	}
	if prune && len(errs) == 0 {
		if err := m.Prune(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("retention: %w", err))
		}
	}
	msg := fmt.Sprintf("%d item(s) backed up", done)
	return total, msg, errors.Join(errs...)
}

func (m *Manager) backupDatabase(ctx context.Context, s *db.BackupSettings, d *db.Database) (int64, error) {
	tags := []string{DatabaseTag(d.ID), "db-name:" + d.Name, "engine:" + d.Engine}
	if d.Engine == db.EngineRedis {
		id, err := m.orch.RedisContainerID(ctx, d.ID)
		if err != nil || id == "" {
			return 0, fmt.Errorf("redis container not running: %v", err)
		}
		if _, err := m.orch.ExecToWriter(ctx, id, []string{"sh", "-c", `redis-cli --no-auth-warning -a "$REDIS_PASSWORD" SAVE`}, nil, io.Discard); err != nil {
			return 0, err
		}
		return m.snapshot(ctx, s, path.Join(mRedis, strconv.FormatInt(d.ID, 10)), tags...)
	}
	dir := m.stagingDir(fmt.Sprintf("db-%d", d.ID))
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	cid, err := m.orch.EngineContainer(ctx, d.Engine)
	if err != nil {
		return 0, err
	}
	env, err := m.orch.RootEnv(d.Engine)
	if err != nil {
		return 0, err
	}
	var file string
	var cmd []string
	switch d.Engine {
	case db.EngineMariaDB:
		file = "dump.sql"
		cmd = []string{"mariadb-dump", "-uroot", "--single-transaction", "--quick", "--routines", "--triggers", "--events", "--hex-blob", d.Name}
	case db.EnginePostgres:
		file = "dump.pgc"
		cmd = []string{"pg_dump", "-U", "postgres", "-Fc", d.Name}
	}
	f, err := os.OpenFile(filepath.Join(dir, file), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	_, err = m.orch.ExecToWriter(ctx, cid, cmd, env, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("dump: %w", err)
	}
	return m.snapshot(ctx, s, path.Join(mStaging, filepath.Base(dir)), tags...)
}

// backupPanel snapshots the panel state: SQLite DB (consistent copy), signing
// secret, engine root passwords and TLS certificates.
func (m *Manager) backupPanel(ctx context.Context, s *db.BackupSettings) (int64, error) {
	dir := m.stagingDir("panel")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	if err := m.db.Snapshot(ctx, filepath.Join(dir, "openplex.db")); err != nil {
		return 0, err
	}
	copies := map[string]string{
		filepath.Join(m.cfg.DataDir, "secret.key"): "secret.key",
		filepath.Join(m.cfg.DataDir, "secrets"):    "secrets",
		filepath.Join(m.cfg.CaddyDir(), "data"):    "caddy-data",
		"/etc/openplex/openplex.env":               "openplex.env",
	}
	for src, dst := range copies {
		if err := copyTree(src, filepath.Join(dir, dst)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("copy %s: %w", src, err)
		}
	}
	return m.snapshot(ctx, s, path.Join(mStaging, "panel"), "panel")
}

// Prune applies the retention policy per item and frees unused data.
func (m *Manager) Prune(ctx context.Context, s *db.BackupSettings) error {
	_, err := m.restic(ctx, s, false, "forget", "--tag", "opx", "--group-by", "host,tags",
		"--keep-daily", strconv.Itoa(s.KeepDaily), "--keep-weekly", strconv.Itoa(s.KeepWeekly),
		"--keep-monthly", strconv.Itoa(s.KeepMonthly), "--prune")
	return err
}

// ---------- restore ----------

// Restore restores a site or database to snapshot snapID. The snapshot must
// carry the item's own tag, so one tenant can never restore another's data.
func (m *Manager) Restore(ctx context.Context, scope, snapID string, userID *int64) (*db.BackupRun, error) {
	if !snapRe.MatchString(snapID) {
		return nil, errors.New("invalid snapshot id")
	}
	if !m.mu.TryLock() {
		return nil, ErrBusy
	}
	run := &db.BackupRun{Kind: "restore", Trigger: "manual", Scope: scope, UserID: userID}
	if err := m.db.CreateBackupRun(ctx, run); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	go func() {
		defer m.mu.Unlock()
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Hour)
		defer cancel()
		err := m.doRestore(rctx, scope, snapID)
		status, msg := "success", "restored snapshot "+snapID[:8]
		if err != nil {
			status, msg = "error", err.Error()
			m.log.Error("restore failed", "scope", scope, "snapshot", snapID, "err", err)
		} else {
			m.log.Info("restore finished", "scope", scope, "snapshot", snapID)
		}
		_ = m.db.FinishBackupRun(rctx, run.ID, status, msg, 0)
	}()
	return run, nil
}

func (m *Manager) doRestore(ctx context.Context, scope, snapID string) error {
	s, err := m.Settings(ctx)
	if err != nil {
		return err
	}
	snaps, err := m.Snapshots(ctx, scope)
	if err != nil {
		return err
	}
	var snap *Snapshot
	for i := range snaps {
		if snaps[i].ID == snapID || snaps[i].ShortID == snapID || strings.HasPrefix(snaps[i].ID, snapID) {
			snap = &snaps[i]
			break
		}
	}
	if snap == nil || !slices.Contains(snap.Tags, scope) {
		return errors.New("snapshot not found for this item")
	}
	switch {
	case strings.HasPrefix(scope, "site:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "site:"), 10, 64)
		d, err := m.db.GetDomain(ctx, id)
		if err != nil {
			return err
		}
		return m.restoreSite(ctx, s, snap, d)
	case strings.HasPrefix(scope, "db:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "db:"), 10, 64)
		d, err := m.db.GetDatabase(ctx, id)
		if err != nil {
			return err
		}
		return m.restoreDatabase(ctx, s, snap, d)
	}
	return fmt.Errorf("cannot restore scope %q", scope)
}

func (m *Manager) restoreSite(ctx context.Context, s *db.BackupSettings, snap *Snapshot, d *db.Domain) error {
	src := path.Join(mSites, d.Name)
	if !slices.Contains(snap.Paths, src) {
		return fmt.Errorf("snapshot does not contain %s", src)
	}
	// --delete makes the site directory exactly match the snapshot.
	if _, err := m.restic(ctx, s, true, "restore", snap.ID+":"+src, "--target", src, "--delete"); err != nil {
		return err
	}
	uid := docker.SiteUID(d.ID)
	if err := chownTree(filepath.Join(m.cfg.SitesDir(), d.Name), uid, uid); err != nil {
		return err
	}
	if d.Status == db.StatusStopped || d.Status == db.StatusDisabled {
		return nil
	}
	return m.orch.DeploySite(ctx, d)
}

func (m *Manager) restoreDatabase(ctx context.Context, s *db.BackupSettings, snap *Snapshot, d *db.Database) error {
	if d.Engine == db.EngineRedis {
		src := path.Join(mRedis, strconv.FormatInt(d.ID, 10))
		pw, err := m.box.Open(d.PasswordEnc)
		if err != nil {
			return err
		}
		if err := m.orch.StopRedis(ctx, d.ID); err != nil {
			return err
		}
		_, rerr := m.restic(ctx, s, true, "restore", snap.ID+":"+src, "--target", src, "--delete")
		if rerr == nil {
			rerr = chownTree(m.orch.RedisDataDir(d.ID), 999, 1000)
		}
		// Bring Redis back even if the restore failed.
		if err := m.orch.RestartRedis(ctx, d, pw); err != nil {
			return errors.Join(rerr, err)
		}
		return rerr
	}
	src := path.Join(mStaging, fmt.Sprintf("db-%d", d.ID))
	target := fmt.Sprintf("restore-db-%d", d.ID)
	host := m.stagingDir(target)
	_ = os.RemoveAll(host)
	defer os.RemoveAll(host)
	if _, err := m.restic(ctx, s, false, "restore", snap.ID+":"+src, "--target", path.Join(mStaging, target)); err != nil {
		return err
	}
	cid, err := m.orch.EngineContainer(ctx, d.Engine)
	if err != nil {
		return err
	}
	env, err := m.orch.RootEnv(d.Engine)
	if err != nil {
		return err
	}
	switch d.Engine {
	case db.EngineMariaDB:
		f, err := os.Open(filepath.Join(host, "dump.sql"))
		if err != nil {
			return err
		}
		defer f.Close()
		if err := m.orch.ResetMariaDB(ctx, d); err != nil {
			return err
		}
		return m.orch.ExecFromReader(ctx, cid, []string{"mariadb", "-uroot", d.Name}, env, f)
	case db.EnginePostgres:
		f, err := os.Open(filepath.Join(host, "dump.pgc"))
		if err != nil {
			return err
		}
		defer f.Close()
		return m.orch.ExecFromReader(ctx, cid, []string{"pg_restore", "-U", "postgres", "--clean", "--if-exists", "--no-owner",
			"--role=" + d.Username, "--exit-on-error", "-d", d.Name}, env, f)
	}
	return fmt.Errorf("unknown engine %q", d.Engine)
}

// ---------- scheduler ----------

// Run executes scheduled backups until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	if err := m.db.FailStaleBackupRuns(ctx); err != nil {
		m.log.Warn("mark stale backup runs", "err", err)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.tick(ctx, now)
		}
	}
}

func (m *Manager) tick(ctx context.Context, now time.Time) {
	s, err := m.Settings(ctx)
	if err != nil || !s.ScheduleEnabled {
		return
	}
	due := Due(s.ScheduleTime, s.LastScheduledAt, now)
	if !due {
		return
	}
	if _, err := m.Backup(ctx, "all", "scheduled", nil); err != nil {
		if !errors.Is(err, ErrBusy) {
			m.log.Error("scheduled backup", "err", err)
		}
		return
	}
	t := now.UTC()
	s.LastScheduledAt = &t
	_ = m.db.SaveBackupSettings(ctx, s)
}

// Due reports whether today's HH:MM (local time) has passed and no scheduled
// run happened since then.
func Due(hhmm string, last *time.Time, now time.Time) bool {
	tm, err := time.ParseInLocation("15:04", hhmm, now.Location())
	if err != nil {
		return false
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), tm.Hour(), tm.Minute(), 0, 0, now.Location())
	if now.Before(slot) {
		return false
	}
	return last == nil || last.Before(slot)
}

// ---------- helpers ----------

func copyTree(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return copyFile(src, dst, st.Mode())
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, out, info.Mode())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()&0o700|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func chownTree(root string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return string(bytes.ReplaceAll([]byte(s), []byte("\n"), []byte(" | ")))
}
