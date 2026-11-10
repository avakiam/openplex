package db

import (
	"context"
	"strings"
	"time"
)

// MailSettings is the single mail configuration row.
type MailSettings struct {
	Enabled   bool      `json:"enabled"`
	Hostname  string    `json:"hostname"` // the server's mail host (MX target, PTR, certificate)
	UpdatedAt time.Time `json:"updated_at"`
}

// MailDomain is a domain that receives mail here.
type MailDomain struct {
	ID           int64     `json:"id"`
	DomainID     int64     `json:"domain_id"`
	Name         string    `json:"name"` // joined from domains
	DKIMSelector string    `json:"dkim_selector"`
	DKIMKeyEnc   string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Mailbox is an IMAP/POP3 account.
type Mailbox struct {
	ID           int64     `json:"id"`
	MailDomainID int64     `json:"mail_domain_id"`
	LocalPart    string    `json:"local_part"`
	PasswordHash string    `json:"-"`
	QuotaMB      int       `json:"quota_mb"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// MailAlias forwards local_part@domain to one or more addresses.
type MailAlias struct {
	ID           int64     `json:"id"`
	MailDomainID int64     `json:"mail_domain_id"`
	LocalPart    string    `json:"local_part"`
	Destinations []string  `json:"destinations"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GetMailSettings returns the settings, creating defaults on first use.
func (db *DB) GetMailSettings(ctx context.Context) (*MailSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO mail_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	var s MailSettings
	err := db.sql.QueryRowContext(ctx, `SELECT enabled, hostname, updated_at FROM mail_settings WHERE id = 1`).Scan(&s.Enabled, &s.Hostname, &s.UpdatedAt)
	return &s, mapErr(err)
}

// SaveMailSettings persists s.
func (db *DB) SaveMailSettings(ctx context.Context, s *MailSettings) error {
	if _, err := db.GetMailSettings(ctx); err != nil {
		return err
	}
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE mail_settings SET enabled=?, hostname=?, updated_at=? WHERE id=1`, s.Enabled, s.Hostname, s.UpdatedAt)
	return err
}

const mailDomainSelect = `SELECT m.id, m.domain_id, d.name, m.dkim_selector, m.dkim_key_enc, m.created_at FROM mail_domains m JOIN domains d ON d.id = m.domain_id`

func scanMailDomain(row interface{ Scan(...any) error }) (*MailDomain, error) {
	var m MailDomain
	if err := row.Scan(&m.ID, &m.DomainID, &m.Name, &m.DKIMSelector, &m.DKIMKeyEnc, &m.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

// CreateMailDomain enables mail for a website.
func (db *DB) CreateMailDomain(ctx context.Context, m *MailDomain) error {
	m.CreatedAt = now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO mail_domains (domain_id, dkim_selector, dkim_key_enc, created_at) VALUES (?,?,?,?)`,
		m.DomainID, m.DKIMSelector, m.DKIMKeyEnc, m.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	m.ID, err = res.LastInsertId()
	return err
}

// GetMailDomainByDomain returns a website's mail domain.
func (db *DB) GetMailDomainByDomain(ctx context.Context, domainID int64) (*MailDomain, error) {
	return scanMailDomain(db.sql.QueryRowContext(ctx, mailDomainSelect+` WHERE m.domain_id = ?`, domainID))
}

// ListMailDomains returns every mail domain.
func (db *DB) ListMailDomains(ctx context.Context) ([]*MailDomain, error) {
	rows, err := db.sql.QueryContext(ctx, mailDomainSelect+` ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MailDomain{}
	for rows.Next() {
		m, err := scanMailDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMailDomain disables mail for a website (mailboxes and aliases go with it).
func (db *DB) DeleteMailDomain(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM mail_domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

const mailboxCols = `id, mail_domain_id, local_part, password_hash, quota_mb, enabled, created_at, updated_at`

func scanMailbox(row interface{ Scan(...any) error }) (*Mailbox, error) {
	var m Mailbox
	if err := row.Scan(&m.ID, &m.MailDomainID, &m.LocalPart, &m.PasswordHash, &m.QuotaMB, &m.Enabled, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

// CreateMailbox inserts m; a duplicate address is ErrConflict.
func (db *DB) CreateMailbox(ctx context.Context, m *Mailbox) error {
	m.CreatedAt, m.UpdatedAt = now(), now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO mailboxes (mail_domain_id, local_part, password_hash, quota_mb, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		m.MailDomainID, m.LocalPart, m.PasswordHash, m.QuotaMB, m.Enabled, m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	m.ID, err = res.LastInsertId()
	return err
}

// GetMailbox returns one mailbox.
func (db *DB) GetMailbox(ctx context.Context, id int64) (*Mailbox, error) {
	return scanMailbox(db.sql.QueryRowContext(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE id = ?`, id))
}

// ListMailboxes returns a domain's mailboxes, or all of them when domainID is 0.
func (db *DB) ListMailboxes(ctx context.Context, mailDomainID int64) ([]*Mailbox, error) {
	q, args := `SELECT `+mailboxCols+` FROM mailboxes ORDER BY mail_domain_id, local_part`, []any{}
	if mailDomainID > 0 {
		q, args = `SELECT `+mailboxCols+` FROM mailboxes WHERE mail_domain_id = ? ORDER BY local_part`, []any{mailDomainID}
	}
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMailbox persists password, quota and enabled.
func (db *DB) UpdateMailbox(ctx context.Context, m *Mailbox) error {
	m.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE mailboxes SET password_hash=?, quota_mb=?, enabled=?, updated_at=? WHERE id=?`, m.PasswordHash, m.QuotaMB, m.Enabled, m.UpdatedAt, m.ID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// DeleteMailbox removes a mailbox.
func (db *DB) DeleteMailbox(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM mailboxes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

const aliasCols = `id, mail_domain_id, local_part, destinations, created_at, updated_at`

func scanAlias(row interface{ Scan(...any) error }) (*MailAlias, error) {
	var a MailAlias
	var dest string
	if err := row.Scan(&a.ID, &a.MailDomainID, &a.LocalPart, &dest, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	a.Destinations = splitList(dest)
	return &a, nil
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinList(l []string) string { return strings.Join(l, ",") }

// CreateMailAlias inserts a; a duplicate address is ErrConflict.
func (db *DB) CreateMailAlias(ctx context.Context, a *MailAlias) error {
	a.CreatedAt, a.UpdatedAt = now(), now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO mail_aliases (mail_domain_id, local_part, destinations, created_at, updated_at) VALUES (?,?,?,?,?)`,
		a.MailDomainID, a.LocalPart, joinList(a.Destinations), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	a.ID, err = res.LastInsertId()
	return err
}

// GetMailAlias returns one alias.
func (db *DB) GetMailAlias(ctx context.Context, id int64) (*MailAlias, error) {
	return scanAlias(db.sql.QueryRowContext(ctx, `SELECT `+aliasCols+` FROM mail_aliases WHERE id = ?`, id))
}

// ListMailAliases returns a domain's aliases, or all of them when domainID is 0.
func (db *DB) ListMailAliases(ctx context.Context, mailDomainID int64) ([]*MailAlias, error) {
	q, args := `SELECT `+aliasCols+` FROM mail_aliases ORDER BY mail_domain_id, local_part`, []any{}
	if mailDomainID > 0 {
		q, args = `SELECT `+aliasCols+` FROM mail_aliases WHERE mail_domain_id = ? ORDER BY local_part`, []any{mailDomainID}
	}
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MailAlias{}
	for rows.Next() {
		a, err := scanAlias(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateMailAlias persists the destinations.
func (db *DB) UpdateMailAlias(ctx context.Context, a *MailAlias) error {
	a.UpdatedAt = now()
	res, err := db.sql.ExecContext(ctx, `UPDATE mail_aliases SET destinations=?, updated_at=? WHERE id=?`, joinList(a.Destinations), a.UpdatedAt, a.ID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// DeleteMailAlias removes an alias.
func (db *DB) DeleteMailAlias(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM mail_aliases WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}
