package dns

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"openplex.dev/openplex/internal/db"
)

func TestNormalizeRecord(t *testing.T) {
	zone := "example.com"
	cases := []struct {
		in      db.DNSRecord
		name    string
		content string
		ok      bool
	}{
		{db.DNSRecord{Name: "", Type: "a", Content: " 203.0.113.10 "}, "@", "203.0.113.10", true},
		{db.DNSRecord{Name: "WWW.example.com.", Type: "AAAA", Content: "2001:DB8::1"}, "www", "2001:db8::1", true},
		{db.DNSRecord{Name: "*.dev", Type: "A", Content: "203.0.113.10"}, "*.dev", "203.0.113.10", true},
		{db.DNSRecord{Name: "www", Type: "A", Content: "2001:db8::1"}, "", "", false},
		{db.DNSRecord{Name: "a b", Type: "A", Content: "203.0.113.10"}, "", "", false},
		{db.DNSRecord{Name: "x.*", Type: "A", Content: "203.0.113.10"}, "", "", false},
		{db.DNSRecord{Name: "www", Type: "CNAME", Content: "@"}, "www", "example.com", true},
		{db.DNSRecord{Name: "@", Type: "CNAME", Content: "other.net"}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "MX", Content: "mail.example.com.", Priority: 10}, "@", "mail.example.com", true},
		{db.DNSRecord{Name: "@", Type: "MX", Content: "mail", Priority: 10}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "MX", Content: "mail.example.com", Priority: 70000}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "TXT", Content: `"v=spf1 mx -all"`}, "@", "v=spf1 mx -all", true},
		{db.DNSRecord{Name: "_dmarc", Type: "TXT", Content: "v=DMARC1; p=none"}, "_dmarc", "v=DMARC1; p=none", true},
		{db.DNSRecord{Name: "@", Type: "TXT", Content: "a\nb"}, "", "", false},
		{db.DNSRecord{Name: "_sip._tcp", Type: "SRV", Content: "5 5060 sip.example.com", Priority: 10}, "_sip._tcp", "5 5060 sip.example.com", true},
		{db.DNSRecord{Name: "sip", Type: "SRV", Content: "5 5060 sip.example.com"}, "", "", false},
		{db.DNSRecord{Name: "_sip._tcp", Type: "SRV", Content: "5 99999 sip.example.com"}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "CAA", Content: `0 issue "letsencrypt.org"`}, "@", "0 issue letsencrypt.org", true},
		{db.DNSRecord{Name: "@", Type: "CAA", Content: "0 bogus letsencrypt.org"}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "NS", Content: "ns1.other.net"}, "", "", false},
		{db.DNSRecord{Name: "lab", Type: "NS", Content: "ns1.other.net"}, "lab", "ns1.other.net", true},
		{db.DNSRecord{Name: "@", Type: "PTR", Content: "x.example.com"}, "", "", false},
		{db.DNSRecord{Name: "@", Type: "A", Content: "203.0.113.10", TTL: 5}, "", "", false},
	}
	for _, c := range cases {
		r := c.in
		err := NormalizeRecord(&r, zone)
		if (err == nil) != c.ok {
			t.Errorf("NormalizeRecord(%+v) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && (r.Name != c.name || r.Content != c.content || r.TTL != DefaultTTL && c.in.TTL == 0) {
			t.Errorf("NormalizeRecord(%+v) = %q %q", c.in, r.Name, r.Content)
		}
	}
}

func TestConflicts(t *testing.T) {
	others := []db.DNSRecord{{ID: 1, Name: "www", Type: "CNAME", Content: "example.com"}, {ID: 2, Name: "@", Type: "A", Content: "203.0.113.1"}}
	if CheckConflicts(db.DNSRecord{Name: "www", Type: "A", Content: "203.0.113.1"}, others) == nil {
		t.Error("A next to CNAME accepted")
	}
	if CheckConflicts(db.DNSRecord{ID: 1, Name: "www", Type: "CNAME", Content: "other.example.com"}, others) != nil {
		t.Error("editing the CNAME itself refused")
	}
	if CheckConflicts(db.DNSRecord{Name: "@", Type: "A", Content: "203.0.113.1"}, others) == nil {
		t.Error("duplicate accepted")
	}
	if CheckConflicts(db.DNSRecord{Name: "@", Type: "A", Content: "203.0.113.2"}, others) != nil {
		t.Error("second A refused")
	}
}

func TestQuoteTXT(t *testing.T) {
	if got := quoteTXT(`say "hi" \o/`); got != `"say \"hi\" \\o/"` {
		t.Errorf("quote = %s", got)
	}
	long := strings.Repeat("k", 300)
	if got := quoteTXT(long); !strings.HasPrefix(got, `"`+strings.Repeat("k", 255)+`" "kkk`) {
		t.Errorf("long TXT not split: %s", got[:20])
	}
}

func TestDesired(t *testing.T) {
	s := db.DNSSettings{NS1: "ns1.example.com", NS2: "ns2.other.net", IPv4: "203.0.113.10", IPv6: "2001:db8::10"}
	recs := []db.DNSRecord{
		{Name: "@", Type: "A", Content: "203.0.113.10", TTL: 3600},
		{Name: "@", Type: "MX", Content: "mail.example.com", Priority: 10, TTL: 300},
		{Name: "@", Type: "MX", Content: "backup.example.com", Priority: 20, TTL: 3600},
		{Name: "@", Type: "CAA", Content: "0 issue letsencrypt.org", TTL: 3600},
	}
	sets := Desired(ZoneInput{Zone: "example.com", Settings: s, Records: recs})
	got := map[string]RRSet{}
	for _, x := range sets {
		got[x.key()] = x
	}
	if ns := got["example.com.|NS"]; len(ns.Records) != 2 || ns.Records[0] != "ns1.example.com." {
		t.Errorf("NS = %+v", ns)
	}
	if mx := got["example.com.|MX"]; mx.TTL != 300 || len(mx.Records) != 2 || mx.Records[0] != "10 mail.example.com." {
		t.Errorf("MX = %+v", mx)
	}
	if g := got["ns1.example.com.|A"]; len(g.Records) != 1 || g.Records[0] != "203.0.113.10" {
		t.Errorf("glue A = %+v", g)
	}
	if _, ok := got["ns2.other.net.|A"]; ok {
		t.Error("glue rendered for a nameserver outside the zone")
	}
	if caa := got["example.com.|CAA"]; caa.Records[0] != `0 issue "letsencrypt.org"` {
		t.Errorf("CAA = %+v", caa)
	}
	if !strings.HasPrefix(SOAContent(s, "example.com", 1), "ns1.example.com. hostmaster.example.com. 1 ") {
		t.Errorf("SOA = %s", SOAContent(s, "example.com", 1))
	}
	s.Hostmaster = "dns.admin@example.org"
	if h := Hostmaster(s, "example.com"); h != `dns\.admin.example.org.` {
		t.Errorf("hostmaster = %s", h)
	}
}

// fakePDNS is an in-memory PowerDNS API.
type fakePDNS struct {
	mu    sync.Mutex
	zones map[string]map[string]apiRRSet // zone -> name|type -> rrset
}

func (f *fakePDNS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-Key") == "" {
		w.WriteHeader(401)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/servers/localhost")
	switch {
	case path == "":
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "localhost"})
	case path == "/zones" && r.Method == "GET":
		var out []apiZone
		for n := range f.zones {
			out = append(out, apiZone{Name: n})
		}
		_ = json.NewEncoder(w).Encode(out)
	case path == "/zones" && r.Method == "POST":
		var z apiZone
		_ = json.NewDecoder(r.Body).Decode(&z)
		m := map[string]apiRRSet{}
		for _, s := range z.RRSets {
			m[s.Name+"|"+s.Type] = s
		}
		f.zones[z.Name] = m
		w.WriteHeader(201)
	case strings.HasPrefix(path, "/zones/"):
		name := strings.TrimPrefix(path, "/zones/")
		m, ok := f.zones[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case "GET":
			z := apiZone{Name: name}
			for _, s := range m {
				z.RRSets = append(z.RRSets, s)
			}
			_ = json.NewEncoder(w).Encode(z)
		case "PATCH":
			var z apiZone
			_ = json.NewDecoder(r.Body).Decode(&z)
			for _, s := range z.RRSets {
				if s.ChangeType == "DELETE" {
					delete(m, s.Name+"|"+s.Type)
				} else {
					s.ChangeType = ""
					m[s.Name+"|"+s.Type] = s
				}
			}
			w.WriteHeader(204)
		case "DELETE":
			delete(f.zones, name)
			w.WriteHeader(204)
		}
	}
}

type fakeServer struct{ running bool }

func (f *fakeServer) EnsureDNS(context.Context, string, []netip.Addr) error {
	f.running = true
	return nil
}
func (f *fakeServer) StopDNS(context.Context) error   { f.running = false; return nil }
func (f *fakeServer) DNSRunning(context.Context) bool { return f.running }

func TestSync(t *testing.T) {
	ctx := context.Background()
	store := db.OpenMemory(t)
	fake := &fakePDNS{zones: map[string]map[string]apiRRSet{"stale.test.": {}}}
	ts := httptest.NewServer(fake)
	defer ts.Close()
	srv := &fakeServer{}
	m := New(store, srv, t.TempDir(), ts.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.addrs = func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("192.168.1.5"), netip.MustParseAddr("203.0.113.10")}
	}

	u := &db.User{Username: "o", PasswordHash: "x", Role: db.RoleCustomer}
	if err := store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	site := &db.Domain{OwnerID: u.ID, Name: "example.com", Kind: db.KindPrimary, Runtime: "static", Status: db.StatusRunning}
	if err := store.CreateDomain(ctx, site); err != nil {
		t.Fatal(err)
	}
	if err := m.initSettings(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := store.GetDNSSettings(ctx)
	if s.IPv4 != "203.0.113.10" || s.IPv6 != "" {
		t.Fatalf("detected addresses = %q %q", s.IPv4, s.IPv6)
	}
	if err := m.UpdateSettings(ctx, db.DNSSettings{Enabled: true, NS1: "ns1.example.com", NS2: "ns2.example.com", IPv4: "203.0.113.10"}); err != nil {
		t.Fatal(err)
	}
	m.reconcile(ctx)
	sub := &db.Domain{OwnerID: u.ID, Name: "blog.example.com", Kind: db.KindSubdomain, ParentID: &site.ID, Runtime: "static", Status: db.StatusRunning}
	if err := store.CreateDomain(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := m.DomainCreated(ctx, sub); err != nil {
		t.Fatal(err)
	}
	m.reconcile(ctx)
	st, _ := m.Status(ctx)
	if st.LastError != "" || !st.Running {
		t.Fatalf("status = %+v", st)
	}
	fake.mu.Lock()
	z := fake.zones["example.com."]
	_, stale := fake.zones["stale.test."]
	fake.mu.Unlock()
	for _, k := range []string{"example.com.|SOA", "example.com.|NS", "example.com.|A", "www.example.com.|CNAME", "ns1.example.com.|A", "blog.example.com.|A"} {
		if _, ok := z[k]; !ok {
			t.Errorf("zone lacks %s: %v", k, z)
		}
	}
	if stale {
		t.Error("zone not in the panel was kept")
	}

	// Moving the server: records with the old address follow, SOA changes.
	_ = m.UpdateSettings(ctx, db.DNSSettings{Enabled: true, NS1: "ns1.example.com", NS2: "ns2.example.com", IPv4: "198.51.100.7", Hostmaster: "me@example.org"})
	m.reconcile(ctx)
	fake.mu.Lock()
	z = fake.zones["example.com."]
	a, soa := z["blog.example.com.|A"], z["example.com.|SOA"]
	fake.mu.Unlock()
	if len(a.Records) != 1 || a.Records[0].Content != "198.51.100.7" {
		t.Errorf("subdomain A after move = %+v", a)
	}
	if !strings.Contains(soa.Records[0].Content, "me.example.org.") || !strings.Contains(soa.Records[0].Content, " 2 ") {
		t.Errorf("SOA after change = %+v", soa)
	}

	// Deleting the site removes its zone from PowerDNS.
	if err := store.DeleteDomain(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	m.reconcile(ctx)
	fake.mu.Lock()
	_, kept := fake.zones["example.com."]
	fake.mu.Unlock()
	if kept {
		t.Error("deleted website's zone still served")
	}

	// Turning the server off stops it.
	_ = m.UpdateSettings(ctx, db.DNSSettings{NS1: "ns1.example.com", NS2: "ns2.example.com", IPv4: "198.51.100.7"})
	m.reconcile(ctx)
	if srv.running {
		t.Error("server still running after turning it off")
	}
}

func TestSettingsValidation(t *testing.T) {
	m := New(db.OpenMemory(t), nil, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	bad := []db.DNSSettings{
		{NS1: "ns1", NS2: "ns2.example.com"},
		{NS1: "ns1.example.com", NS2: "ns1.example.com"},
		{IPv4: "2001:db8::1"},
		{IPv6: "203.0.113.1"},
		{Hostmaster: "not an email"},
		{Enabled: true, NS1: "ns1.example.com", NS2: "ns2.example.com"},
	}
	for _, s := range bad {
		if err := m.UpdateSettings(context.Background(), s); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
}

func TestDelegation(t *testing.T) {
	store := db.OpenMemory(t)
	m := New(store, nil, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = store.SaveDNSSettings(context.Background(), &db.DNSSettings{NS1: "ns1.example.com", NS2: "ns2.example.com"})
	answers := map[string][]string{"ours.test.": {"NS1.example.com.", "ns2.example.com."}, "half.test.": {"ns1.example.com.", "ns.other.net."}, "other.test.": {"a.dns-parking.com."}}
	m.lookupNS = func(_ context.Context, name string) ([]*net.NS, error) {
		hosts, ok := answers[name]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []*net.NS
		for _, h := range hosts {
			out = append(out, &net.NS{Host: h})
		}
		return out, nil
	}
	for zone, want := range map[string]string{"ours.test": "ok", "half.test": "partial", "other.test": "other", "missing.test": "none"} {
		if got := m.Delegation(context.Background(), zone); got.Status != want {
			t.Errorf("Delegation(%s) = %+v, want %s", zone, got, want)
		}
	}
}
