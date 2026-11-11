package db

import (
	"context"
	"database/sql"
	"encoding/json"
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

// EdgePolicy is what an extension with the edge:write permission asks of the
// edge web server. The API validates it; the edge renderer validates it again.
type EdgePolicy struct {
	// TrustedProxies are CIDRs whose forwarding headers are believed, so the
	// edge and the websites see the visitor's address behind them.
	TrustedProxies []string `json:"trusted_proxies"`
	// ClientIPHeaders name the headers carrying that address, in order.
	ClientIPHeaders []string `json:"client_ip_headers"`
	// Restrict limits website hosts to connections from these CIDRs.
	Restrict []EdgeRestriction `json:"restrict"`
}

// EdgeRestriction allows a website host only from some networks.
type EdgeRestriction struct {
	Host  string   `json:"host"`
	Allow []string `json:"allow"`
}

// Empty reports whether the policy asks for nothing.
func (p EdgePolicy) Empty() bool {
	return len(p.TrustedProxies) == 0 && len(p.ClientIPHeaders) == 0 && len(p.Restrict) == 0
}

// GetExtensionEdgePolicy returns an extension's edge policy (empty when none).
func (db *DB) GetExtensionEdgePolicy(ctx context.Context, id string) (EdgePolicy, error) {
	var raw string
	if err := db.sql.QueryRowContext(ctx, `SELECT edge_policy FROM extensions WHERE id = ?`, id).Scan(&raw); err != nil {
		return EdgePolicy{}, mapErr(err)
	}
	var p EdgePolicy
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return EdgePolicy{}, err
		}
	}
	return p, nil
}

// SetExtensionEdgePolicy stores an extension's edge policy (empty clears it).
func (db *DB) SetExtensionEdgePolicy(ctx context.Context, id string, p EdgePolicy) error {
	raw := ""
	if !p.Empty() {
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	res, err := db.sql.ExecContext(ctx, `UPDATE extensions SET edge_policy = ? WHERE id = ?`, raw, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// EdgePolicies returns the edge policies of the enabled extensions, by id.
// An unreadable policy is skipped: the edge then simply stays open.
func (db *DB) EdgePolicies(ctx context.Context) (map[string]EdgePolicy, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, edge_policy FROM extensions WHERE enabled = 1 AND edge_policy != '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]EdgePolicy{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p EdgePolicy
		if json.Unmarshal([]byte(raw), &p) == nil {
			out[id] = p
		}
	}
	return out, rows.Err()
}
