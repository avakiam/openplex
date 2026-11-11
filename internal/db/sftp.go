package db

import "context"

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
