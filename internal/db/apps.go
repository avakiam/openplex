package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Application install states.
const (
	AppInstalling = "installing"
	AppInstalled  = "installed"
	AppError      = "error"
)

// Tasks running on an installed application.
const (
	AppTaskChecking = "checking"
	AppTaskUpdating = "updating"
)

// SiteApp is an application (WordPress, Joomla, Drupal…) installed by the
// panel in a website's document root. A website has at most one.
type SiteApp struct {
	DomainID      int64     `json:"domain_id"`
	App           string    `json:"app"`
	Version       string    `json:"version"`
	DatabaseID    *int64    `json:"database_id"`
	AdminUser     string    `json:"admin_user"`
	AdminEmail    string    `json:"admin_email"`
	Language      string    `json:"language"`
	Status        string    `json:"status"`
	StatusMessage string    `json:"status_message"`
	CreatedBy     *int64    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Updates is the result of the last update check (JSON; null before the first).
	Updates      json.RawMessage `json:"updates"`
	CheckedAt    *time.Time      `json:"checked_at"`
	Task         string          `json:"task"`       // "", checking or updating
	TaskError    string          `json:"task_error"` // why the last check or update failed
	LastUpdateAt *time.Time      `json:"last_update_at"`
}

const siteAppCols = `domain_id, app, version, database_id, admin_user, admin_email, language, status, status_message, created_by, created_at, updated_at`

const siteAppUpdateCols = `updates, checked_at, task, task_error, last_update_at`

func scanSiteApp(row interface{ Scan(...any) error }) (*SiteApp, error) {
	var a SiteApp
	var dbID, by sql.NullInt64
	var updates string
	var checked, lastUpdate sql.NullTime
	if err := row.Scan(&a.DomainID, &a.App, &a.Version, &dbID, &a.AdminUser, &a.AdminEmail, &a.Language,
		&a.Status, &a.StatusMessage, &by, &a.CreatedAt, &a.UpdatedAt,
		&updates, &checked, &a.Task, &a.TaskError, &lastUpdate); err != nil {
		return nil, mapErr(err)
	}
	if dbID.Valid {
		a.DatabaseID = &dbID.Int64
	}
	if by.Valid {
		a.CreatedBy = &by.Int64
	}
	if updates != "" {
		a.Updates = json.RawMessage(updates)
	}
	if checked.Valid {
		a.CheckedAt = &checked.Time
	}
	if lastUpdate.Valid {
		a.LastUpdateAt = &lastUpdate.Time
	}
	return &a, nil
}

// GetSiteApp returns the application of a website (ErrNotFound if none).
func (db *DB) GetSiteApp(ctx context.Context, domainID int64) (*SiteApp, error) {
	return scanSiteApp(db.sql.QueryRowContext(ctx, `SELECT `+siteAppCols+`, `+siteAppUpdateCols+` FROM site_apps WHERE domain_id = ?`, domainID))
}

// StartSiteApp records a new install of a, replacing a failed one. It returns
// ErrConflict when the website already has an application installed or
// installing.
func (db *DB) StartSiteApp(ctx context.Context, a *SiteApp) error {
	a.CreatedAt, a.UpdatedAt, a.Status, a.StatusMessage, a.Version = now(), now(), AppInstalling, "", ""
	a.Updates, a.CheckedAt, a.Task, a.TaskError, a.LastUpdateAt = nil, nil, "", "", nil
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM site_apps WHERE domain_id = ? AND status = ?`, a.DomainID, AppError); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO site_apps (`+siteAppCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.DomainID, a.App, a.Version, a.DatabaseID, a.AdminUser, a.AdminEmail, a.Language, a.Status, a.StatusMessage,
			a.CreatedBy, a.CreatedAt, a.UpdatedAt)
		return mapErr(err)
	})
}

// SetSiteAppDatabase links the database created for the application.
func (db *DB) SetSiteAppDatabase(ctx context.Context, domainID int64, databaseID *int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET database_id = ?, updated_at = ? WHERE domain_id = ?`, databaseID, now(), domainID)
	return err
}

// FinishSiteApp records the outcome of an install.
func (db *DB) FinishSiteApp(ctx context.Context, domainID int64, status, version, message string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET status = ?, version = ?, status_message = ?, updated_at = ? WHERE domain_id = ?`,
		status, version, message, now(), domainID)
	return err
}

// FailInterruptedSiteApps marks the installs that were running when the
// panel stopped as failed (message) and ends the update checks and updates
// that were running (taskMessage). It returns how many installs there were.
func (db *DB) FailInterruptedSiteApps(ctx context.Context, message, taskMessage string) (int64, error) {
	if _, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = '', task_error = ? WHERE task <> ''`, taskMessage); err != nil {
		return 0, err
	}
	res, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET status = ?, status_message = ?, updated_at = ? WHERE status = ?`,
		AppError, message, now(), AppInstalling)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteSiteApp forgets the application of a website (its files and database stay).
func (db *DB) DeleteSiteApp(ctx context.Context, domainID int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM site_apps WHERE domain_id = ?`, domainID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// StartSiteAppTask marks a check or an update as running on an installed
// application. It returns ErrNotFound when the website has no application and
// ErrConflict when it is not installed or another task is running.
func (db *DB) StartSiteAppTask(ctx context.Context, domainID int64, task string) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = ?, task_error = '' WHERE domain_id = ? AND status = ? AND task = ''`,
		task, domainID, AppInstalled)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := db.GetSiteApp(ctx, domainID); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

// FinishSiteAppCheck records the outcome of an update check: the version
// found and the updates available (JSON), or why it failed (the previous
// result then stays).
func (db *DB) FinishSiteAppCheck(ctx context.Context, domainID int64, version string, updates []byte, failure string) error {
	if failure != "" {
		_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = '', task_error = ? WHERE domain_id = ?`, failure, domainID)
		return err
	}
	_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = '', task_error = '', updates = ?, checked_at = ?,
		version = CASE WHEN ? = '' THEN version ELSE ? END WHERE domain_id = ?`, string(updates), now(), version, version, domainID)
	return err
}

// FinishSiteAppUpdate records the outcome of an update; version is the one
// installed now ("" when unknown).
func (db *DB) FinishSiteAppUpdate(ctx context.Context, domainID int64, version, failure string) error {
	if failure != "" {
		_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = '', task_error = ?,
			version = CASE WHEN ? = '' THEN version ELSE ? END WHERE domain_id = ?`, failure, version, version, domainID)
		return err
	}
	_, err := db.sql.ExecContext(ctx, `UPDATE site_apps SET task = '', task_error = '', last_update_at = ?,
		version = CASE WHEN ? = '' THEN version ELSE ? END WHERE domain_id = ?`, now(), version, version, domainID)
	return err
}

// SiteAppsToCheck lists the websites whose installed application was last
// checked for updates before (or never).
func (db *DB) SiteAppsToCheck(ctx context.Context, before time.Time) ([]int64, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT domain_id FROM site_apps
		WHERE status = ? AND task = '' AND (checked_at IS NULL OR checked_at < ?) ORDER BY domain_id`, AppInstalled, before.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
