package db

import (
	"context"
	"database/sql"
	"time"
)

// Extension is an installed extension (see docs/adr/0001-extensions.md).
type Extension struct {
	ID            string    `json:"id"`
	UID           int       `json:"-"` // container uid
	Version       string    `json:"version"`
	Manifest      string    `json:"-"`      // openplex-extension.json as installed
	Source        string    `json:"source"` // upload | marketplace
	Signer        string    `json:"signer"` // id of the trusted key that signed it, "" = unverified
	Enabled       bool      `json:"enabled"`
	SettingsEnc   string    `json:"-"` // JSON object sealed with secrets.Box
	LicenseEnc    string    `json:"-"` // licence key sealed with secrets.Box
	Token         string    `json:"-"` // sent with every panel → extension request
	Status        string    `json:"status"`
	StatusMessage string    `json:"status_message"`
	InstalledAt   time.Time `json:"installed_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExtensionUIDBase is added to the extension sequence to get its container uid.
const ExtensionUIDBase = 400000

const extensionCols = `id, uid, version, manifest, source, signer, enabled, settings_enc, license_enc, token, status, status_message, installed_at, updated_at`

func scanExtension(row interface{ Scan(...any) error }) (*Extension, error) {
	var e Extension
	if err := row.Scan(&e.ID, &e.UID, &e.Version, &e.Manifest, &e.Source, &e.Signer, &e.Enabled, &e.SettingsEnc, &e.LicenseEnc,
		&e.Token, &e.Status, &e.StatusMessage, &e.InstalledAt, &e.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &e, nil
}

// ListExtensions returns every installed extension, by id.
func (db *DB) ListExtensions(ctx context.Context) ([]*Extension, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+extensionCols+` FROM extensions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Extension
	for rows.Next() {
		e, err := scanExtension(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetExtension returns one extension or ErrNotFound.
func (db *DB) GetExtension(ctx context.Context, id string) (*Extension, error) {
	return scanExtension(db.sql.QueryRowContext(ctx, `SELECT `+extensionCols+` FROM extensions WHERE id = ?`, id))
}

// SaveExtension inserts e (allocating a uid that is never reused) or updates
// the existing row with the same id.
func (db *DB) SaveExtension(ctx context.Context, e *Extension) error {
	e.UpdatedAt = now()
	return db.tx(ctx, func(tx *sql.Tx) error {
		var uid int
		err := tx.QueryRowContext(ctx, `SELECT uid FROM extensions WHERE id = ?`, e.ID).Scan(&uid)
		switch {
		case err == sql.ErrNoRows:
			var n int
			if err := tx.QueryRowContext(ctx, `UPDATE id_sequences SET value = value + 1 WHERE name = 'extensions' RETURNING value`).Scan(&n); err != nil {
				return err
			}
			e.UID, e.InstalledAt = ExtensionUIDBase+n, e.UpdatedAt
			_, err = tx.ExecContext(ctx, `INSERT INTO extensions (`+extensionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				e.ID, e.UID, e.Version, e.Manifest, e.Source, e.Signer, e.Enabled, e.SettingsEnc, e.LicenseEnc, e.Token,
				e.Status, e.StatusMessage, e.InstalledAt, e.UpdatedAt)
			return mapErr(err)
		case err != nil:
			return err
		}
		e.UID = uid
		_, err = tx.ExecContext(ctx, `UPDATE extensions SET version=?, manifest=?, source=?, signer=?, enabled=?, settings_enc=?, license_enc=?,
			token=?, status=?, status_message=?, updated_at=? WHERE id=?`,
			e.Version, e.Manifest, e.Source, e.Signer, e.Enabled, e.SettingsEnc, e.LicenseEnc, e.Token, e.Status, e.StatusMessage, e.UpdatedAt, e.ID)
		return err
	})
}

// SetExtensionStatus records the runtime state of an extension.
func (db *DB) SetExtensionStatus(ctx context.Context, id, status, msg string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE extensions SET status=?, status_message=?, updated_at=? WHERE id=?`, status, msg, now(), id)
	return err
}

// DeleteExtension removes the row.
func (db *DB) DeleteExtension(ctx context.Context, id string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM extensions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
