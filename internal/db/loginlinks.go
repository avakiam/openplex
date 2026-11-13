package db

import (
	"context"
	"database/sql"
	"time"
)

// What a sign-in link is for.
const (
	// LinkHandover hands an account over (openplexd login-link): after
	// signing in, it may set a password without knowing the current one.
	LinkHandover = "handover"
	// LinkSSO signs in from a billing system's client area, nothing more.
	LinkSSO = "sso"
)

// CreateLoginLink stores a one-time sign-in link for a user (its token hashed).
func (db *DB) CreateLoginLink(ctx context.Context, userID int64, tokenHash string, expires time.Time, purpose string) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO login_links (user_id, token_hash, expires_at, created_at, purpose) VALUES (?,?,?,?,?)`,
		userID, tokenHash, expires.UTC(), now(), purpose)
	return mapErr(err)
}

// UseLoginLink marks a link used and returns its user and purpose;
// ErrNotFound when the link does not exist, was used already or has expired.
func (db *DB) UseLoginLink(ctx context.Context, tokenHash string) (uid int64, purpose string, err error) {
	err = db.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT user_id, purpose FROM login_links WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
			tokenHash, now()).Scan(&uid, &purpose); err != nil {
			return mapErr(err)
		}
		_, err := tx.ExecContext(ctx, `UPDATE login_links SET used_at = ? WHERE token_hash = ?`, now(), tokenHash)
		return err
	})
	return uid, purpose, err
}

// PruneLoginLinks deletes links that expired or were used before t.
func (db *DB) PruneLoginLinks(ctx context.Context, t time.Time) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM login_links WHERE expires_at < ? OR used_at < ?`, t.UTC(), t.UTC())
	return err
}

// LoginLinkUsedSince reports whether the user signed in with a handover link
// after t: such a session may set a password without knowing the current one.
func (db *DB) LoginLinkUsedSince(ctx context.Context, userID int64, t time.Time) (bool, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_links WHERE user_id = ? AND used_at > ? AND purpose = ?`,
		userID, t.UTC(), LinkHandover).Scan(&n)
	return n > 0, err
}

// ForgetUsedLoginLinks deletes the user's used links (once it has set a
// password, a link sign-in no longer lets it skip the current one).
func (db *DB) ForgetUsedLoginLinks(ctx context.Context, userID int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM login_links WHERE user_id = ? AND used_at IS NOT NULL`, userID)
	return err
}
