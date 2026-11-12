package db

import (
	"context"
	"database/sql"
	"time"
)

// Database engines.
const (
	EngineMariaDB  = "mariadb"
	EnginePostgres = "postgres"
	EngineRedis    = "redis"
)

// Database is a tenant database (MariaDB/PostgreSQL schema + user) or a
// dedicated Redis instance. The password is stored encrypted (secrets.Box).
type Database struct {
	ID            int64  `json:"id"`
	OwnerID       int64  `json:"owner_id"`
	Engine        string `json:"engine"`
	Name          string `json:"name"`
	Username      string `json:"username"`
	PasswordEnc   string `json:"-"`
	Status        string `json:"status"` // pending | running | error
	StatusMessage string `json:"status_message"`
	// RemoteAllow lists the addresses/networks (comma-separated) allowed to
	// reach the database remotely; empty = no remote access.
	RemoteAllow string `json:"-"`
	// MaxSizeMB limits the database's size (0: the account's limit applies).
	MaxSizeMB int `json:"max_size_mb"`
	// SizeLocked is set while the database is read-only for being over its limit.
	SizeLocked bool      `json:"size_locked"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const databaseCols = `id, owner_id, engine, name, username, password_enc, status, status_message, remote_allow, max_size_mb, size_locked, created_at, updated_at`

func scanDatabase(row interface{ Scan(...any) error }) (*Database, error) {
	var d Database
	if err := row.Scan(&d.ID, &d.OwnerID, &d.Engine, &d.Name, &d.Username, &d.PasswordEnc, &d.Status, &d.StatusMessage, &d.RemoteAllow, &d.MaxSizeMB, &d.SizeLocked, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

// CreateDatabase inserts d.
func (db *DB) CreateDatabase(ctx context.Context, d *Database) error {
	d.CreatedAt, d.UpdatedAt = now(), now()
	if d.Status == "" {
		d.Status = StatusPending
	}
	return db.tx(ctx, func(tx *sql.Tx) error {
		id, err := nextID(ctx, tx, "databases")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO databases (id, owner_id, engine, name, username, password_enc, status, status_message, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, id, d.OwnerID, d.Engine, d.Name, d.Username, d.PasswordEnc, d.Status, d.StatusMessage, d.CreatedAt, d.UpdatedAt); err != nil {
			return mapErr(err)
		}
		d.ID = id
		return nil
	})
}

// GetDatabase returns a database by ID.
func (db *DB) GetDatabase(ctx context.Context, id int64) (*Database, error) {
	return scanDatabase(db.sql.QueryRowContext(ctx, `SELECT `+databaseCols+` FROM databases WHERE id = ?`, id))
}

// ListDatabases lists databases of the given owners (nil = all), ordered by name.
func (db *DB) ListDatabases(ctx context.Context, ownerIDs []int64) ([]*Database, error) {
	q := `SELECT ` + databaseCols + ` FROM databases`
	var args []any
	if ownerIDs != nil {
		if len(ownerIDs) == 0 {
			return []*Database{}, nil
		}
		q += ` WHERE owner_id IN (` + placeholders(len(ownerIDs)) + `)`
		for _, id := range ownerIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY engine, name`
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountDatabasesByOwner counts databases of ownerID.
func (db *DB) CountDatabasesByOwner(ctx context.Context, ownerID int64) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM databases WHERE owner_id = ?`, ownerID).Scan(&n)
	return n, err
}

// SetDatabaseStatus records provisioning state.
func (db *DB) SetDatabaseStatus(ctx context.Context, id int64, status, message string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE databases SET status=?, status_message=?, updated_at=? WHERE id=?`, status, message, now(), id)
	return err
}

// SetDatabasePassword stores a new encrypted password.
func (db *DB) SetDatabasePassword(ctx context.Context, id int64, enc string) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE databases SET password_enc=?, updated_at=? WHERE id=?`, enc, now(), id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// DeleteDatabase removes the row.
func (db *DB) DeleteDatabase(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// SetDatabaseRemote sets the remote access allow-list of a database (empty = off).
func (db *DB) SetDatabaseRemote(ctx context.Context, id int64, allow string) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE databases SET remote_allow = ?, updated_at = ? WHERE id = ?`, allow, now(), id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoteDatabases lists the databases with remote access switched on.
func (db *DB) RemoteDatabases(ctx context.Context) ([]*Database, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+databaseCols+` FROM databases WHERE remote_allow <> '' ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDatabaseLimit sets the size limit of a database (MB, 0 = the account's).
func (db *DB) SetDatabaseLimit(ctx context.Context, id int64, mb int) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE databases SET max_size_mb = ?, updated_at = ? WHERE id = ?`, mb, now(), id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDatabaseLocked records whether a database is read-only for being over its limit.
func (db *DB) SetDatabaseLocked(ctx context.Context, id int64, locked bool) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE databases SET size_locked = ?, updated_at = ? WHERE id = ?`, locked, now(), id)
	return mapErr(err)
}

// LimitMB is the size limit that applies to d (MB, 0 = none): its own, or
// else its owner account's.
func (d *Database) LimitMB(owner *User) int {
	if d.MaxSizeMB > 0 {
		return d.MaxSizeMB
	}
	if owner != nil {
		return owner.MaxDBSizeMB
	}
	return 0
}
