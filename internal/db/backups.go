package db

import (
	"context"
	"database/sql"
	"time"
)

// BackupSettings is the single backup configuration row.
type BackupSettings struct {
	Target          string     `json:"target"` // local | s3
	LocalPath       string     `json:"local_path"`
	S3Endpoint      string     `json:"s3_endpoint"`
	S3Bucket        string     `json:"s3_bucket"`
	S3Prefix        string     `json:"s3_prefix"`
	S3Region        string     `json:"s3_region"`
	S3AccessKey     string     `json:"s3_access_key"`
	S3SecretEnc     string     `json:"-"`
	RepoPasswordEnc string     `json:"-"`
	ScheduleEnabled bool       `json:"schedule_enabled"`
	ScheduleTime    string     `json:"schedule_time"` // HH:MM, server local time
	KeepDaily       int        `json:"keep_daily"`
	KeepWeekly      int        `json:"keep_weekly"`
	KeepMonthly     int        `json:"keep_monthly"`
	LastScheduledAt *time.Time `json:"last_scheduled_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GetBackupSettings returns the settings, creating defaults on first use.
func (db *DB) GetBackupSettings(ctx context.Context) (*BackupSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO backup_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	var s BackupSettings
	var last sql.NullTime
	err := db.sql.QueryRowContext(ctx, `SELECT target, local_path, s3_endpoint, s3_bucket, s3_prefix, s3_region, s3_access_key, s3_secret_enc,
		repo_password_enc, schedule_enabled, schedule_time, keep_daily, keep_weekly, keep_monthly, last_scheduled_at, updated_at
		FROM backup_settings WHERE id = 1`).Scan(&s.Target, &s.LocalPath, &s.S3Endpoint, &s.S3Bucket, &s.S3Prefix, &s.S3Region, &s.S3AccessKey,
		&s.S3SecretEnc, &s.RepoPasswordEnc, &s.ScheduleEnabled, &s.ScheduleTime, &s.KeepDaily, &s.KeepWeekly, &s.KeepMonthly, &last, &s.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	s.LastScheduledAt = ptrTime(last)
	return &s, nil
}

// SaveBackupSettings persists s (all fields).
func (db *DB) SaveBackupSettings(ctx context.Context, s *BackupSettings) error {
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE backup_settings SET target=?, local_path=?, s3_endpoint=?, s3_bucket=?, s3_prefix=?, s3_region=?,
		s3_access_key=?, s3_secret_enc=?, repo_password_enc=?, schedule_enabled=?, schedule_time=?, keep_daily=?, keep_weekly=?, keep_monthly=?,
		last_scheduled_at=?, updated_at=? WHERE id = 1`,
		s.Target, s.LocalPath, s.S3Endpoint, s.S3Bucket, s.S3Prefix, s.S3Region, s.S3AccessKey, s.S3SecretEnc, s.RepoPasswordEnc,
		s.ScheduleEnabled, s.ScheduleTime, s.KeepDaily, s.KeepWeekly, s.KeepMonthly, s.LastScheduledAt, s.UpdatedAt)
	return err
}

// BackupRun is one backup or restore execution.
type BackupRun struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`    // backup | restore
	Trigger    string     `json:"trigger"` // manual | scheduled
	Scope      string     `json:"scope"`   // all | site:<id> | db:<id> | panel
	Status     string     `json:"status"`  // running | success | error
	Message    string     `json:"message"`
	BytesAdded int64      `json:"bytes_added"`
	UserID     *int64     `json:"user_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// CreateBackupRun inserts a running run.
func (db *DB) CreateBackupRun(ctx context.Context, r *BackupRun) error {
	r.StartedAt, r.Status = now(), "running"
	res, err := db.sql.ExecContext(ctx, `INSERT INTO backup_runs (kind, trigger, scope, status, user_id, started_at) VALUES (?,?,?,?,?,?)`,
		r.Kind, r.Trigger, r.Scope, r.Status, r.UserID, r.StartedAt)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// FinishBackupRun records the outcome.
func (db *DB) FinishBackupRun(ctx context.Context, id int64, status, message string, bytesAdded int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE backup_runs SET status=?, message=?, bytes_added=?, finished_at=? WHERE id=?`,
		status, message, bytesAdded, now(), id)
	return err
}

// FailStaleBackupRuns marks runs left "running" by a crash/restart as failed.
func (db *DB) FailStaleBackupRuns(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE backup_runs SET status='error', message='interrupted (openplexd restarted)', finished_at=? WHERE status='running'`, now())
	return err
}

// ListBackupRuns returns the newest runs; scopes filters by scope (nil = all).
func (db *DB) ListBackupRuns(ctx context.Context, scopes []string, limit int) ([]*BackupRun, error) {
	q := `SELECT id, kind, trigger, scope, status, message, bytes_added, user_id, started_at, finished_at FROM backup_runs`
	var args []any
	if scopes != nil {
		if len(scopes) == 0 {
			return []*BackupRun{}, nil
		}
		q += ` WHERE scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*BackupRun{}
	for rows.Next() {
		var r BackupRun
		var uid sql.NullInt64
		var fin sql.NullTime
		if err := rows.Scan(&r.ID, &r.Kind, &r.Trigger, &r.Scope, &r.Status, &r.Message, &r.BytesAdded, &uid, &r.StartedAt, &fin); err != nil {
			return nil, err
		}
		r.UserID, r.FinishedAt = ptrInt(uid), ptrTime(fin)
		out = append(out, &r)
	}
	return out, rows.Err()
}
