package db

import (
	"context"
	"time"
)

// UpdateSettings is the single panel-update configuration row.
type UpdateSettings struct {
	CheckEnabled bool      `json:"check_enabled"` // look for new releases twice a day
	UpdatedAt    time.Time `json:"updated_at"`
}

// GetUpdateSettings returns the settings, creating defaults on first use.
func (db *DB) GetUpdateSettings(ctx context.Context) (*UpdateSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO update_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	var s UpdateSettings
	err := db.sql.QueryRowContext(ctx, `SELECT check_enabled, updated_at FROM update_settings WHERE id = 1`).Scan(&s.CheckEnabled, &s.UpdatedAt)
	return &s, mapErr(err)
}

// SaveUpdateSettings persists s.
func (db *DB) SaveUpdateSettings(ctx context.Context, s *UpdateSettings) error {
	if _, err := db.GetUpdateSettings(ctx); err != nil {
		return err
	}
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE update_settings SET check_enabled = ?, updated_at = ? WHERE id = 1`, s.CheckEnabled, s.UpdatedAt)
	return err
}
