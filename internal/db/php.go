package db

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetPHPSettings returns a website's PHP settings that differ from the
// defaults (an empty map when there are none).
func (db *DB) GetPHPSettings(ctx context.Context, domainID int64) (map[string]string, error) {
	var raw string
	err := mapErr(db.sql.QueryRowContext(ctx, `SELECT settings_json FROM php_settings WHERE domain_id = ?`, domainID).Scan(&raw))
	out := map[string]string{}
	if err == ErrNotFound {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode PHP settings of domain %d: %w", domainID, err)
	}
	return out, nil
}

// SavePHPSettings replaces a website's PHP settings; an empty map removes them.
func (db *DB) SavePHPSettings(ctx context.Context, domainID int64, settings map[string]string) error {
	if len(settings) == 0 {
		_, err := db.sql.ExecContext(ctx, `DELETE FROM php_settings WHERE domain_id = ?`, domainID)
		return err
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx, `INSERT INTO php_settings (domain_id, settings_json, updated_at) VALUES (?, ?, ?)
	ON CONFLICT (domain_id) DO UPDATE SET settings_json = excluded.settings_json, updated_at = excluded.updated_at`, domainID, string(b), now())
	return err
}
