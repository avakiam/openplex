package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AlertSettings configure the e-mail alerts (a single row).
type AlertSettings struct {
	Enabled         bool      `json:"enabled"`
	Recipients      []string  `json:"recipients"`
	Language        string    `json:"language"` // en | es | ca
	Events          []string  `json:"events"`   // alert kinds to send
	Transport       string    `json:"transport"`
	FromAddress     string    `json:"from_address"` // empty: openplex@<panel host>
	SMTPHost        string    `json:"smtp_host"`
	SMTPPort        int       `json:"smtp_port"`
	SMTPSecurity    string    `json:"smtp_security"` // starttls | tls | none
	SMTPUser        string    `json:"smtp_user"`
	SMTPPasswordEnc string    `json:"-"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DefaultAlertEvents are the alert kinds sent until the administrator chooses.
const DefaultAlertEvents = "backup_failed,site_down,disk_full,database_full,cert_expiring,update_available,update_failed"

// GetAlertSettings returns the settings, creating defaults on first use.
func (db *DB) GetAlertSettings(ctx context.Context) (*AlertSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO alert_settings (id, events, updated_at) VALUES (1, ?, ?)`, DefaultAlertEvents, now()); err != nil {
		return nil, err
	}
	var s AlertSettings
	var recipients, events string
	err := db.sql.QueryRowContext(ctx, `SELECT enabled, recipients, language, events, transport, from_address, smtp_host, smtp_port,
		smtp_security, smtp_user, smtp_password_enc, updated_at FROM alert_settings WHERE id = 1`).Scan(&s.Enabled, &recipients,
		&s.Language, &events, &s.Transport, &s.FromAddress, &s.SMTPHost, &s.SMTPPort, &s.SMTPSecurity, &s.SMTPUser, &s.SMTPPasswordEnc, &s.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	s.Recipients, s.Events = splitList(recipients), splitList(events)
	return &s, nil
}

// SaveAlertSettings persists s (callers validate it first).
func (db *DB) SaveAlertSettings(ctx context.Context, s *AlertSettings) error {
	if _, err := db.GetAlertSettings(ctx); err != nil {
		return err
	}
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE alert_settings SET enabled=?, recipients=?, language=?, events=?, transport=?, from_address=?,
		smtp_host=?, smtp_port=?, smtp_security=?, smtp_user=?, smtp_password_enc=?, updated_at=? WHERE id = 1`,
		s.Enabled, strings.Join(s.Recipients, ","), s.Language, strings.Join(s.Events, ","), s.Transport, s.FromAddress,
		s.SMTPHost, s.SMTPPort, s.SMTPSecurity, s.SMTPUser, s.SMTPPasswordEnc, s.UpdatedAt)
	return err
}

// Alert states: an ongoing problem (firing → resolved) or a one-off event (sent).
const (
	AlertFiring   = "firing"
	AlertResolved = "resolved"
	AlertSent     = "sent"
)

// Alert is one problem or event the panel told the admins about.
type Alert struct {
	Key        string     `json:"key"`
	Kind       string     `json:"kind"`
	Subject    string     `json:"subject"`
	State      string     `json:"state"`
	FirstAt    time.Time  `json:"first_at"`
	LastSentAt *time.Time `json:"last_sent_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	SendError  string     `json:"send_error"`
}

const alertCols = `key, kind, subject, state, first_at, last_sent_at, resolved_at, send_error`

func scanAlert(row interface{ Scan(...any) error }) (*Alert, error) {
	var a Alert
	var sent, resolved sql.NullTime
	if err := row.Scan(&a.Key, &a.Kind, &a.Subject, &a.State, &a.FirstAt, &sent, &resolved, &a.SendError); err != nil {
		return nil, mapErr(err)
	}
	if sent.Valid {
		a.LastSentAt = &sent.Time
	}
	if resolved.Valid {
		a.ResolvedAt = &resolved.Time
	}
	return &a, nil
}

// GetAlert returns the alert with key (ErrNotFound if none).
func (db *DB) GetAlert(ctx context.Context, key string) (*Alert, error) {
	return scanAlert(db.sql.QueryRowContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE key = ?`, key))
}

// PutAlert inserts or replaces a.
func (db *DB) PutAlert(ctx context.Context, a *Alert) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO alerts (`+alertCols+`) VALUES (?,?,?,?,?,?,?,?)
	ON CONFLICT (key) DO UPDATE SET kind=excluded.kind, subject=excluded.subject, state=excluded.state, first_at=excluded.first_at,
	last_sent_at=excluded.last_sent_at, resolved_at=excluded.resolved_at, send_error=excluded.send_error`,
		a.Key, a.Kind, a.Subject, a.State, a.FirstAt, a.LastSentAt, a.ResolvedAt, a.SendError)
	return err
}

// FiringAlerts lists the ongoing problems.
func (db *DB) FiringAlerts(ctx context.Context) ([]*Alert, error) {
	return db.listAlerts(ctx, `SELECT `+alertCols+` FROM alerts WHERE state = ? ORDER BY first_at`, AlertFiring)
}

// RecentAlerts lists the newest alerts first.
func (db *DB) RecentAlerts(ctx context.Context, limit int) ([]*Alert, error) {
	return db.listAlerts(ctx, `SELECT `+alertCols+` FROM alerts ORDER BY first_at DESC, key LIMIT ?`, limit)
}

func (db *DB) listAlerts(ctx context.Context, q string, args ...any) ([]*Alert, error) {
	rows, err := db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneAlerts forgets alerts that ended (or were sent) before t.
func (db *DB) PruneAlerts(ctx context.Context, t time.Time) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM alerts WHERE state != ? AND COALESCE(resolved_at, first_at) < ?`, AlertFiring, t)
	return err
}
