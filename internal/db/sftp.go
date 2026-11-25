package db

import (
	"context"
	"database/sql"
	"time"
)

// SFTPPasswordHash returns the hash of a user's SFTP password ("" if none is set).
func (db *DB) SFTPPasswordHash(ctx context.Context, userID int64) (string, error) {
	var h string
	err := db.sql.QueryRowContext(ctx, `SELECT password_hash FROM sftp_passwords WHERE user_id = ?`, userID).Scan(&h)
	if err = mapErr(err); err == ErrNotFound {
		return "", nil
	}
	return h, err
}

// SetSFTPPassword stores (or replaces) a user's SFTP password hash.
func (db *DB) SetSFTPPassword(ctx context.Context, userID int64, hash string) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO sftp_passwords (user_id, password_hash, updated_at) VALUES (?, ?, ?)
	ON CONFLICT (user_id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`, userID, hash, now())
	return err
}

// DeleteSFTPPassword removes a user's SFTP password (keys keep working).
func (db *DB) DeleteSFTPPassword(ctx context.Context, userID int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM sftp_passwords WHERE user_id = ?`, userID)
	return err
}

// SFTPAccount signs in to one website over SFTP (as name@website) and has no
// panel access. Directory limits it to a folder of the website ("" = all of it).
type SFTPAccount struct {
	ID             int64      `json:"id"`
	DomainID       int64      `json:"domain_id"`
	Username       string     `json:"username"`
	PasswordHash   string     `json:"-"`
	PublicKey      string     `json:"public_key"`
	KeyFingerprint string     `json:"key_fingerprint"`
	Directory      string     `json:"directory"`
	ReadOnly       bool       `json:"read_only"`
	Enabled        bool       `json:"enabled"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastLoginAt    *time.Time `json:"last_login_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Expired reports whether the account's end date has passed at t.
func (a *SFTPAccount) Expired(t time.Time) bool {
	return a.ExpiresAt != nil && !t.Before(*a.ExpiresAt)
}

const sftpAccountCols = `id, domain_id, username, password_hash, public_key, key_fingerprint, directory, read_only, enabled,
	expires_at, last_login_at, created_at, updated_at`

func scanSFTPAccount(row interface{ Scan(...any) error }) (*SFTPAccount, error) {
	var a SFTPAccount
	var exp, last sql.NullTime
	if err := row.Scan(&a.ID, &a.DomainID, &a.Username, &a.PasswordHash, &a.PublicKey, &a.KeyFingerprint, &a.Directory,
		&a.ReadOnly, &a.Enabled, &exp, &last, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	a.ExpiresAt, a.LastLoginAt = ptrTime(exp), ptrTime(last)
	return &a, nil
}

// CreateSFTPAccount inserts a; a taken username is ErrConflict.
func (db *DB) CreateSFTPAccount(ctx context.Context, a *SFTPAccount) error {
	a.CreatedAt, a.UpdatedAt = now(), now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO sftp_accounts (domain_id, username, password_hash, public_key, key_fingerprint,
		directory, read_only, enabled, expires_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.DomainID, a.Username, a.PasswordHash, a.PublicKey, a.KeyFingerprint, a.Directory, a.ReadOnly, a.Enabled,
		utcPtr(a.ExpiresAt), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	a.ID, err = res.LastInsertId()
	return err
}

// GetSFTPAccount returns an account by ID.
func (db *DB) GetSFTPAccount(ctx context.Context, id int64) (*SFTPAccount, error) {
	return scanSFTPAccount(db.sql.QueryRowContext(ctx, `SELECT `+sftpAccountCols+` FROM sftp_accounts WHERE id = ?`, id))
}

// GetSFTPAccountByUsername returns the account signing in as username.
func (db *DB) GetSFTPAccountByUsername(ctx context.Context, username string) (*SFTPAccount, error) {
	return scanSFTPAccount(db.sql.QueryRowContext(ctx, `SELECT `+sftpAccountCols+` FROM sftp_accounts WHERE username = ?`, username))
}

// ListSFTPAccounts returns a website's accounts.
func (db *DB) ListSFTPAccounts(ctx context.Context, domainID int64) ([]*SFTPAccount, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+sftpAccountCols+` FROM sftp_accounts WHERE domain_id = ? ORDER BY username`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SFTPAccount{}
	for rows.Next() {
		a, err := scanSFTPAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountSFTPAccounts counts a website's accounts.
func (db *DB) CountSFTPAccounts(ctx context.Context, domainID int64) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_accounts WHERE domain_id = ?`, domainID).Scan(&n)
	return n, err
}

// UpdateSFTPAccount persists the editable fields of a.
func (db *DB) UpdateSFTPAccount(ctx context.Context, a *SFTPAccount) error {
	a.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE sftp_accounts SET password_hash=?, public_key=?, key_fingerprint=?, directory=?,
		read_only=?, enabled=?, expires_at=?, updated_at=? WHERE id=?`,
		a.PasswordHash, a.PublicKey, a.KeyFingerprint, a.Directory, a.ReadOnly, a.Enabled, utcPtr(a.ExpiresAt), a.UpdatedAt, a.ID)
	if err != nil {
		return mapErr(err)
	}
	return expectOne(res)
}

// TouchSFTPAccountLogin records a successful sign-in.
func (db *DB) TouchSFTPAccountLogin(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE sftp_accounts SET last_login_at = ? WHERE id = ?`, now(), id)
	return err
}

// DeleteSFTPAccount removes an account of a website.
func (db *DB) DeleteSFTPAccount(ctx context.Context, domainID, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM sftp_accounts WHERE id = ? AND domain_id = ?`, id, domainID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
