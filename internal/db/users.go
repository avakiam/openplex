package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Roles.
const (
	RoleAdmin    = "admin"
	RoleReseller = "reseller"
	RoleCustomer = "customer"
)

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool {
	return r == RoleAdmin || r == RoleReseller || r == RoleCustomer
}

// User is a panel account.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	ParentID     *int64    `json:"parent_id"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	Disabled     bool      `json:"disabled"`
	MaxDomains   int       `json:"max_domains"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const userCols = `id, username, email, password_hash, role, parent_id, totp_secret, totp_enabled, disabled, max_domains, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var parent sql.NullInt64
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &parent,
		&u.TOTPSecret, &u.TOTPEnabled, &u.Disabled, &u.MaxDomains, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	u.ParentID = ptrInt(parent)
	return &u, nil
}

// CreateUser inserts u and sets its ID and timestamps.
func (d *DB) CreateUser(ctx context.Context, u *User) error {
	u.CreatedAt, u.UpdatedAt = now(), now()
	res, err := d.sql.ExecContext(ctx, `INSERT INTO users (username, email, password_hash, role, parent_id, totp_secret, totp_enabled, disabled, max_domains, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		strings.TrimSpace(u.Username), u.Email, u.PasswordHash, u.Role, u.ParentID, u.TOTPSecret, u.TOTPEnabled, u.Disabled, u.MaxDomains, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	u.ID, err = res.LastInsertId()
	return err
}

// GetUser returns a user by ID.
func (d *DB) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(d.sql.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetUserByUsername returns a user by case-insensitive username.
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(d.sql.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, strings.TrimSpace(username)))
}

// UserFilter restricts ListUsers. A nil ParentID lists every user.
type UserFilter struct {
	ParentID *int64
}

// ListUsers returns users ordered by ID.
func (d *DB) ListUsers(ctx context.Context, f UserFilter) ([]*User, error) {
	q := `SELECT ` + userCols + ` FROM users`
	var args []any
	if f.ParentID != nil {
		q += ` WHERE parent_id = ?`
		args = append(args, *f.ParentID)
	}
	q += ` ORDER BY id`
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of users.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser persists mutable profile fields (email, role, parent, disabled, quota, TOTP, password).
func (d *DB) UpdateUser(ctx context.Context, u *User) error {
	u.UpdatedAt = now()
	res, err := d.sql.ExecContext(ctx, `UPDATE users SET email=?, password_hash=?, role=?, parent_id=?, totp_secret=?, totp_enabled=?, disabled=?, max_domains=?, updated_at=? WHERE id=?`,
		u.Email, u.PasswordHash, u.Role, u.ParentID, u.TOTPSecret, u.TOTPEnabled, u.Disabled, u.MaxDomains, u.UpdatedAt, u.ID)
	if err != nil {
		return mapErr(err)
	}
	return expectOne(res)
}

// DeleteUser removes a user. Fails if the user still owns domains or customers.
func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return fmt.Errorf("%w: user still owns domains or customers", ErrConflict)
		}
		return err
	}
	return expectOne(res)
}

func expectOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
