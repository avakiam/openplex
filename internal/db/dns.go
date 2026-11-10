package db

import (
	"context"
	"database/sql"
	"time"
)

// DNSSettings is the single DNS configuration row.
type DNSSettings struct {
	Enabled    bool      `json:"enabled"`
	NS1        string    `json:"ns1"`
	NS2        string    `json:"ns2"`
	IPv4       string    `json:"ipv4"`
	IPv6       string    `json:"ipv6"`
	Hostmaster string    `json:"hostmaster"` // e-mail address for the SOA record
	UpdatedAt  time.Time `json:"updated_at"`
}

// DNSZone is an authoritative zone, one per primary website or alias.
type DNSZone struct {
	ID        int64     `json:"id"`
	DomainID  int64     `json:"domain_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DNSRecord is one record. Name is relative to the zone ("@" for the apex);
// hostnames in Content are absolute without the trailing dot.
type DNSRecord struct {
	ID        int64     `json:"id"`
	ZoneID    int64     `json:"zone_id"`
	DomainID  *int64    `json:"domain_id"` // subdomain website that owns it, if any
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Priority  int       `json:"priority"` // MX and SRV
	TTL       int       `json:"ttl"`
	Source    string    `json:"source"` // module that added it ("mail:<domain id>"), "" = user
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetDNSSettings returns the settings, creating defaults on first use.
func (db *DB) GetDNSSettings(ctx context.Context) (*DNSSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO dns_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	var s DNSSettings
	err := db.sql.QueryRowContext(ctx, `SELECT enabled, ns1, ns2, ipv4, ipv6, hostmaster, updated_at FROM dns_settings WHERE id = 1`).
		Scan(&s.Enabled, &s.NS1, &s.NS2, &s.IPv4, &s.IPv6, &s.Hostmaster, &s.UpdatedAt)
	return &s, mapErr(err)
}

// SaveDNSSettings persists s.
func (db *DB) SaveDNSSettings(ctx context.Context, s *DNSSettings) error {
	if _, err := db.GetDNSSettings(ctx); err != nil {
		return err
	}
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE dns_settings SET enabled=?, ns1=?, ns2=?, ipv4=?, ipv6=?, hostmaster=?, updated_at=? WHERE id=1`,
		s.Enabled, s.NS1, s.NS2, s.IPv4, s.IPv6, s.Hostmaster, s.UpdatedAt)
	return err
}

const dnsZoneCols = `id, domain_id, name, created_at, updated_at`

func scanZone(row interface{ Scan(...any) error }) (*DNSZone, error) {
	var z DNSZone
	if err := row.Scan(&z.ID, &z.DomainID, &z.Name, &z.CreatedAt, &z.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &z, nil
}

// CreateDNSZone inserts a zone together with its initial records.
func (db *DB) CreateDNSZone(ctx context.Context, z *DNSZone, records []DNSRecord) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		z.CreatedAt, z.UpdatedAt = now(), now()
		res, err := tx.ExecContext(ctx, `INSERT INTO dns_zones (domain_id, name, created_at, updated_at) VALUES (?,?,?,?)`, z.DomainID, z.Name, z.CreatedAt, z.UpdatedAt)
		if err != nil {
			return mapErr(err)
		}
		if z.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		for i := range records {
			records[i].ZoneID = z.ID
			if err := insertRecord(ctx, tx, &records[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListDNSZones returns every zone.
func (db *DB) ListDNSZones(ctx context.Context) ([]*DNSZone, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+dnsZoneCols+` FROM dns_zones ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DNSZone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// GetDNSZone returns a zone by ID.
func (db *DB) GetDNSZone(ctx context.Context, id int64) (*DNSZone, error) {
	return scanZone(db.sql.QueryRowContext(ctx, `SELECT `+dnsZoneCols+` FROM dns_zones WHERE id = ?`, id))
}

// GetDNSZoneByDomain returns the zone of a website.
func (db *DB) GetDNSZoneByDomain(ctx context.Context, domainID int64) (*DNSZone, error) {
	return scanZone(db.sql.QueryRowContext(ctx, `SELECT `+dnsZoneCols+` FROM dns_zones WHERE domain_id = ?`, domainID))
}

// TouchDNSZone marks a zone as changed.
func (db *DB) TouchDNSZone(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dns_zones SET updated_at=? WHERE id=?`, now(), id)
	return err
}

const dnsRecordCols = `id, zone_id, domain_id, name, type, content, priority, ttl, source, created_at, updated_at`

func insertRecord(ctx context.Context, tx *sql.Tx, r *DNSRecord) error {
	r.CreatedAt, r.UpdatedAt = now(), now()
	res, err := tx.ExecContext(ctx, `INSERT INTO dns_records (zone_id, domain_id, name, type, content, priority, ttl, source, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		r.ZoneID, r.DomainID, r.Name, r.Type, r.Content, r.Priority, r.TTL, r.Source, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	r.ID, err = res.LastInsertId()
	return err
}

// CreateDNSRecords inserts records (all in one transaction).
func (db *DB) CreateDNSRecords(ctx context.Context, records []DNSRecord) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		for i := range records {
			if err := insertRecord(ctx, tx, &records[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListDNSRecords returns a zone's records.
func (db *DB) ListDNSRecords(ctx context.Context, zoneID int64) ([]DNSRecord, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+dnsRecordCols+` FROM dns_records WHERE zone_id = ? ORDER BY name = '@' DESC, name, type, priority, id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSRecord{}
	for rows.Next() {
		var r DNSRecord
		var dom sql.NullInt64
		if err := rows.Scan(&r.ID, &r.ZoneID, &dom, &r.Name, &r.Type, &r.Content, &r.Priority, &r.TTL, &r.Source, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.DomainID = ptrInt(dom)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDNSRecord returns one record.
func (db *DB) GetDNSRecord(ctx context.Context, id int64) (*DNSRecord, error) {
	var r DNSRecord
	var dom sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `SELECT `+dnsRecordCols+` FROM dns_records WHERE id = ?`, id).
		Scan(&r.ID, &r.ZoneID, &dom, &r.Name, &r.Type, &r.Content, &r.Priority, &r.TTL, &r.Source, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	r.DomainID = ptrInt(dom)
	return &r, nil
}

// UpdateDNSRecord persists the editable fields.
func (db *DB) UpdateDNSRecord(ctx context.Context, r *DNSRecord) error {
	r.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE dns_records SET name=?, type=?, content=?, priority=?, ttl=?, updated_at=? WHERE id=?`,
		r.Name, r.Type, r.Content, r.Priority, r.TTL, r.UpdatedAt, r.ID)
	if err != nil {
		return mapErr(err)
	}
	return expectOne(res)
}

// DeleteDNSRecord removes a record.
func (db *DB) DeleteDNSRecord(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM dns_records WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// CountDNSRecords counts a zone's records.
func (db *DB) CountDNSRecords(ctx context.Context, zoneID int64) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_records WHERE zone_id = ?`, zoneID).Scan(&n)
	return n, err
}

// ReplaceDNSAddress points every record of typ with content old at new.
func (db *DB) ReplaceDNSAddress(ctx context.Context, typ, old, new string) (int64, error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE dns_records SET content=?, updated_at=? WHERE type=? AND content=?`, new, now(), typ, old)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteDNSRecordsBySource removes the records a module added.
func (db *DB) DeleteDNSRecordsBySource(ctx context.Context, source string) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM dns_records WHERE source = ?`, source)
	return err
}

// RetargetMX points the MX (and CNAME) records a module added at a new host.
func (db *DB) RetargetMX(ctx context.Context, source, host string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dns_records SET content=?, updated_at=? WHERE source=? AND type IN ('MX', 'CNAME')`, host, now(), source)
	return err
}
