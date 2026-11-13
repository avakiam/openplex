// Package alerts e-mails the administrators about problems on the server: a
// failed backup, a website that is down, a disk almost full, a certificate
// that is not renewing, and panel updates. It checks every minute; an
// ongoing problem is sent once, reminded every day while it lasts and
// reported again when it is over. Its state lives in the database, so a
// restart does not send anything twice.
package alerts

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/secrets"
	"openplex.dev/openplex/internal/update"
)

// Timing and thresholds.
const (
	checkEvery    = time.Minute
	siteDownAfter = 2 * time.Minute // a deploy or restart may briefly show an error
	remindEvery   = 24 * time.Hour
	diskFire      = 90 // percent used
	diskClear     = 85
	keepHistory   = 90 * 24 * time.Hour
)

// Options wire the manager to the rest of the panel.
type Options struct {
	DB      *db.DB
	Box     *secrets.Box
	Log     *slog.Logger
	Server  string // name of this server in the e-mails (panel host or hostname)
	URL     string // panel address, "" if unknown
	CertDir string // Caddy's certificate storage
	// Panel host names that have a certificate besides the websites (panel, webmail…).
	ExtraHosts func(ctx context.Context) []string
	// Disk returns the use of the data disk (ok=false while unknown).
	Disk func() (used, total uint64, ok bool)
	// Update returns the panel update status (nil when updates are off).
	Update func(ctx context.Context) *update.Status
	// LocalSend hands a message to this server's own mail service.
	LocalSend func(ctx context.Context, from string, to []string, msg []byte) error
}

// Manager runs the checks and sends the e-mails.
type Manager struct {
	o       Options
	mu      sync.Mutex
	pending map[string]time.Time // website down since (not yet long enough to alert)
}

// New returns a manager.
func New(o Options) *Manager { return &Manager{o: o, pending: map[string]time.Time{}} }

// Run checks every minute until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Check(ctx); err != nil && ctx.Err() == nil {
				m.o.Log.Warn("alerts check", "err", err)
			}
		}
	}
}

// DefaultFrom is the sender used when none is set.
func (m *Manager) DefaultFrom() string { return m.from(&db.AlertSettings{}) }

func (m *Manager) from(s *db.AlertSettings) string {
	if s.FromAddress != "" {
		return s.FromAddress
	}
	host := m.o.Server
	if !strings.Contains(host, ".") {
		host += ".localdomain"
	}
	return "openplex@" + host
}

// send delivers one e-mail to the recipients.
func (m *Manager) send(ctx context.Context, s *db.AlertSettings, subject, body string) error {
	if m.o.URL != "" {
		body += "\n\n" + text(s.Language, "panel", map[string]string{"url": m.o.URL})
	}
	from := m.from(s)
	msg := message(from, s.Recipients, subject, body)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if s.Transport == "smtp" {
		pw := ""
		if s.SMTPPasswordEnc != "" {
			var err error
			if pw, err = m.o.Box.Open(s.SMTPPasswordEnc); err != nil {
				return err
			}
		}
		helo := m.o.Server
		if !strings.Contains(helo, ".") {
			_, helo, _ = strings.Cut(from, "@")
		}
		return sendSMTP(ctx, s, helo, pw, from, s.Recipients, msg)
	}
	if m.o.LocalSend == nil {
		return errors.New("this server's mail service is not available: send through an SMTP server")
	}
	return m.o.LocalSend(ctx, from, s.Recipients, msg)
}

// Test sends a test e-mail with the given settings.
func (m *Manager) Test(ctx context.Context, s *db.AlertSettings) error {
	if len(s.Recipients) == 0 {
		return errors.New("add at least one recipient")
	}
	v := map[string]string{"server": m.o.Server}
	return m.send(ctx, s, text(s.Language, "test.subject", v), text(s.Language, "test.body", v))
}

// Check runs every check once.
func (m *Manager) Check(ctx context.Context) error {
	s, err := m.o.DB.GetAlertSettings(ctx)
	if err != nil || !s.Enabled || len(s.Recipients) == 0 {
		return err
	}
	on := func(kind string) bool { return slices.Contains(s.Events, kind) }
	var errs []error
	if on(KindBackupFailed) {
		errs = append(errs, m.checkBackups(ctx, s))
	}
	if on(KindSiteDown) {
		errs = append(errs, m.checkSites(ctx, s))
	}
	if on(KindDiskFull) {
		errs = append(errs, m.checkDisk(ctx, s))
	}
	if on(KindDatabaseFull) {
		errs = append(errs, m.checkDatabases(ctx, s))
	}
	if on(KindCertExpiring) {
		errs = append(errs, m.checkCerts(ctx, s))
	}
	if on(KindUpdateAvailable) || on(KindUpdateFailed) {
		errs = append(errs, m.checkUpdates(ctx, s, on(KindUpdateAvailable), on(KindUpdateFailed)))
	}
	errs = append(errs, m.o.DB.PruneAlerts(ctx, time.Now().Add(-keepHistory)))
	return errors.Join(errs...)
}

// event sends a one-off alert once.
func (m *Manager) event(ctx context.Context, s *db.AlertSettings, key, kind, subject, body string) error {
	if _, err := m.o.DB.GetAlert(ctx, key); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return err
	}
	now := time.Now().UTC()
	a := &db.Alert{Key: key, Kind: kind, Subject: subject, State: db.AlertSent, FirstAt: now, LastSentAt: &now}
	if err := m.send(ctx, s, subject, body); err != nil {
		a.SendError = err.Error()
	}
	return m.o.DB.PutAlert(ctx, a)
}

// Extension sends an extension's alert once per key, when alerts are on.
// It reports whether an e-mail went out (false: off, no recipients, or
// already sent for that key).
func (m *Manager) Extension(ctx context.Context, id, name, key, subject, body string) (bool, error) {
	s, err := m.o.DB.GetAlertSettings(ctx)
	if err != nil || !s.Enabled || len(s.Recipients) == 0 {
		return false, err
	}
	key = "ext:" + id + ":" + key
	if _, err := m.o.DB.GetAlert(ctx, key); err == nil {
		return false, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return false, err
	}
	body = strings.TrimSpace(body) + "\n\n" + text(s.Language, "extension", map[string]string{"name": name})
	now := time.Now().UTC()
	a := &db.Alert{Key: key, Kind: KindExtension, Subject: subject, State: db.AlertSent, FirstAt: now, LastSentAt: &now}
	sendErr := m.send(ctx, s, subject, body)
	if sendErr != nil {
		a.SendError = sendErr.Error()
	}
	if err := m.o.DB.PutAlert(ctx, a); err != nil {
		return false, err
	}
	return sendErr == nil, sendErr
}

// firing reports an ongoing problem: sent when it starts, then once a day.
func (m *Manager) firing(ctx context.Context, s *db.AlertSettings, key, kind, subject, body string) error {
	now := time.Now().UTC()
	a, err := m.o.DB.GetAlert(ctx, key)
	switch {
	case errors.Is(err, db.ErrNotFound) || (err == nil && a.State != db.AlertFiring):
		a = &db.Alert{Key: key, Kind: kind, Subject: subject, State: db.AlertFiring, FirstAt: now}
	case err != nil:
		return err
	case a.LastSentAt != nil && now.Sub(*a.LastSentAt) < remindEvery:
		return nil
	default:
		subject = text(s.Language, "reminder", nil) + subject
	}
	a.LastSentAt, a.SendError = &now, ""
	if err := m.send(ctx, s, subject, body); err != nil {
		a.SendError = err.Error()
	}
	return m.o.DB.PutAlert(ctx, a)
}

// resolveMissing ends the firing alerts of kind whose key is not in active,
// with the e-mail built by msg (subject, body; empty subject: no e-mail).
func (m *Manager) resolveMissing(ctx context.Context, s *db.AlertSettings, kind string, active map[string]bool, msg func(a *db.Alert) (string, string)) error {
	list, err := m.o.DB.FiringAlerts(ctx)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Kind != kind || active[a.Key] {
			continue
		}
		now := time.Now().UTC()
		a.State, a.ResolvedAt, a.SendError = db.AlertResolved, &now, ""
		if subject, body := msg(a); subject != "" {
			a.LastSentAt = &now
			if err := m.send(ctx, s, subject, body); err != nil {
				a.SendError = err.Error()
			}
		}
		if err := m.o.DB.PutAlert(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) when(s *db.AlertSettings, t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// checkBackups reports failed backups of the last day, once each.
func (m *Manager) checkBackups(ctx context.Context, s *db.AlertSettings) error {
	runs, err := m.o.DB.ListBackupRuns(ctx, nil, 50)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Kind != "backup" || r.Status != "error" || r.FinishedAt == nil || time.Since(*r.FinishedAt) > 24*time.Hour {
			continue
		}
		scope := r.Scope
		if scope == "all" {
			scope = text(s.Language, "scope.all", nil)
		}
		v := map[string]string{"server": m.o.Server, "scope": scope, "time": m.when(s, *r.FinishedAt),
			"trigger": text(s.Language, "trigger."+r.Trigger, nil), "message": r.Message}
		if err := m.event(ctx, s, "backup:"+strconv.FormatInt(r.ID, 10), KindBackupFailed,
			text(s.Language, "backup_failed.subject", v), text(s.Language, "backup_failed.body", v)); err != nil {
			return err
		}
	}
	return nil
}

// checkSites reports websites in error for siteDownAfter or longer.
func (m *Manager) checkSites(ctx context.Context, s *db.AlertSettings) error {
	ds, err := m.o.DB.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return err
	}
	type downSite struct {
		d     *db.Domain
		since time.Time
	}
	now := time.Now()
	active := map[string]bool{}
	var down []downSite
	m.mu.Lock()
	for _, d := range ds {
		if d.Kind == db.KindAlias || d.Status != db.StatusError {
			continue
		}
		key := "site:" + d.Name
		since, ok := m.pending[key]
		if !ok {
			m.pending[key], since = now, now
		}
		active[key] = true // still pending counts as ongoing
		if now.Sub(since) >= siteDownAfter {
			down = append(down, downSite{d, since})
		}
	}
	for k := range m.pending {
		if !active[k] {
			delete(m.pending, k)
		}
	}
	m.mu.Unlock()
	for _, x := range down {
		v := map[string]string{"site": x.d.Name, "time": m.when(s, x.since), "message": x.d.StatusMessage}
		if err := m.firing(ctx, s, "site:"+x.d.Name, KindSiteDown,
			text(s.Language, "site_down.subject", v), text(s.Language, "site_down.body", v)); err != nil {
			return err
		}
	}
	return m.resolveMissing(ctx, s, KindSiteDown, active, func(a *db.Alert) (string, string) {
		v := map[string]string{"site": strings.TrimPrefix(a.Key, "site:")}
		return text(s.Language, "site_down.resolved", v), text(s.Language, "site_down.resolved_body", v)
	})
}

// checkDatabases reports the databases made read-only for being over their size limit.
func (m *Manager) checkDatabases(ctx context.Context, s *db.AlertSettings) error {
	dbs, err := m.o.DB.ListDatabases(ctx, nil)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, d := range dbs {
		if !d.SizeLocked {
			continue
		}
		key := "database:" + d.Name
		active[key] = true
		owner, _ := m.o.DB.GetUser(ctx, d.OwnerID)
		v := map[string]string{"database": d.Name, "limit": strconv.Itoa(d.LimitMB(owner)) + " MB", "owner": ""}
		if owner != nil {
			v["owner"] = owner.Username
		}
		if err := m.firing(ctx, s, key, KindDatabaseFull,
			text(s.Language, "database_full.subject", v), text(s.Language, "database_full.body", v)); err != nil {
			return err
		}
	}
	return m.resolveMissing(ctx, s, KindDatabaseFull, active, func(a *db.Alert) (string, string) {
		v := map[string]string{"database": strings.TrimPrefix(a.Key, "database:")}
		return text(s.Language, "database_full.resolved", v), text(s.Language, "database_full.resolved_body", v)
	})
}

func human(b uint64) string {
	const g = 1 << 30
	if b >= 10*g {
		return strconv.FormatUint(b/g, 10) + " GB"
	}
	return strconv.FormatFloat(float64(b)/g, 'f', 1, 64) + " GB"
}

// checkDisk reports the data disk above diskFire% until it drops below diskClear%.
func (m *Manager) checkDisk(ctx context.Context, s *db.AlertSettings) error {
	if m.o.Disk == nil {
		return nil
	}
	used, total, ok := m.o.Disk()
	if !ok || total == 0 {
		return nil
	}
	pct := int(used * 100 / total)
	v := map[string]string{"server": m.o.Server, "pct": strconv.Itoa(pct), "free": human(total - used)}
	cur, err := m.o.DB.GetAlert(ctx, "disk")
	firing := err == nil && cur.State == db.AlertFiring
	switch {
	case pct >= diskFire:
		return m.firing(ctx, s, "disk", KindDiskFull, text(s.Language, "disk_full.subject", v), text(s.Language, "disk_full.body", v))
	case firing && pct < diskClear:
		return m.resolveMissing(ctx, s, KindDiskFull, nil, func(*db.Alert) (string, string) {
			return text(s.Language, "disk_full.resolved", v), text(s.Language, "disk_full.resolved_body", v)
		})
	}
	return nil
}

// checkCerts reports certificates of names still served that are close to
// expiring: Caddy renews when a third of the lifetime is left, so one with
// less than a sixth left has failed to renew for a while.
func (m *Manager) checkCerts(ctx context.Context, s *db.AlertSettings) error {
	served, err := m.servedHosts(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	newest := map[string]*x509.Certificate{} // a renewed name may keep an older file from another issuer
	_ = filepath.WalkDir(m.o.CertDir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".crt") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil
		}
		host := strings.TrimSuffix(filepath.Base(p), ".crt")
		if !served[host] {
			return nil
		}
		if old := newest[host]; old == nil || c.NotAfter.After(old.NotAfter) {
			newest[host] = c
		}
		return nil
	})
	active := map[string]bool{}
	for host, c := range newest {
		life := c.NotAfter.Sub(c.NotBefore)
		if c.NotAfter.Sub(now) >= life/6 {
			continue
		}
		key := "cert:" + host
		active[key] = true
		v := map[string]string{"host": host, "date": c.NotAfter.UTC().Format("2006-01-02")}
		if err := m.firing(ctx, s, key, KindCertExpiring, text(s.Language, "cert_expiring.subject", v), text(s.Language, "cert_expiring.body", v)); err != nil {
			return err
		}
	}
	return m.resolveMissing(ctx, s, KindCertExpiring, active, func(a *db.Alert) (string, string) {
		host := strings.TrimPrefix(a.Key, "cert:")
		c := newest[host]
		if c == nil {
			return "", "" // the name is not served any more: nothing to report
		}
		v := map[string]string{"host": host, "date": c.NotAfter.UTC().Format("2006-01-02")}
		return text(s.Language, "cert_expiring.resolved", v), text(s.Language, "cert_expiring.resolved_body", v)
	})
}

// servedHosts are the names that should have a valid certificate: every
// website and alias (with www., mail. and webmail.) and the panel's own names.
func (m *Manager) servedHosts(ctx context.Context) (map[string]bool, error) {
	ds, err := m.o.DB.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, d := range ds {
		if d.Status == db.StatusDisabled {
			continue
		}
		for _, p := range []string{"", "www.", "mail.", "webmail."} {
			out[p+d.Name] = true
		}
	}
	if m.o.ExtraHosts != nil {
		for _, h := range m.o.ExtraHosts(ctx) {
			out[h] = true
		}
	}
	return out, nil
}

// checkUpdates reports a new stable release once, and an update that was
// rolled back or failed once.
func (m *Manager) checkUpdates(ctx context.Context, s *db.AlertSettings, available, failed bool) error {
	if m.o.Update == nil {
		return nil
	}
	st := m.o.Update(ctx)
	if st == nil {
		return nil
	}
	if available && st.Available && st.Latest != nil {
		notes := st.Latest.Notes
		if len(notes) > 3000 {
			notes = notes[:3000] + "…"
		}
		v := map[string]string{"version": st.Latest.Version, "current": st.Current, "notes": notes}
		if err := m.event(ctx, s, "update:"+st.Latest.Version, KindUpdateAvailable,
			text(s.Language, "update_available.subject", v), text(s.Language, "update_available.body", v)); err != nil {
			return err
		}
	}
	if o := st.Last; failed && o != nil && o.Result != "updated" && time.Since(o.Time) < 7*24*time.Hour {
		v := map[string]string{"version": o.To, "from": o.From, "result": o.Result, "message": o.Message}
		return m.event(ctx, s, fmt.Sprintf("update-outcome:%s:%d", o.To, o.Time.Unix()), KindUpdateFailed,
			text(s.Language, "update_failed.subject", v), text(s.Language, "update_failed.body", v))
	}
	return nil
}
