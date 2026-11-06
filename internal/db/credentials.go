package db

import (
	"context"
	"database/sql"
	"time"
)

// RefreshToken is a stored (hashed) refresh token. Tokens issued by rotation
// share a Family; presenting a revoked token revokes the whole family.
type RefreshToken struct {
	ID        int64
	UserID    int64
	Family    string
	TokenHash string
	ExpiresAt time.Time
	Revoked   bool
	UserAgent string
	IP        string
	CreatedAt time.Time
}

// CreateRefreshToken stores t.
func (d *DB) CreateRefreshToken(ctx context.Context, t *RefreshToken) error {
	t.CreatedAt = now()
	res, err := d.sql.ExecContext(ctx, `INSERT INTO refresh_tokens (user_id, family, token_hash, expires_at, revoked, user_agent, ip, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		t.UserID, t.Family, t.TokenHash, t.ExpiresAt.UTC(), t.Revoked, t.UserAgent, t.IP, t.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	t.ID, err = res.LastInsertId()
	return err
}

// GetRefreshTokenByHash looks up a token by its hash.
func (d *DB) GetRefreshTokenByHash(ctx context.Context, hash string) (*RefreshToken, error) {
	var t RefreshToken
	err := d.sql.QueryRowContext(ctx, `SELECT id, user_id, family, token_hash, expires_at, revoked, user_agent, ip, created_at FROM refresh_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.UserID, &t.Family, &t.TokenHash, &t.ExpiresAt, &t.Revoked, &t.UserAgent, &t.IP, &t.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

// RotateRefreshToken atomically revokes old (only if still active) and stores next.
// Returns ErrNotFound if old was already revoked (concurrent reuse).
func (d *DB) RotateRefreshToken(ctx context.Context, oldID int64, next *RefreshToken) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE id = ? AND revoked = 0`, oldID)
		if err != nil {
			return err
		}
		if err := expectOne(res); err != nil {
			return err
		}
		next.CreatedAt = now()
		r, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens (user_id, family, token_hash, expires_at, revoked, user_agent, ip, created_at) VALUES (?,?,?,?,0,?,?,?)`,
			next.UserID, next.Family, next.TokenHash, next.ExpiresAt.UTC(), next.UserAgent, next.IP, next.CreatedAt)
		if err != nil {
			return mapErr(err)
		}
		next.ID, err = r.LastInsertId()
		return err
	})
}

// RevokeRefreshFamily revokes every token in a rotation family.
func (d *DB) RevokeRefreshFamily(ctx context.Context, family string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE family = ?`, family)
	return err
}

// RevokeUserRefreshTokens revokes all sessions of a user (password change, disable).
func (d *DB) RevokeUserRefreshTokens(ctx context.Context, userID int64) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE refresh_tokens SET revoked = 1 WHERE user_id = ?`, userID)
	return err
}

// PruneRefreshTokens deletes expired tokens.
func (d *DB) PruneRefreshTokens(ctx context.Context) (int64, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE expires_at < ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// APIKey is a long-lived bearer credential for automation.
type APIKey struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	KeyHash    string     `json:"-"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

const apiKeyCols = `id, user_id, name, prefix, key_hash, last_used_at, expires_at, created_at`

func scanAPIKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	var k APIKey
	var last, exp sql.NullTime
	if err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.KeyHash, &last, &exp, &k.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	k.LastUsedAt, k.ExpiresAt = ptrTime(last), ptrTime(exp)
	return &k, nil
}

// CreateAPIKey stores k.
func (d *DB) CreateAPIKey(ctx context.Context, k *APIKey) error {
	k.CreatedAt = now()
	res, err := d.sql.ExecContext(ctx, `INSERT INTO api_keys (user_id, name, prefix, key_hash, expires_at, created_at) VALUES (?,?,?,?,?,?)`,
		k.UserID, k.Name, k.Prefix, k.KeyHash, k.ExpiresAt, k.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	k.ID, err = res.LastInsertId()
	return err
}

// GetAPIKeyByHash resolves a presented key.
func (d *DB) GetAPIKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	return scanAPIKey(d.sql.QueryRowContext(ctx, `SELECT `+apiKeyCols+` FROM api_keys WHERE key_hash = ?`, hash))
}

// TouchAPIKey records usage.
func (d *DB) TouchAPIKey(ctx context.Context, id int64) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now(), id)
	return err
}

// ListAPIKeys lists a user's keys.
func (d *DB) ListAPIKeys(ctx context.Context, userID int64) ([]*APIKey, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+apiKeyCols+` FROM api_keys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey deletes a key owned by userID.
func (d *DB) DeleteAPIKey(ctx context.Context, userID, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// SSHKey is an authorized public key for SFTP/SSH access.
type SSHKey struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateSSHKey stores k.
func (d *DB) CreateSSHKey(ctx context.Context, k *SSHKey) error {
	k.CreatedAt = now()
	res, err := d.sql.ExecContext(ctx, `INSERT INTO ssh_keys (user_id, name, public_key, fingerprint, created_at) VALUES (?,?,?,?,?)`,
		k.UserID, k.Name, k.PublicKey, k.Fingerprint, k.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	k.ID, err = res.LastInsertId()
	return err
}

// ListSSHKeys lists a user's keys.
func (d *DB) ListSSHKeys(ctx context.Context, userID int64) ([]*SSHKey, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT id, user_id, name, public_key, fingerprint, created_at FROM ssh_keys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SSHKey{}
	for rows.Next() {
		var k SSHKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &k)
	}
	return out, rows.Err()
}

// DeleteSSHKey deletes a key owned by userID.
func (d *DB) DeleteSSHKey(ctx context.Context, userID, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// AuditEntry is one security-relevant event.
type AuditEntry struct {
	ID        int64     `json:"id"`
	UserID    *int64    `json:"user_id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

// Audit appends an entry. Errors are returned but callers usually only log them.
func (d *DB) Audit(ctx context.Context, e AuditEntry) error {
	_, err := d.sql.ExecContext(ctx, `INSERT INTO audit_log (user_id, action, target, detail, ip, created_at) VALUES (?,?,?,?,?,?)`,
		e.UserID, e.Action, e.Target, e.Detail, e.IP, now())
	return err
}

// ListAudit returns the newest entries first, paginated by beforeID (0 = newest).
func (d *DB) ListAudit(ctx context.Context, beforeID int64, limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, action, target, detail, ip, created_at FROM audit_log`
	args := []any{}
	if beforeID > 0 {
		q += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var uid sql.NullInt64
		if err := rows.Scan(&e.ID, &uid, &e.Action, &e.Target, &e.Detail, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.UserID = ptrInt(uid)
		out = append(out, &e)
	}
	return out, rows.Err()
}
