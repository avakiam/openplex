package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/mount"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
)

// Disaster recovery: `openplexd restore-panel` brings the panel state of a
// lost server back from its repository (RestorePanelFiles), then the daemon,
// on its next start, restores every website, database and mail domain from
// the same backup run (RecoverPending).

// Repo is a repository given on the command line of a new server.
type Repo struct {
	Local                           string // host path of a local repository
	S3Endpoint, S3Bucket, S3Prefix  string
	S3Region, S3AccessKey, S3Secret string
	Password                        string // the recovery key
}

// ParseRepo reads "/path/to/repo" or "s3:https://host/bucket[/prefix]".
func ParseRepo(spec string) (Repo, error) {
	if rest, ok := strings.CutPrefix(spec, "s3:"); ok {
		u, err := url.Parse(rest)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return Repo{}, errors.New("an S3 repository looks like s3:https://s3.example.com/bucket/prefix")
		}
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if parts[0] == "" || !bucketRe.MatchString(parts[0]) {
			return Repo{}, errors.New("the S3 repository needs a bucket: s3:https://s3.example.com/bucket/prefix")
		}
		r := Repo{S3Endpoint: u.Scheme + "://" + u.Host, S3Bucket: parts[0]}
		if len(parts) == 2 {
			r.S3Prefix = parts[1]
			if !prefixRe.MatchString(r.S3Prefix) || strings.Contains(r.S3Prefix, "..") {
				return Repo{}, errors.New("invalid S3 prefix")
			}
		}
		return r, nil
	}
	c := filepath.Clean(spec)
	if spec == "" || !filepath.IsAbs(c) || c == "/" {
		return Repo{}, errors.New("give the repository as an absolute directory or s3:https://host/bucket/prefix")
	}
	if _, err := os.Stat(filepath.Join(c, "config")); err != nil {
		return Repo{}, fmt.Errorf("%s is not a backup repository (no config file)", c)
	}
	return Repo{Local: c}, nil
}

func (r Repo) env() []string {
	env := []string{"RESTIC_PASSWORD=" + r.Password}
	if r.Local != "" {
		return append(env, "RESTIC_REPOSITORY="+mRepo)
	}
	repo := "s3:" + strings.TrimRight(r.S3Endpoint, "/") + "/" + r.S3Bucket
	if p := strings.Trim(r.S3Prefix, "/"); p != "" {
		repo += "/" + p
	}
	env = append(env, "RESTIC_REPOSITORY="+repo, "AWS_ACCESS_KEY_ID="+r.S3AccessKey, "AWS_SECRET_ACCESS_KEY="+r.S3Secret)
	if r.S3Region != "" {
		env = append(env, "AWS_DEFAULT_REGION="+r.S3Region)
	}
	return env
}

func (r Repo) run(ctx context.Context, orch *docker.Orchestrator, staging string, args ...string) (string, error) {
	ms := []mount.Mount{{Type: mount.TypeBind, Source: staging, Target: mStaging}}
	if r.Local != "" {
		ms = append(ms, mount.Mount{Type: mount.TypeBind, Source: r.Local, Target: mRepo, ReadOnly: true})
	}
	res, err := orch.RunJob(ctx, docker.Job{Image: ResticImage, Cmd: append([]string{"--no-cache"}, args...), Env: r.env(), Mounts: ms})
	if err != nil {
		return "", err
	}
	if res.Exit != 0 {
		return res.Stdout, fmt.Errorf("restic %s: exit %d: %s", args[0], res.Exit, tail(res.Stderr, 600))
	}
	return res.Stdout, nil
}

// FindPanelSnapshot returns the newest panel snapshot, or the one given.
func FindPanelSnapshot(ctx context.Context, orch *docker.Orchestrator, r Repo, staging, snapID string) (*Snapshot, error) {
	out, err := r.run(ctx, orch, staging, "snapshots", "--no-lock", "--json", "--tag", "opx,panel")
	if err != nil {
		if strings.Contains(err.Error(), "wrong password") {
			return nil, errors.New("the recovery key does not open this repository")
		}
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal([]byte(out), &snaps); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	slices.SortFunc(snaps, func(a, b Snapshot) int { return b.Time.Compare(a.Time) })
	for i := range snaps {
		if snapID == "" || snaps[i].ID == snapID || snaps[i].ShortID == snapID || strings.HasPrefix(snaps[i].ID, snapID) {
			return &snaps[i], nil
		}
	}
	if snapID != "" {
		return nil, fmt.Errorf("no panel backup %s in the repository", snapID)
	}
	return nil, errors.New("the repository has no panel backup")
}

// RestorePanelFiles extracts a panel snapshot into staging/<dir>.
func RestorePanelFiles(ctx context.Context, orch *docker.Orchestrator, r Repo, staging, dir string, snap *Snapshot) error {
	if !snapRe.MatchString(snap.ID) || strings.ContainsAny(dir, "/\\.") {
		return errors.New("invalid restore request")
	}
	_ = os.RemoveAll(filepath.Join(staging, dir))
	// Read-only: the repository may be mounted read-only and in use by nobody.
	_, err := r.run(ctx, orch, staging, "restore", "--no-lock", snap.ID+":"+path.Join(mStaging, "panel"), "--target", path.Join(mStaging, dir))
	return err
}

// Recovery is the note restore-panel leaves for the daemon.
type Recovery struct {
	PanelSnapshot string    `json:"panel_snapshot"`
	PanelTime     time.Time `json:"panel_time"`
}

// RecoveryFile is where the note lives.
func RecoveryFile(dataDir string) string { return filepath.Join(dataDir, "recovery-pending.json") }

// WriteRecovery leaves the note for the next start.
func WriteRecovery(dataDir string, r Recovery) error {
	b, _ := json.Marshal(r)
	return os.WriteFile(RecoveryFile(dataDir), b, 0o600)
}

// RecoverPending restores every website, database, mail domain and the
// webmail's data from the backup run of the restored panel, once. It runs
// after the containers were reconciled from the restored database.
func (m *Manager) RecoverPending(ctx context.Context) {
	b, err := os.ReadFile(RecoveryFile(m.cfg.DataDir))
	if err != nil {
		return
	}
	var rec Recovery
	if err := json.Unmarshal(b, &rec); err != nil {
		m.log.Error("recovery: bad note", "err", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run := &db.BackupRun{Kind: "restore", Trigger: "recovery", Scope: "all"}
	if err := m.db.CreateBackupRun(ctx, run); err != nil {
		m.log.Error("recovery: record run", "err", err)
		return
	}
	m.log.Info("recovery: restoring websites, databases and mail", "panel_snapshot", rec.PanelSnapshot)
	done, errs := m.recoverAll(ctx, rec.PanelTime)
	status, msg := "success", fmt.Sprintf("disaster recovery: %d item(s) restored from the backup of %s", done, rec.PanelTime.UTC().Format(time.RFC3339))
	if len(errs) > 0 {
		status = "error"
		msg += "; " + errors.Join(errs...).Error()
		m.log.Error("recovery finished with errors", "restored", done, "err", errors.Join(errs...))
	} else {
		m.log.Info("recovery finished", "restored", done)
	}
	_ = m.db.FinishBackupRun(context.WithoutCancel(ctx), run.ID, status, tail(msg, 4000), 0)
	if ctx.Err() == nil {
		_ = os.Rename(RecoveryFile(m.cfg.DataDir), filepath.Join(m.cfg.DataDir, "recovery-done.json"))
	}
}

// pick is the snapshot of tag from the same backup run as the panel: the
// newest taken before the panel's (a run backs the panel up last), else the
// newest there is.
func pick(snaps []Snapshot, panelTime time.Time) *Snapshot {
	var newest, before *Snapshot
	for i := range snaps {
		s := &snaps[i]
		if newest == nil || s.Time.After(newest.Time) {
			newest = s
		}
		if !s.Time.After(panelTime.Add(time.Minute)) && (before == nil || s.Time.After(before.Time)) {
			before = s
		}
	}
	if before != nil {
		return before
	}
	return newest
}

func (m *Manager) latest(ctx context.Context, tag string, panelTime time.Time) (*Snapshot, error) {
	snaps, err := m.Snapshots(ctx, tag)
	if err != nil {
		return nil, err
	}
	return pick(snaps, panelTime), nil
}

func (m *Manager) recoverAll(ctx context.Context, panelTime time.Time) (int, []error) {
	var errs []error
	done := 0
	s, err := m.Settings(ctx)
	if err != nil {
		return 0, []error{err}
	}
	domains, err := m.db.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return 0, []error{err}
	}
	for _, d := range domains {
		if d.Kind == db.KindAlias {
			continue
		}
		snap, err := m.latest(ctx, SiteTag(d.ID), panelTime)
		if err == nil && snap != nil {
			err = m.restoreSite(ctx, s, snap, d)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("website %s: %w", d.Name, err))
		} else if snap != nil {
			done++
		}
	}
	dbs, err := m.db.ListDatabases(ctx, nil)
	if err != nil {
		errs = append(errs, err)
	}
	for _, d := range dbs {
		snap, err := m.latest(ctx, DatabaseTag(d.ID), panelTime)
		if err == nil && snap != nil {
			var ready *db.Database
			if ready, err = m.waitDatabase(ctx, d.ID); err == nil {
				err = m.restoreDatabase(ctx, s, snap, ready)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("database %s: %w", d.Name, err))
		} else if snap != nil {
			done++
		}
	}
	mails, err := m.db.ListMailDomains(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	if len(mails) > 0 {
		if err := m.orch.EnsureVolume(ctx, docker.MailDataVolume); err != nil {
			errs = append(errs, err)
		}
	}
	for _, md := range mails {
		snap, err := m.latest(ctx, MailTag(md.DomainID), panelTime)
		if err == nil && snap != nil {
			_, err = m.restoreMail(ctx, snap, md, "")
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("mail of %s: %w", md.Name, err))
		} else if snap != nil {
			done++
		}
	}
	if snap, err := m.latest(ctx, "webmail", panelTime); err != nil {
		errs = append(errs, fmt.Errorf("webmail: %w", err))
	} else if snap != nil {
		if err := m.restoreWebmail(ctx, s, snap); err != nil {
			errs = append(errs, fmt.Errorf("webmail: %w", err))
		} else {
			done++
		}
	}
	return done, errs
}

// waitDatabase waits until the reconciled database server is ready.
func (m *Manager) waitDatabase(ctx context.Context, id int64) (*db.Database, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		d, err := m.db.GetDatabase(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.Status == db.StatusRunning {
			return d, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("not running after 10 minutes (status %s)", d.Status)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// restoreWebmail puts Roundcube's database back. Each file is replaced by a
// rename, so the running webmail sees either the old or the restored one.
func (m *Manager) restoreWebmail(ctx context.Context, s *db.BackupSettings, snap *Snapshot) error {
	if err := m.orch.EnsureVolume(ctx, docker.WebmailDBVolume); err != nil {
		return err
	}
	root, err := m.orch.VolumePath(ctx, docker.WebmailDBVolume)
	if err != nil || root == "" {
		return fmt.Errorf("webmail volume: %v", err)
	}
	stage := "recover-webmail"
	host := m.stagingDir(stage)
	_ = os.RemoveAll(host)
	defer os.RemoveAll(host)
	if _, err := m.restic(ctx, s, false, "restore", snap.ID+":"+mWebmail, "--target", path.Join(mStaging, stage)); err != nil {
		return err
	}
	return filepath.WalkDir(host, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(host, p)
		out := filepath.Join(root, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return mkdirLike(out, info)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := copyLike(p, out+".restore", info); err != nil {
			return err
		}
		return os.Rename(out+".restore", out)
	})
}
