// Package db is the embedded SQLite state store of the OpenPlex control plane.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registers "sqlite"
)

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint is violated.
var ErrConflict = errors.New("already exists")

// DB wraps *sql.DB and exposes typed repositories.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the SQLite database at path in WAL mode
// and applies pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	q.Set("_time_format", "sqlite")
	return open(ctx, "file:"+path+"?"+q.Encode(), 8)
}

// OpenMemory opens a private in-memory database for tests.
func OpenMemory(t testing.TB) *DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)&_txlock=immediate&_time_format=sqlite", name)
	d, err := open(context.Background(), dsn, 1)
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func open(ctx context.Context, dsn string, maxConns int) (*DB, error) {
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s.SetMaxOpenConns(maxConns)
	s.SetConnMaxIdleTime(5 * time.Minute)
	if err := s.PingContext(ctx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	d := &DB{sql: s}
	if err := d.migrate(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the raw handle (backups use it for VACUUM INTO).
func (d *DB) SQL() *sql.DB { return d.sql }

// Snapshot writes a consistent copy of the database to dest using VACUUM INTO.
func (d *DB) Snapshot(ctx context.Context, dest string) error {
	_, err := d.sql.ExecContext(ctx, "VACUUM INTO ?", dest)
	if err != nil {
		return fmt.Errorf("snapshot db: %w", err)
	}
	return nil
}

// nextID reserves the next never-used ID for table ("domains" or "databases").
func nextID(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var q string
	switch table {
	case "domains":
		q = `UPDATE id_sequences SET value = MAX(value, (SELECT COALESCE(MAX(id), 0) FROM domains)) + 1 WHERE name = 'domains' RETURNING value`
	case "databases":
		q = `UPDATE id_sequences SET value = MAX(value, (SELECT COALESCE(MAX(id), 0) FROM databases)) + 1 WHERE name = 'databases' RETURNING value`
	default:
		return 0, fmt.Errorf("no id sequence for %q", table)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, q).Scan(&id); err != nil {
		return 0, fmt.Errorf("reserve %s id: %w", table, err)
	}
	return id, nil
}

func (d *DB) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// mapErr converts driver errors into package sentinels.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// nullTime helps scan nullable timestamps.
func ptrTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time.UTC()
	return &t
}

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
