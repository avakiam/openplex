package db

import (
	"context"
	"database/sql"
	"time"
)

// Git deploy states.
const (
	GitRunning = "running"
	GitSuccess = "success"
	GitError   = "error"
)

// GitDeploy deploys a website from a branch of a Git repository.
type GitDeploy struct {
	DomainID     int64      `json:"domain_id"`
	RepoURL      string     `json:"repo_url"`
	Branch       string     `json:"branch"`
	Target       string     `json:"target"` // folder inside the website's directory ("" = the directory itself)
	BuildCommand string     `json:"build_command"`
	AutoDeploy   bool       `json:"auto_deploy"` // deploy when the webhook is called
	WebhookToken string     `json:"-"`
	KeyEnc       string     `json:"-"` // sealed private deploy key
	PublicKey    string     `json:"public_key"`
	Status       string     `json:"status"`  // "", running, success, error
	Pending      bool       `json:"pending"` // another deploy was asked for while one ran
	LastCommit   string     `json:"last_commit"`
	LastSubject  string     `json:"last_subject"`
	LastOutput   string     `json:"last_output"`
	LastError    string     `json:"last_error"`
	LastTrigger  string     `json:"last_trigger"` // manual or webhook
	LastDeployAt *time.Time `json:"last_deploy_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const gitCols = `domain_id, repo_url, branch, target, build_command, auto_deploy, webhook_token, key_enc, public_key,
	status, pending, last_commit, last_subject, last_output, last_error, last_trigger, last_deploy_at, created_at, updated_at`

func scanGit(row interface{ Scan(...any) error }) (*GitDeploy, error) {
	var g GitDeploy
	var at sql.NullTime
	if err := row.Scan(&g.DomainID, &g.RepoURL, &g.Branch, &g.Target, &g.BuildCommand, &g.AutoDeploy, &g.WebhookToken,
		&g.KeyEnc, &g.PublicKey, &g.Status, &g.Pending, &g.LastCommit, &g.LastSubject, &g.LastOutput, &g.LastError,
		&g.LastTrigger, &at, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	g.LastDeployAt = ptrTime(at)
	return &g, nil
}

// GetGitDeploy returns the Git deploy of a website (ErrNotFound if none).
func (db *DB) GetGitDeploy(ctx context.Context, domainID int64) (*GitDeploy, error) {
	return scanGit(db.sql.QueryRowContext(ctx, `SELECT `+gitCols+` FROM git_deploys WHERE domain_id = ?`, domainID))
}

// CreateGitDeploy records a new Git deploy (ErrConflict if the website has one).
func (db *DB) CreateGitDeploy(ctx context.Context, g *GitDeploy) error {
	g.CreatedAt, g.UpdatedAt = now(), now()
	_, err := db.sql.ExecContext(ctx, `INSERT INTO git_deploys (domain_id, repo_url, branch, target, build_command, auto_deploy,
		webhook_token, key_enc, public_key, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		g.DomainID, g.RepoURL, g.Branch, g.Target, g.BuildCommand, g.AutoDeploy, g.WebhookToken, g.KeyEnc, g.PublicKey,
		g.CreatedAt, g.UpdatedAt)
	return mapErr(err)
}

// UpdateGitDeploy saves the settings of a Git deploy.
func (db *DB) UpdateGitDeploy(ctx context.Context, g *GitDeploy) error {
	g.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE git_deploys SET repo_url = ?, branch = ?, target = ?, build_command = ?,
		auto_deploy = ?, webhook_token = ?, updated_at = ? WHERE domain_id = ?`,
		g.RepoURL, g.Branch, g.Target, g.BuildCommand, g.AutoDeploy, g.WebhookToken, g.UpdatedAt, g.DomainID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// DeleteGitDeploy forgets the Git deploy of a website (its files stay).
func (db *DB) DeleteGitDeploy(ctx context.Context, domainID int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM git_deploys WHERE domain_id = ?`, domainID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// StartGitDeploy marks a deploy as running. When one is already running it
// is asked to run again once it ends, and started is false.
func (db *DB) StartGitDeploy(ctx context.Context, domainID int64, trigger string) (started bool, err error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE git_deploys SET status = ?, pending = 0, last_trigger = ?, last_error = ''
		WHERE domain_id = ? AND status <> ?`, GitRunning, trigger, domainID, GitRunning)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	res, err = db.sql.ExecContext(ctx, `UPDATE git_deploys SET pending = 1 WHERE domain_id = ?`, domainID)
	if err != nil {
		return false, err
	}
	return false, expectOne(res)
}

// FinishGitDeploy records the outcome of a deploy. It returns whether
// another deploy was asked for meanwhile (it is then marked running again).
func (db *DB) FinishGitDeploy(ctx context.Context, domainID int64, commit, subject, output, failure string) (again bool, err error) {
	status := GitSuccess
	if failure != "" {
		status = GitError
	}
	err = db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE git_deploys SET status = ?, last_output = ?, last_error = ?, last_deploy_at = ?,
			last_commit = CASE WHEN ? = '' THEN last_commit ELSE ? END,
			last_subject = CASE WHEN ? = '' THEN last_subject ELSE ? END WHERE domain_id = ?`,
			status, output, failure, now(), commit, commit, commit, subject, domainID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE git_deploys SET status = ?, pending = 0, last_trigger = 'webhook'
			WHERE domain_id = ? AND pending = 1`, GitRunning, domainID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		again = n == 1
		return nil
	})
	return again, err
}

// FailInterruptedGitDeploys marks deploys that were running when the panel
// stopped as failed.
func (db *DB) FailInterruptedGitDeploys(ctx context.Context, message string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE git_deploys SET status = ?, pending = 0, last_error = ? WHERE status = ?`,
		GitError, message, GitRunning)
	return err
}
