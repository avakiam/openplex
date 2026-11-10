package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Domain kinds.
const (
	KindPrimary   = "primary"
	KindSubdomain = "subdomain"
	KindAlias     = "alias"
)

// Domain statuses.
const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusError    = "error"
	StatusDisabled = "disabled"
)

// Domain is a hosted name. Primary domains and subdomains are websites with
// their own runtime container; aliases are extra hostnames served by ParentID.
type Domain struct {
	ID             int64             `json:"id"`
	OwnerID        int64             `json:"owner_id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	ParentID       *int64            `json:"parent_id"`
	Runtime        string            `json:"runtime"`
	RuntimeVersion string            `json:"runtime_version"`
	DocumentRoot   string            `json:"document_root"`
	StartCommand   string            `json:"start_command"`
	AppPort        int               `json:"app_port"`
	SSLEnabled     bool              `json:"ssl_enabled"`
	ForceHTTPS     bool              `json:"force_https"`
	MemoryMB       int               `json:"memory_mb"`
	CPUs           float64           `json:"cpus"`
	Env            map[string]string `json:"env"`
	Status         string            `json:"status"`
	StatusMessage  string            `json:"status_message"`
	ContainerID    string            `json:"container_id"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

const domainCols = `id, owner_id, name, kind, parent_id, runtime, runtime_version, document_root, start_command, app_port,
	ssl_enabled, force_https, memory_mb, cpus, env_json, status, status_message, container_id, created_at, updated_at`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var parent sql.NullInt64
	var envJSON string
	if err := row.Scan(&d.ID, &d.OwnerID, &d.Name, &d.Kind, &parent, &d.Runtime, &d.RuntimeVersion, &d.DocumentRoot,
		&d.StartCommand, &d.AppPort, &d.SSLEnabled, &d.ForceHTTPS, &d.MemoryMB, &d.CPUs, &envJSON,
		&d.Status, &d.StatusMessage, &d.ContainerID, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	d.ParentID = ptrInt(parent)
	d.Env = map[string]string{}
	if err := json.Unmarshal([]byte(envJSON), &d.Env); err != nil {
		return nil, fmt.Errorf("decode env of domain %d: %w", d.ID, err)
	}
	return &d, nil
}

func encodeEnv(env map[string]string) (string, error) {
	if env == nil {
		env = map[string]string{}
	}
	b, err := json.Marshal(env)
	return string(b), err
}

// CreateDomain inserts d.
func (db *DB) CreateDomain(ctx context.Context, d *Domain) error {
	envJSON, err := encodeEnv(d.Env)
	if err != nil {
		return err
	}
	d.CreatedAt, d.UpdatedAt = now(), now()
	if d.Status == "" {
		d.Status = StatusPending
	}
	return db.tx(ctx, func(tx *sql.Tx) error {
		id, err := nextID(ctx, tx, "domains")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domains (id, owner_id, name, kind, parent_id, runtime, runtime_version, document_root, start_command, app_port,
		ssl_enabled, force_https, memory_mb, cpus, env_json, status, status_message, container_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, d.OwnerID, d.Name, d.Kind, d.ParentID, d.Runtime, d.RuntimeVersion, d.DocumentRoot, d.StartCommand, d.AppPort,
			d.SSLEnabled, d.ForceHTTPS, d.MemoryMB, d.CPUs, envJSON, d.Status, d.StatusMessage, d.ContainerID, d.CreatedAt, d.UpdatedAt); err != nil {
			return mapErr(err)
		}
		d.ID = id
		return nil
	})
}

// GetDomain returns a domain by ID.
func (db *DB) GetDomain(ctx context.Context, id int64) (*Domain, error) {
	return scanDomain(db.sql.QueryRowContext(ctx, `SELECT `+domainCols+` FROM domains WHERE id = ?`, id))
}

// GetDomainByName returns a domain by hostname.
func (db *DB) GetDomainByName(ctx context.Context, name string) (*Domain, error) {
	return scanDomain(db.sql.QueryRowContext(ctx, `SELECT `+domainCols+` FROM domains WHERE name = ?`, name))
}

// DomainFilter restricts ListDomains. Zero values mean "no filter".
type DomainFilter struct {
	OwnerIDs []int64 // nil = all owners
	ParentID *int64
	Kind     string
}

// ListDomains returns domains matching f ordered by name.
func (db *DB) ListDomains(ctx context.Context, f DomainFilter) ([]*Domain, error) {
	q := `SELECT ` + domainCols + ` FROM domains WHERE 1=1`
	var args []any
	if f.OwnerIDs != nil {
		if len(f.OwnerIDs) == 0 {
			return []*Domain{}, nil
		}
		q += ` AND owner_id IN (` + placeholders(len(f.OwnerIDs)) + `)`
		for _, id := range f.OwnerIDs {
			args = append(args, id)
		}
	}
	if f.ParentID != nil {
		q += ` AND parent_id = ?`
		args = append(args, *f.ParentID)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	q += ` ORDER BY name`
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountDomainsByOwner counts websites (non-alias domains) owned by ownerID.
func (db *DB) CountDomainsByOwner(ctx context.Context, ownerID int64) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM domains WHERE owner_id = ? AND kind != 'alias'`, ownerID).Scan(&n)
	return n, err
}

// UpdateDomain persists all mutable fields of d.
func (db *DB) UpdateDomain(ctx context.Context, d *Domain) error {
	envJSON, err := encodeEnv(d.Env)
	if err != nil {
		return err
	}
	d.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE domains SET runtime=?, runtime_version=?, document_root=?, start_command=?, app_port=?,
		ssl_enabled=?, force_https=?, memory_mb=?, cpus=?, env_json=?, status=?, status_message=?, container_id=?, updated_at=? WHERE id=?`,
		d.Runtime, d.RuntimeVersion, d.DocumentRoot, d.StartCommand, d.AppPort, d.SSLEnabled, d.ForceHTTPS,
		d.MemoryMB, d.CPUs, envJSON, d.Status, d.StatusMessage, d.ContainerID, d.UpdatedAt, d.ID)
	if err != nil {
		return mapErr(err)
	}
	return expectOne(res)
}

// SetDomainStatus updates only the runtime status fields.
func (db *DB) SetDomainStatus(ctx context.Context, id int64, status, message, containerID string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE domains SET status=?, status_message=?, container_id=?, updated_at=? WHERE id=?`,
		status, message, containerID, now(), id)
	return err
}

// SetDomainHealth records a status observed from the website's container. It
// only moves between running and error, so it never overrides a deploy in
// progress, a stop or a suspension, and it keeps deploy error messages until
// the app has proven healthy. Reports whether anything changed.
func (db *DB) SetDomainHealth(ctx context.Context, id int64, status, message string) (bool, error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE domains SET status=?, status_message=?, updated_at=?
		WHERE id=? AND status IN ('running','error') AND (status<>? OR status_message<>?)`,
		status, message, now(), id, status, message)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteDomain removes a domain (its aliases and redirects cascade).
func (db *DB) DeleteDomain(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// Redirect is an HTTP redirect rule attached to a website.
type Redirect struct {
	ID         int64     `json:"id"`
	DomainID   int64     `json:"domain_id"`
	SourcePath string    `json:"source_path"`
	TargetURL  string    `json:"target_url"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateRedirect inserts r.
func (db *DB) CreateRedirect(ctx context.Context, r *Redirect) error {
	r.CreatedAt = now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO redirects (domain_id, source_path, target_url, status_code, created_at) VALUES (?,?,?,?,?)`,
		r.DomainID, r.SourcePath, r.TargetURL, r.StatusCode, r.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	r.ID, err = res.LastInsertId()
	return err
}

// ListRedirects lists rules for a domain (nil domainID = all, used by the edge renderer).
func (db *DB) ListRedirects(ctx context.Context, domainID *int64) ([]*Redirect, error) {
	q := `SELECT id, domain_id, source_path, target_url, status_code, created_at FROM redirects`
	var args []any
	if domainID != nil {
		q += ` WHERE domain_id = ?`
		args = append(args, *domainID)
	}
	q += ` ORDER BY domain_id, length(source_path) DESC`
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Redirect{}
	for rows.Next() {
		var r Redirect
		if err := rows.Scan(&r.ID, &r.DomainID, &r.SourcePath, &r.TargetURL, &r.StatusCode, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// DeleteRedirect deletes a rule belonging to domainID.
func (db *DB) DeleteRedirect(ctx context.Context, domainID, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM redirects WHERE id = ? AND domain_id = ?`, id, domainID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func placeholders(n int) string {
	b := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}
