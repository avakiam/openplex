package mail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dns"
	"openplex.dev/openplex/internal/secrets"
)

// Server runs the mail container (the Docker orchestrator).
type Server interface {
	EnsureMail(ctx context.Context, hostname, configDir string) error
	StopMail(ctx context.Context) error
	MailStatus(ctx context.Context) (running bool, health string)
	MailExec(ctx context.Context, cmd ...string) (string, error)
}

// DNS is the part of the DNS manager mail needs.
type DNS interface {
	ZoneFor(ctx context.Context, host string) (*db.DNSZone, error)
	Changed()
}

// Status is the mail service as the dashboard shows it.
type Status struct {
	db.MailSettings
	Available   bool       `json:"available"`
	Running     bool       `json:"running"`
	Health      string     `json:"health"`
	LastError   string     `json:"last_error"`
	LastSync    *time.Time `json:"last_sync"`
	Certificate string     `json:"certificate"` // letsencrypt | self-signed | none
	ServerIPs   []string   `json:"server_ips"`
	PTR         []string   `json:"ptr"`     // reverse DNS of the server addresses
	PTROK       bool       `json:"ptr_ok"`  // every address points back at the hostname
	Forward     []string   `json:"forward"` // what the hostname resolves to
}

// Manager keeps docker-mailserver in sync with the database.
type Manager struct {
	db        *db.DB
	srv       Server // nil without Docker
	dns       DNS
	box       *secrets.Box
	log       *slog.Logger
	configDir string // host path mounted as /tmp/docker-mailserver
	certDir   string // Caddy's certificate store on the host
	edge      func() // re-renders the edge (the mail host gets a certificate there)
	kick      chan struct{}

	syncMu   sync.Mutex
	mu       sync.Mutex
	lastErr  string
	lastSync *time.Time
	resolver *net.Resolver
}

// New creates a Manager. srv may be nil; edge may be nil.
func New(store *db.DB, srv Server, d DNS, box *secrets.Box, dataDir, caddyDir string, edge func(), log *slog.Logger) *Manager {
	return &Manager{
		db: store, srv: srv, dns: d, box: box, log: log, edge: edge,
		configDir: filepath.Join(dataDir, "mail", "config"),
		certDir:   filepath.Join(caddyDir, "data", "caddy", "certificates"),
		kick:      make(chan struct{}, 1), resolver: net.DefaultResolver,
	}
}

// Changed asks for a sync soon.
func (m *Manager) Changed() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run syncs at start, on every change and every 10 minutes (certificate renewals).
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		m.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
			time.Sleep(300 * time.Millisecond)
		case <-t.C:
		}
	}
}

func (m *Manager) setErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = err.Error()
		return
	}
	now := time.Now().UTC()
	m.lastErr, m.lastSync = "", &now
}

// Sync writes the configuration and makes sure the container runs (or not).
func (m *Manager) Sync(ctx context.Context) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	err := m.sync(ctx)
	if err != nil {
		m.log.Error("mail: sync", "err", err)
	}
	m.setErr(err)
}

func (m *Manager) sync(ctx context.Context) error {
	s, err := m.db.GetMailSettings(ctx)
	if err != nil {
		return err
	}
	if m.srv == nil {
		return errors.New("Docker is not available on this server.")
	}
	if !s.Enabled {
		if running, _ := m.srv.MailStatus(ctx); running {
			return m.srv.StopMail(ctx)
		}
		return nil
	}
	snap, err := m.snapshot(ctx)
	if err != nil {
		return err
	}
	files, err := Files(snap)
	if err != nil {
		return err
	}
	if err := m.certFiles(s.Hostname, files); err != nil {
		return err
	}
	if _, err := WriteFiles(m.configDir, files); err != nil {
		return fmt.Errorf("write mail configuration: %w", err)
	}
	return m.srv.EnsureMail(ctx, s.Hostname, m.configDir)
}

func (m *Manager) snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	doms, err := m.db.ListMailDomains(ctx)
	if err != nil {
		return snap, err
	}
	names := map[int64]string{}
	for _, d := range doms {
		key, err := m.box.Open(d.DKIMKeyEnc)
		if err != nil {
			return snap, fmt.Errorf("dkim key of %s: %w", d.Name, err)
		}
		names[d.ID] = d.Name
		snap.Domains = append(snap.Domains, DomainConfig{Name: d.Name, Selector: d.DKIMSelector, KeyPEM: key})
	}
	mbs, err := m.db.ListMailboxes(ctx, 0)
	if err != nil {
		return snap, err
	}
	for _, b := range mbs {
		if b.Enabled && names[b.MailDomainID] != "" {
			snap.Mailboxes = append(snap.Mailboxes, MailboxConfig{Address: b.LocalPart + "@" + names[b.MailDomainID], Hash: b.PasswordHash, QuotaMB: b.QuotaMB})
		}
	}
	als, err := m.db.ListMailAliases(ctx, 0)
	if err != nil {
		return snap, err
	}
	for _, a := range als {
		if names[a.MailDomainID] != "" {
			snap.Aliases = append(snap.Aliases, AliasConfig{Address: a.LocalPart + "@" + names[a.MailDomainID], Destinations: a.Destinations})
		}
	}
	return snap, nil
}

// certFiles adds the mail host's certificate: Caddy's (it obtains one because
// the edge serves the mail host), else a self-signed one until Caddy has it.
func (m *Manager) certFiles(host string, files map[string][]byte) error {
	if crt, key, ok := m.caddyCert(host); ok {
		files["ssl/cert.pem"], files["ssl/key.pem"] = crt, key
		return nil
	}
	if _, err := os.Stat(filepath.Join(m.configDir, "ssl", "cert.pem")); err == nil {
		return nil // keep the temporary certificate already there
	}
	crt, key, err := selfSigned(host)
	if err != nil {
		return err
	}
	files["ssl/cert.pem"], files["ssl/key.pem"] = crt, key
	return nil
}

func (m *Manager) caddyCert(host string) (crt, key []byte, ok bool) {
	matches, _ := filepath.Glob(filepath.Join(m.certDir, "*", host, host+".crt"))
	for _, c := range matches {
		k := strings.TrimSuffix(c, ".crt") + ".key"
		crt, err1 := os.ReadFile(c)
		key, err2 := os.ReadFile(k)
		if err1 == nil && err2 == nil {
			return crt, key, true
		}
	}
	return nil, nil, false
}

func selfSigned(host string) ([]byte, []byte, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}

// Status returns the service state, including reverse-DNS checks.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	s, err := m.db.GetMailSettings(ctx)
	if err != nil {
		return nil, err
	}
	st := &Status{MailSettings: *s, Available: m.srv != nil, Certificate: "none", ServerIPs: []string{}, PTR: []string{}, Forward: []string{}}
	if m.srv != nil {
		st.Running, st.Health = m.srv.MailStatus(ctx)
	}
	if s.Hostname != "" {
		if _, _, ok := m.caddyCert(s.Hostname); ok {
			st.Certificate = "letsencrypt"
		} else if _, err := os.Stat(filepath.Join(m.configDir, "ssl", "cert.pem")); err == nil {
			st.Certificate = "self-signed"
		}
	}
	m.mu.Lock()
	st.LastError, st.LastSync = m.lastErr, m.lastSync
	m.mu.Unlock()
	ds, err := m.db.GetDNSSettings(ctx)
	if err == nil && s.Hostname != "" {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		st.PTROK = true
		for _, ip := range []string{ds.IPv4, ds.IPv6} {
			if ip == "" {
				continue
			}
			st.ServerIPs = append(st.ServerIPs, ip)
			names, _ := m.resolver.LookupAddr(cctx, ip)
			match := false
			for _, n := range names {
				n = strings.TrimSuffix(strings.ToLower(n), ".")
				st.PTR = append(st.PTR, ip+" → "+n)
				match = match || n == s.Hostname
			}
			st.PTROK = st.PTROK && match
		}
		st.PTROK = st.PTROK && len(st.ServerIPs) > 0
		if addrs, err := m.resolver.LookupHost(cctx, s.Hostname); err == nil {
			st.Forward = addrs
		}
	}
	return st, nil
}

// UpdateSettings validates and stores the settings.
func (m *Manager) UpdateSettings(ctx context.Context, in db.MailSettings) error {
	in.Hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Hostname)), ".")
	if in.Hostname != "" && !dns.ValidHost(in.Hostname) {
		return errors.New("enter the mail server's host name, such as mail.example.com")
	}
	if in.Enabled && in.Hostname == "" {
		return errors.New("to turn mail on, enter the mail server's host name")
	}
	old, err := m.db.GetMailSettings(ctx)
	if err != nil {
		return err
	}
	if err := m.db.SaveMailSettings(ctx, &in); err != nil {
		return err
	}
	if old.Hostname != in.Hostname {
		// MX records of every mail domain follow the new host.
		doms, err := m.db.ListMailDomains(ctx)
		if err != nil {
			return err
		}
		for _, d := range doms {
			if err := m.db.RetargetMX(ctx, Source(d.DomainID), in.Hostname); err != nil {
				return err
			}
		}
	}
	m.Changed()
	if m.dns != nil {
		m.dns.Changed()
	}
	if m.edge != nil {
		m.edge()
	}
	return nil
}

// EnableDomain starts receiving mail for a website: DKIM key + DNS records.
func (m *Manager) EnableDomain(ctx context.Context, d *db.Domain) (*db.MailDomain, error) {
	key, err := NewDKIMKey()
	if err != nil {
		return nil, err
	}
	enc, err := m.box.Seal(key)
	if err != nil {
		return nil, err
	}
	md := &db.MailDomain{DomainID: d.ID, Name: d.Name, DKIMSelector: DKIMSelector, DKIMKeyEnc: enc}
	if err := m.db.CreateMailDomain(ctx, md); err != nil {
		return nil, err
	}
	if err := m.addRecords(ctx, d, key); err != nil {
		m.log.Warn("mail: dns records", "domain", d.Name, "err", err)
	}
	m.Changed()
	return md, nil
}

// addRecords puts MX/SPF/DKIM/DMARC in the domain's zone, keeping any MX,
// SPF or DMARC the owner already has (two SPF records break SPF).
func (m *Manager) addRecords(ctx context.Context, d *db.Domain, key string) error {
	if m.dns == nil {
		return nil
	}
	z, err := m.dns.ZoneFor(ctx, d.Name)
	if err != nil || z == nil {
		return err
	}
	s, err := m.db.GetMailSettings(ctx)
	if err != nil {
		return err
	}
	host := s.Hostname
	if host == "" {
		host = "mail." + d.Name
	}
	dkim, err := DKIMRecord(key)
	if err != nil {
		return err
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(d.Name, z.Name), ".")
	existing, err := m.db.ListDNSRecords(ctx, z.ID)
	if err != nil {
		return err
	}
	var add []db.DNSRecord
	for _, r := range Records(prefix, host, DKIMSelector, dkim, d.ID) {
		if slices.ContainsFunc(existing, func(e db.DNSRecord) bool { return clashes(e, r) }) {
			continue
		}
		r.ZoneID = z.ID
		id := d.ID
		r.DomainID = &id // removed with the website
		add = append(add, r)
	}
	if err := m.db.CreateDNSRecords(ctx, add); err != nil {
		return err
	}
	m.dns.Changed()
	return nil
}

func clashes(e, r db.DNSRecord) bool {
	if e.Name != r.Name {
		return false
	}
	switch {
	case r.Type == "MX":
		return e.Type == "MX" || e.Type == "CNAME"
	case strings.HasPrefix(r.Content, "v=spf1"):
		return e.Type == "TXT" && strings.HasPrefix(e.Content, "v=spf1")
	case strings.HasPrefix(r.Content, "v=DMARC1"), strings.HasPrefix(r.Content, "v=DKIM1"):
		return e.Type == "TXT" || e.Type == "CNAME"
	}
	return false
}

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]*$`)

// DisableDomain stops mail for a website and deletes its mailboxes' mail.
func (m *Manager) DisableDomain(ctx context.Context, md *db.MailDomain) error {
	if err := m.db.DeleteDNSRecordsBySource(ctx, Source(md.DomainID)); err != nil {
		return err
	}
	if err := m.db.DeleteMailDomain(ctx, md.ID); err != nil {
		return err
	}
	if m.dns != nil {
		m.dns.Changed()
	}
	m.Sync(ctx)
	m.removeMail(ctx, md.Name, "")
	return nil
}

// DomainDeleted removes the stored mail of a deleted website.
func (m *Manager) DomainDeleted(ctx context.Context, name string) {
	m.Sync(ctx)
	m.removeMail(ctx, name, "")
}

// MailboxDeleted removes a deleted mailbox's stored mail.
func (m *Manager) MailboxDeleted(ctx context.Context, domain, local string) {
	m.Sync(ctx)
	m.removeMail(ctx, domain, local)
}

// removeMail deletes /var/mail/<domain>[/<local>] inside the container.
func (m *Manager) removeMail(ctx context.Context, domain, local string) {
	if m.srv == nil || !safeName.MatchString(domain) || (local != "" && !safeName.MatchString(local)) || strings.Contains(domain+local, "..") {
		return
	}
	if running, _ := m.srv.MailStatus(ctx); !running {
		return
	}
	p := "/var/mail/" + domain
	if local != "" {
		p += "/" + local
	}
	if _, err := m.srv.MailExec(ctx, "rm", "-rf", "--", p); err != nil {
		m.log.Warn("mail: remove stored mail", "path", p, "err", err)
	}
}

// Usage returns the storage each mailbox uses, in MB (empty if unavailable).
func (m *Manager) Usage(ctx context.Context) map[string]float64 {
	out := map[string]float64{}
	if m.srv == nil {
		return out
	}
	if running, _ := m.srv.MailStatus(ctx); !running {
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	text, err := m.srv.MailExec(cctx, "doveadm", "-f", "tab", "quota", "get", "-A")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) >= 4 && strings.EqualFold(f[2], "STORAGE") {
			if kb, err := strconv.ParseFloat(f[3], 64); err == nil {
				out[f[0]] = kb / 1024
			}
		}
	}
	return out
}
