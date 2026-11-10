package db

import (
	"context"
	"database/sql"
	"time"
)

// Cron run statuses.
const (
	CronRunning = "running"
	CronSuccess = "success"
	CronFailed  = "failed"
	CronTimeout = "timeout"
	CronSkipped = "skipped"
)

// CronJob is a scheduled command run inside a website's environment.
type CronJob struct {
	ID         int64      `json:"id"`
	DomainID   int64      `json:"domain_id"`
	Name       string     `json:"name"`
	Schedule   string     `json:"schedule"`
	Command    string     `json:"command"`
	Enabled    bool       `json:"enabled"`
	TimeoutSec int        `json:"timeout_sec"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus string     `json:"last_status"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CronRun is one execution of a job.
type CronRun struct {
	ID         int64      `json:"id"`
	JobID      int64      `json:"job_id"`
	Trigger    string     `json:"trigger"` // schedule | manual
	Status     string     `json:"status"`
	ExitCode   *int64     `json:"exit_code"`
	Output     string     `json:"output"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

const cronJobCols = `id, domain_id, name, schedule, command, enabled, timeout_sec, next_run_at, last_run_at, last_status, created_at, updated_at`

func scanCronJob(row interface{ Scan(...any) error }) (*CronJob, error) {
	var j CronJob
	var next, last sql.NullTime
	if err := row.Scan(&j.ID, &j.DomainID, &j.Name, &j.Schedule, &j.Command, &j.Enabled, &j.TimeoutSec,
		&next, &last, &j.LastStatus, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	j.NextRunAt, j.LastRunAt = ptrTime(next), ptrTime(last)
	return &j, nil
}

func (db *DB) queryCronJobs(ctx context.Context, where string, args ...any) ([]*CronJob, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+cronJobCols+` FROM cron_jobs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CronJob{}
	for rows.Next() {
		j, err := scanCronJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CreateCronJob inserts j.
func (db *DB) CreateCronJob(ctx context.Context, j *CronJob) error {
	j.CreatedAt, j.UpdatedAt = now(), now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO cron_jobs (domain_id, name, schedule, command, enabled, timeout_sec, next_run_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, j.DomainID, j.Name, j.Schedule, j.Command, j.Enabled, j.TimeoutSec, j.NextRunAt, j.CreatedAt, j.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	j.ID, err = res.LastInsertId()
	return err
}

// GetCronJob returns a job by ID.
func (db *DB) GetCronJob(ctx context.Context, id int64) (*CronJob, error) {
	return scanCronJob(db.sql.QueryRowContext(ctx, `SELECT `+cronJobCols+` FROM cron_jobs WHERE id = ?`, id))
}

// ListCronJobs returns a website's jobs.
func (db *DB) ListCronJobs(ctx context.Context, domainID int64) ([]*CronJob, error) {
	return db.queryCronJobs(ctx, `WHERE domain_id = ? ORDER BY id`, domainID)
}

// ListDueCronJobs returns enabled jobs whose next run is at or before t.
func (db *DB) ListDueCronJobs(ctx context.Context, t time.Time) ([]*CronJob, error) {
	return db.queryCronJobs(ctx, `WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ? ORDER BY next_run_at`, t.UTC())
}

// CountCronJobs counts a website's jobs.
func (db *DB) CountCronJobs(ctx context.Context, domainID int64) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM cron_jobs WHERE domain_id = ?`, domainID).Scan(&n)
	return n, err
}

// UpdateCronJob persists the editable fields and the next run time.
func (db *DB) UpdateCronJob(ctx context.Context, j *CronJob) error {
	j.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE cron_jobs SET name=?, schedule=?, command=?, enabled=?, timeout_sec=?, next_run_at=?, updated_at=? WHERE id=?`,
		j.Name, j.Schedule, j.Command, j.Enabled, j.TimeoutSec, j.NextRunAt, j.UpdatedAt, j.ID)
	if err != nil {
		return mapErr(err)
	}
	return expectOne(res)
}

// SetCronNextRun reschedules a job.
func (db *DB) SetCronNextRun(ctx context.Context, id int64, next *time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE cron_jobs SET next_run_at=? WHERE id=?`, next, id)
	return err
}

// DeleteCronJob removes a job and its history.
func (db *DB) DeleteCronJob(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// StartCronRun records a run in progress.
func (db *DB) StartCronRun(ctx context.Context, jobID int64, trigger string) (*CronRun, error) {
	r := &CronRun{JobID: jobID, Trigger: trigger, Status: CronRunning, StartedAt: now()}
	res, err := db.sql.ExecContext(ctx, `INSERT INTO cron_runs (job_id, trigger, status, started_at) VALUES (?,?,?,?)`, jobID, trigger, r.Status, r.StartedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// FinishCronRun stores a run's outcome, updates the job summary and keeps
// only the newest `keep` runs of the job.
func (db *DB) FinishCronRun(ctx context.Context, r *CronRun, keep int) error {
	fin := now()
	r.FinishedAt = &fin
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE cron_runs SET status=?, exit_code=?, output=?, finished_at=? WHERE id=?`,
			r.Status, r.ExitCode, r.Output, fin, r.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cron_jobs SET last_run_at=?, last_status=? WHERE id=?`, r.StartedAt, r.Status, r.JobID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM cron_runs WHERE job_id = ? AND id NOT IN
			(SELECT id FROM cron_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?)`, r.JobID, r.JobID, keep)
		return err
	})
}

// ListCronRuns returns a job's newest runs first.
func (db *DB) ListCronRuns(ctx context.Context, jobID int64, limit int) ([]*CronRun, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, job_id, trigger, status, exit_code, output, started_at, finished_at
		FROM cron_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CronRun{}
	for rows.Next() {
		var r CronRun
		var code sql.NullInt64
		var fin sql.NullTime
		if err := rows.Scan(&r.ID, &r.JobID, &r.Trigger, &r.Status, &code, &r.Output, &r.StartedAt, &fin); err != nil {
			return nil, err
		}
		r.ExitCode, r.FinishedAt = ptrInt(code), ptrTime(fin)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// FailInterruptedCronRuns closes runs left "running" by a daemon restart.
func (db *DB) FailInterruptedCronRuns(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE cron_runs SET status=?, output=output || ?, finished_at=? WHERE status=?`,
		CronFailed, "\n[interrupted: the panel restarted while this task was running]", now(), CronRunning)
	return err
}
