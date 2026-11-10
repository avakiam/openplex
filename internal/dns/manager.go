package dns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/db"
)

// Server runs the PowerDNS container (the Docker orchestrator).
type Server interface {
	EnsureDNS(ctx context.Context, apiKey string, listen []netip.Addr) error
	StopDNS(ctx context.Context) error
	DNSRunning(ctx context.Context) bool
}

// Status is the DNS service as the dashboard shows it.
type Status struct {
	db.DNSSettings
	Available bool         `json:"available"` // Docker is there to run the server
	Running   bool         `json:"running"`
	LastError string       `json:"last_error"`
	LastSync  *time.Time   `json:"last_sync"`
	Listen    []netip.Addr `json:"listen"`
}

// Delegation says whether the world's DNS points a zone at this server.
type Delegation struct {
	Status  string    `json:"status"` // ok | partial | other | none
	Servers []string  `json:"servers"`
	Checked time.Time `json:"checked_at"`
}

// Manager keeps zones in the database and PowerDNS in sync.
type Manager struct {
	db      *db.DB
	srv     Server // nil without Docker
	log     *slog.Logger
	dataDir string
	apiURL  string
	kick    chan struct{}

	mu       sync.Mutex
	lastErr  string
	lastSync *time.Time
	deleg    map[string]Delegation
	lookupNS func(ctx context.Context, name string) ([]*net.NS, error)
	addrs    func() []netip.Addr
}

// New creates a Manager; srv may be nil.
func New(store *db.DB, srv Server, dataDir, apiURL string, log *slog.Logger) *Manager {
	return &Manager{
		db: store, srv: srv, log: log, dataDir: dataDir, apiURL: apiURL,
		kick: make(chan struct{}, 1), deleg: map[string]Delegation{},
		lookupNS: parentNS, addrs: hostAddrs,
	}
}

// Changed asks for a sync soon (non-blocking).
func (m *Manager) Changed() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run reconciles at start, on every change and every 10 minutes.
func (m *Manager) Run(ctx context.Context) {
	if err := m.initSettings(ctx); err != nil {
		m.log.Error("dns: settings", "err", err)
	}
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		m.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
			time.Sleep(300 * time.Millisecond) // coalesce bursts of edits
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
	t := time.Now().UTC()
	m.lastErr, m.lastSync = "", &t
}

// initSettings fills in the server addresses on first use.
func (m *Manager) initSettings(ctx context.Context) error {
	s, err := m.db.GetDNSSettings(ctx)
	if err != nil || s.IPv4 != "" || s.IPv6 != "" {
		return err
	}
	s.IPv4, s.IPv6 = defaultAddrs(m.addrs())
	return m.db.SaveDNSSettings(ctx, s)
}

func (m *Manager) reconcile(ctx context.Context) {
	if err := m.EnsureZones(ctx); err != nil {
		m.log.Error("dns: ensure zones", "err", err)
	}
	s, err := m.db.GetDNSSettings(ctx)
	if err != nil {
		m.setErr(err)
		return
	}
	if m.srv == nil {
		m.setErr(errors.New("Docker is not available on this server."))
		return
	}
	if !s.Enabled {
		if m.srv.DNSRunning(ctx) {
			if err := m.srv.StopDNS(ctx); err != nil {
				m.log.Error("dns: stop server", "err", err)
			}
		}
		m.setErr(nil)
		return
	}
	err = m.sync(ctx, s)
	if err != nil {
		m.log.Error("dns: sync", "err", err)
	}
	m.setErr(err)
}

func (m *Manager) client() (*pdns, error) {
	key, err := m.apiKey()
	if err != nil {
		return nil, err
	}
	return &pdns{base: m.apiURL, key: key, http: &http.Client{}}, nil
}

func (m *Manager) apiKey() (string, error) {
	p := filepath.Join(m.dataDir, "dns", "api.key")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := hex.EncodeToString(raw)
	return key, os.WriteFile(p, []byte(key), 0o600)
}

func (m *Manager) sync(ctx context.Context, s *db.DNSSettings) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	listen := ListenAddrs(m.addrs())
	if err := m.srv.EnsureDNS(ctx, api.key, listen); err != nil {
		return fmt.Errorf("start the DNS server: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err = api.ping(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("the DNS server does not answer: %w", err)
		}
		time.Sleep(time.Second)
	}
	var hosts []string
	if ms, err := m.db.GetMailSettings(ctx); err == nil && ms.Enabled && ms.Hostname != "" {
		hosts = append(hosts, ms.Hostname)
	}
	zones, err := m.db.ListDNSZones(ctx)
	if err != nil {
		return err
	}
	existing, err := api.listZones(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, z := range zones {
		recs, err := m.db.ListDNSRecords(ctx, z.ID)
		if err != nil {
			return err
		}
		desired := Desired(ZoneInput{Zone: z.Name, Settings: *s, Records: recs, Hosts: hosts})
		soa := SOAContent(*s, z.Name, 1)
		if !slices.Contains(existing, z.Name) {
			if err := api.createZone(ctx, z.Name, desired, soa); err != nil {
				errs = append(errs, fmt.Errorf("zone %s: %w", z.Name, err))
			}
			continue
		}
		cur, err := api.getZone(ctx, z.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("zone %s: %w", z.Name, err))
			continue
		}
		if err := api.patch(ctx, z.Name, diff(z.Name, cur.RRSets, desired, soa)); err != nil {
			errs = append(errs, fmt.Errorf("zone %s: %w", z.Name, err))
		}
	}
	for _, name := range existing {
		if !slices.ContainsFunc(zones, func(z *db.DNSZone) bool { return strings.EqualFold(z.Name, name) }) {
			if err := api.deleteZone(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("remove zone %s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// EnsureZones creates missing zones for primary websites and aliases (with
// default records plus records for subdomain websites already under them).
func (m *Manager) EnsureZones(ctx context.Context) error {
	domains, err := m.db.ListDomains(ctx, db.DomainFilter{})
	if err != nil {
		return err
	}
	s, err := m.db.GetDNSSettings(ctx)
	if err != nil {
		return err
	}
	for _, d := range domains {
		if d.Kind == db.KindSubdomain {
			continue
		}
		if _, err := m.db.GetDNSZoneByDomain(ctx, d.ID); err == nil {
			continue
		} else if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		recs := DefaultRecords(*s, d.Name)
		for _, sub := range domains {
			if sub.Kind == db.KindSubdomain && strings.HasSuffix(sub.Name, "."+d.Name) {
				recs = append(recs, SubdomainRecords(*s, strings.TrimSuffix(sub.Name, "."+d.Name), sub.ID)...)
			}
		}
		z := &db.DNSZone{DomainID: d.ID, Name: d.Name}
		if err := m.db.CreateDNSZone(ctx, z, recs); err != nil {
			if errors.Is(err, db.ErrConflict) {
				continue // a zone of that name already exists
			}
			return err
		}
		m.log.Info("dns: zone created", "zone", d.Name)
	}
	return nil
}

// DomainCreated adds DNS for a new website: a zone for primaries and
// aliases, address records in the enclosing zone for subdomains.
func (m *Manager) DomainCreated(ctx context.Context, d *db.Domain) error {
	defer m.Changed()
	if d.Kind != db.KindSubdomain {
		return m.EnsureZones(ctx)
	}
	z, err := m.ZoneFor(ctx, d.Name)
	if err != nil || z == nil {
		return err
	}
	s, err := m.db.GetDNSSettings(ctx)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(d.Name, "."+z.Name)
	return m.db.CreateDNSRecords(ctx, withZone(SubdomainRecords(*s, name, d.ID), z.ID))
}

func withZone(recs []db.DNSRecord, zoneID int64) []db.DNSRecord {
	for i := range recs {
		recs[i].ZoneID = zoneID
	}
	return recs
}

// ZoneFor returns the zone that holds host (longest matching suffix), or nil.
func (m *Manager) ZoneFor(ctx context.Context, host string) (*db.DNSZone, error) {
	zones, err := m.db.ListDNSZones(ctx)
	if err != nil {
		return nil, err
	}
	var best *db.DNSZone
	for _, z := range zones {
		if (host == z.Name || strings.HasSuffix(host, "."+z.Name)) && (best == nil || len(z.Name) > len(best.Name)) {
			best = z
		}
	}
	return best, nil
}

// Status returns the service state.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	s, err := m.db.GetDNSSettings(ctx)
	if err != nil {
		return nil, err
	}
	st := &Status{DNSSettings: *s, Available: m.srv != nil, Listen: ListenAddrs(m.addrs())}
	if m.srv != nil {
		st.Running = m.srv.DNSRunning(ctx)
	}
	m.mu.Lock()
	st.LastError, st.LastSync = m.lastErr, m.lastSync
	m.mu.Unlock()
	return st, nil
}

// UpdateSettings validates and stores new settings. Records that pointed
// at the previous server address are moved to the new one.
func (m *Manager) UpdateSettings(ctx context.Context, in db.DNSSettings) error {
	in.NS1 = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.NS1)), ".")
	in.NS2 = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.NS2)), ".")
	in.Hostmaster = strings.TrimSpace(in.Hostmaster)
	for _, ns := range []string{in.NS1, in.NS2} {
		if ns != "" && !ValidHost(ns) {
			return errors.New("enter the nameservers as host names, such as ns1.example.com")
		}
	}
	if in.NS1 != "" && in.NS1 == in.NS2 {
		return errors.New("the two nameservers must be different names")
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(in.IPv4)); in.IPv4 != "" && (err != nil || !a.Is4()) {
		return errors.New("enter an IPv4 address, such as 203.0.113.10")
	} else if err == nil {
		in.IPv4 = a.String()
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(in.IPv6)); in.IPv6 != "" && (err != nil || !a.Is6() || a.Is4In6()) {
		return errors.New("enter an IPv6 address, such as 2001:db8::10")
	} else if err == nil {
		in.IPv6 = a.String()
	}
	if in.Hostmaster != "" {
		if a, err := mail.ParseAddress(in.Hostmaster); err != nil || a.Address != in.Hostmaster || !strings.Contains(in.Hostmaster, "@") {
			return errors.New("enter a valid e-mail address for the hostmaster")
		}
	}
	if in.Enabled && (in.NS1 == "" || in.NS2 == "" || (in.IPv4 == "" && in.IPv6 == "")) {
		return errors.New("to turn the DNS server on, set two nameservers and the server's address")
	}
	old, err := m.db.GetDNSSettings(ctx)
	if err != nil {
		return err
	}
	if err := m.db.SaveDNSSettings(ctx, &in); err != nil {
		return err
	}
	for _, p := range [][3]string{{"A", old.IPv4, in.IPv4}, {"AAAA", old.IPv6, in.IPv6}} {
		if p[1] != "" && p[2] != "" && p[1] != p[2] {
			if _, err := m.db.ReplaceDNSAddress(ctx, p[0], p[1], p[2]); err != nil {
				return err
			}
		}
	}
	m.mu.Lock()
	m.deleg = map[string]Delegation{}
	m.mu.Unlock()
	m.Changed()
	return nil
}

// Delegation looks up the zone's delegation at its parent zone (cached 5 min).
func (m *Manager) Delegation(ctx context.Context, zone string) Delegation {
	m.mu.Lock()
	if d, ok := m.deleg[zone]; ok && time.Since(d.Checked) < 5*time.Minute {
		m.mu.Unlock()
		return d
	}
	m.mu.Unlock()
	s, _ := m.db.GetDNSSettings(ctx)
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	d := Delegation{Status: "none", Servers: []string{}, Checked: time.Now().UTC()}
	if nss, err := m.lookupNS(cctx, zone+"."); err == nil {
		for _, ns := range nss {
			d.Servers = append(d.Servers, strings.ToLower(strings.TrimSuffix(ns.Host, ".")))
		}
		slices.Sort(d.Servers)
		ours := 0
		for _, h := range d.Servers {
			if s != nil && (h == s.NS1 || h == s.NS2) {
				ours++
			}
		}
		switch {
		case len(d.Servers) == 0:
		case ours == len(d.Servers):
			d.Status = "ok"
		case ours > 0:
			d.Status = "partial"
		default:
			d.Status = "other"
		}
	}
	m.mu.Lock()
	m.deleg[zone] = d
	m.mu.Unlock()
	return d
}

// hostAddrs lists usable unicast addresses of the host's own interfaces
// (container bridges and virtual Ethernet pairs excluded).
func hostAddrs() []netip.Addr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			strings.HasPrefix(ifc.Name, "docker") || strings.HasPrefix(ifc.Name, "br-") || strings.HasPrefix(ifc.Name, "veth") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().IsGlobalUnicast() {
				out = append(out, p.Addr().Unmap())
			}
		}
	}
	return out
}

// ListenAddrs are the addresses PowerDNS answers on.
func ListenAddrs(addrs []netip.Addr) []netip.Addr {
	out := slices.Clone(addrs)
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(out)
}

// defaultAddrs picks the server address for records: public before private.
func defaultAddrs(addrs []netip.Addr) (v4, v6 string) {
	pick := func(want4 bool) string {
		var private string
		for _, a := range addrs {
			if a.Is4() != want4 {
				continue
			}
			if !a.IsPrivate() {
				return a.String()
			}
			if private == "" && want4 { // a private IPv6 (ULA) is never a useful default
				private = a.String()
			}
		}
		return private
	}
	return pick(true), pick(false)
}
