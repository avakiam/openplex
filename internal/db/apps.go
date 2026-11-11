package db

import (
	"context"
	"database/sql"
	"time"
)

// Application install states.
const (
	AppInstalling = "installing"
	AppInstalled  = "installed"
	AppError      = "error"
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
}

const siteAppCols = `domain_id, app, version, database_id, admin_user, admin_email, language, status, status_message, created_by, created_at, updated_at`

func scanSiteApp(row interface{ Scan(...any) error }) (*SiteApp, error) {
	var a SiteApp
	var dbID, by sql.NullInt64
	if err := row.Scan(&a.DomainID, &a.App, &a.Version, &dbID, &a.AdminUser, &a.AdminEmail, &a.Language,
		&a.Status, &a.StatusMessage, &by, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	if dbID.Valid {
		a.DatabaseID = &dbID.Int64
	}
	if by.Valid {
		a.CreatedBy = &by.Int64
	}
	return &a, nil
}

// GetSiteApp returns the application of a website (ErrNotFound if none).
func (db *DB) GetSiteApp(ctx context.Context, domainID int64) (*SiteApp, error) {
	return scanSiteApp(db.sql.QueryRowContext(ctx, `SELECT `+siteAppCols+` FROM site_apps WHERE domain_id = ?`, domainID))
}

// StartSiteApp records a new install of a, replacing a failed one. It returns
// ErrConflict when the website already has an application installed or
// installing.
func (db *DB) StartSiteApp(ctx context.Context, a *SiteApp) error {
	a.CreatedAt, a.UpdatedAt, a.Status, a.StatusMessage, a.Version = now(), now(), AppInstalling, "", ""
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

// FailInterruptedSiteApps marks installs that were running when the panel
// stopped as failed; it returns how many there were.
func (db *DB) FailInterruptedSiteApps(ctx context.Context, message string) (int64, error) {
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
