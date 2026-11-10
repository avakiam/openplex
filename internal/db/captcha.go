package db

import (
	"context"
	"time"
)

// CaptchaSettings is the single login-captcha configuration row.
type CaptchaSettings struct {
	Provider  string    `json:"provider"` // "" (none), turnstile, hcaptcha, recaptcha
	SiteKey   string    `json:"site_key"`
	SecretEnc string    `json:"-"`       // sealed with secrets.Box
	Panel     bool      `json:"panel"`   // asked on the panel's login page
	Webmail   bool      `json:"webmail"` // asked on the webmail's login page
	UpdatedAt time.Time `json:"updated_at"`
}

// GetCaptchaSettings returns the settings, creating defaults on first use.
func (db *DB) GetCaptchaSettings(ctx context.Context) (*CaptchaSettings, error) {
	if _, err := db.sql.ExecContext(ctx, `INSERT OR IGNORE INTO captcha_settings (id, updated_at) VALUES (1, ?)`, now()); err != nil {
		return nil, err
	}
	var s CaptchaSettings
	err := db.sql.QueryRowContext(ctx, `SELECT provider, site_key, secret_enc, panel, webmail, updated_at FROM captcha_settings WHERE id = 1`).
		Scan(&s.Provider, &s.SiteKey, &s.SecretEnc, &s.Panel, &s.Webmail, &s.UpdatedAt)
	return &s, mapErr(err)
}

// SaveCaptchaSettings persists s.
func (db *DB) SaveCaptchaSettings(ctx context.Context, s *CaptchaSettings) error {
	if _, err := db.GetCaptchaSettings(ctx); err != nil {
		return err
	}
	s.UpdatedAt = now()
	_, err := db.sql.ExecContext(ctx, `UPDATE captcha_settings SET provider=?, site_key=?, secret_enc=?, panel=?, webmail=?, updated_at=? WHERE id=1`,
		s.Provider, s.SiteKey, s.SecretEnc, s.Panel, s.Webmail, s.UpdatedAt)
	return err
}
