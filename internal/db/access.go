package db

import (
	"context"
	"strings"
	"time"
)

// Grant kinds and the areas of a website a grant can open.
const (
	GrantSite     = "site"
	GrantDatabase = "database"

	AreaSite    = "site"    // settings, start/stop, logs, redirects, cron
	AreaFiles   = "files"   // file manager and web terminal
	AreaDNS     = "dns"     // DNS zone
	AreaMail    = "mail"    // mailboxes, forwarding, webmail, mail backups
	AreaBackups = "backups" // backups of the website's files
)

// SiteAreas lists the website areas in display order.
var SiteAreas = []string{AreaSite, AreaFiles, AreaDNS, AreaMail, AreaBackups}

// AccessGrant lets a user work on a resource owned by someone else.
type AccessGrant struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"user_id"`
	Kind          string    `json:"kind"`
	ResourceID    int64     `json:"resource_id"`
	Areas         []string  `json:"areas"` // websites only
	ReadOnly      bool      `json:"read_only"`
	CreatedBy     *int64    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	ResourceName  string    `json:"resource_name"` // joined
	ResourceOwner int64     `json:"resource_owner_id"`
}

// Has reports whether the grant opens area.
func (g *AccessGrant) Has(area string) bool {
	for _, a := range g.Areas {
		if a == area {
			return true
		}
	}
	return false
}

const grantSelect = `SELECT g.id, g.user_id, g.kind, g.resource_id, g.areas, g.read_only, g.created_by, g.created_at,
	COALESCE(d.name, b.name, ''), COALESCE(d.owner_id, b.owner_id, 0)
FROM access_grants g
LEFT JOIN domains d ON g.kind = 'site' AND d.id = g.resource_id
LEFT JOIN databases b ON g.kind = 'database' AND b.id = g.resource_id`

func scanGrant(row interface{ Scan(...any) error }) (*AccessGrant, error) {
	var g AccessGrant
	var areas string
	if err := row.Scan(&g.ID, &g.UserID, &g.Kind, &g.ResourceID, &areas, &g.ReadOnly, &g.CreatedBy, &g.CreatedAt, &g.ResourceName, &g.ResourceOwner); err != nil {
		return nil, err
	}
	g.Areas = []string{}
	if areas != "" {
		g.Areas = strings.Split(areas, ",")
	}
	return &g, nil
}

// SaveGrant creates the grant, or replaces the areas of the user's existing
// grant on that resource.
func (db *DB) SaveGrant(ctx context.Context, g *AccessGrant) error {
	g.CreatedAt = now()
	err := db.sql.QueryRowContext(ctx, `INSERT INTO access_grants (user_id, kind, resource_id, areas, read_only, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, kind, resource_id) DO UPDATE SET areas = excluded.areas, read_only = excluded.read_only
		RETURNING id`, g.UserID, g.Kind, g.ResourceID, strings.Join(g.Areas, ","), g.ReadOnly, g.CreatedBy, g.CreatedAt).Scan(&g.ID)
	return mapErr(err)
}

// ListUserGrants returns the grants a user holds.
func (db *DB) ListUserGrants(ctx context.Context, userID int64) ([]*AccessGrant, error) {
	rows, err := db.sql.QueryContext(ctx, grantSelect+` WHERE g.user_id = ? ORDER BY g.kind, 9`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AccessGrant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FindGrant returns the user's grant on a resource (ErrNotFound if none).
func (db *DB) FindGrant(ctx context.Context, userID int64, kind string, resourceID int64) (*AccessGrant, error) {
	g, err := scanGrant(db.sql.QueryRowContext(ctx, grantSelect+` WHERE g.user_id = ? AND g.kind = ? AND g.resource_id = ?`, userID, kind, resourceID))
	return g, mapErr(err)
}

// GetGrant returns one grant.
func (db *DB) GetGrant(ctx context.Context, id int64) (*AccessGrant, error) {
	g, err := scanGrant(db.sql.QueryRowContext(ctx, grantSelect+` WHERE g.id = ?`, id))
	return g, mapErr(err)
}

// DeleteGrant removes a grant.
func (db *DB) DeleteGrant(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM access_grants WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
