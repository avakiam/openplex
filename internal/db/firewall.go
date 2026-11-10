package db

import (
	"context"
	"database/sql"
	"time"
)

// FirewallRule allows incoming traffic to a host port (or, with no ports,
// everything from Source).
type FirewallRule struct {
	ID       int64  `json:"id"`
	Protocol string `json:"protocol"` // tcp | udp | both
	Ports    string `json:"ports"`    // "22", "8000-8100" or "" (all ports; needs a source)
	Source   string `json:"source"`   // "" (anywhere), an IP or a CIDR
	Comment  string `json:"comment"`
}

// FirewallConfig is the confirmed host firewall configuration.
type FirewallConfig struct {
	Enabled   bool           `json:"enabled"`
	Seeded    bool           `json:"-"`
	Rules     []FirewallRule `json:"rules"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// FirewallBlock drops all traffic from an address, websites included.
type FirewallBlock struct {
	ID        int64      `json:"id"`
	Address   string     `json:"address"` // IP or CIDR, normalised
	Comment   string     `json:"comment"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// GetFirewall returns the confirmed configuration, creating defaults on first use.
func (db *DB) GetFirewall(ctx context.Context) (*FirewallConfig, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO firewall_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	c := &FirewallConfig{Rules: []FirewallRule{}}
	if err := db.sql.QueryRowContext(ctx, `SELECT enabled, seeded, updated_at FROM firewall_settings WHERE id = 1`).
		Scan(&c.Enabled, &c.Seeded, &c.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT id, protocol, ports, source, comment FROM firewall_rules ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r FirewallRule
		if err := rows.Scan(&r.ID, &r.Protocol, &r.Ports, &r.Source, &r.Comment); err != nil {
			return nil, err
		}
		c.Rules = append(c.Rules, r)
	}
	return c, rows.Err()
}

// SaveFirewall replaces the configuration (rules get new IDs) and marks it seeded.
func (db *DB) SaveFirewall(ctx context.Context, enabled bool, rules []FirewallRule) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO firewall_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE firewall_settings SET enabled=?, seeded=1, updated_at=? WHERE id=1`, enabled, now()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_rules`); err != nil {
			return err
		}
		for i, r := range rules {
			if _, err := tx.ExecContext(ctx, `INSERT INTO firewall_rules (protocol, ports, source, comment, position, created_at) VALUES (?,?,?,?,?,?)`,
				r.Protocol, r.Ports, r.Source, r.Comment, i, now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListFirewallBlocks returns blocked addresses, newest first.
func (db *DB) ListFirewallBlocks(ctx context.Context) ([]FirewallBlock, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, address, comment, expires_at, created_at FROM firewall_blocks ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FirewallBlock{}
	for rows.Next() {
		var b FirewallBlock
		var exp sql.NullTime
		if err := rows.Scan(&b.ID, &b.Address, &b.Comment, &exp, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.ExpiresAt = ptrTime(exp)
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateFirewallBlock inserts b; a duplicate address is ErrConflict.
func (db *DB) CreateFirewallBlock(ctx context.Context, b *FirewallBlock) error {
	b.CreatedAt = now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO firewall_blocks (address, comment, expires_at, created_at) VALUES (?,?,?,?)`,
		b.Address, b.Comment, b.ExpiresAt, b.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	b.ID, err = res.LastInsertId()
	return err
}

// GetFirewallBlock returns one blocked address.
func (db *DB) GetFirewallBlock(ctx context.Context, id int64) (*FirewallBlock, error) {
	var b FirewallBlock
	var exp sql.NullTime
	err := db.sql.QueryRowContext(ctx, `SELECT id, address, comment, expires_at, created_at FROM firewall_blocks WHERE id = ?`, id).
		Scan(&b.ID, &b.Address, &b.Comment, &exp, &b.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	b.ExpiresAt = ptrTime(exp)
	return &b, nil
}

// DeleteFirewallBlock removes a blocked address.
func (db *DB) DeleteFirewallBlock(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM firewall_blocks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// DeleteExpiredFirewallBlocks removes blocks that expired at or before t.
func (db *DB) DeleteExpiredFirewallBlocks(ctx context.Context, t time.Time) (int64, error) {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM firewall_blocks WHERE expires_at IS NOT NULL AND expires_at <= ?`, t.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
