package mail

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/secrets"
)

func TestValidation(t *testing.T) {
	for in, ok := range map[string]bool{"info": true, "John.Doe": true, "a+b_c-d": true, ".x": false, "x.": false, "a..b": false, "a b": false, "ñ": false, "": false} {
		if _, err := NormalizeLocal(in); (err == nil) != ok {
			t.Errorf("NormalizeLocal(%q) err=%v", in, err)
		}
	}
	for in, ok := range map[string]bool{"Me@Example.com": true, "a@b.co": true, "a@b": false, "a@@b.com": false, "a@b..com": false, "a b@c.com": false} {
		if _, err := NormalizeAddress(in); (err == nil) != ok {
			t.Errorf("NormalizeAddress(%q) err=%v", in, err)
		}
	}
	if CheckPassword("short") == nil || CheckPassword("long enough pass") != nil || CheckPassword("tab\there-123") == nil {
		t.Error("password policy")
	}
}

func TestFiles(t *testing.T) {
	key, err := NewDKIMKey()
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{
		Domains:   []DomainConfig{{Name: "example.com", Selector: "opx", KeyPEM: key}},
		Mailboxes: []MailboxConfig{{Address: "info@example.com", Hash: HashPassword("correct horse battery"), QuotaMB: 500}},
		Aliases:   []AliasConfig{{Address: "sales@example.com", Destinations: []string{"info@example.com", "boss@other.net"}}},
	}
	files, err := Files(snap)
	if err != nil {
		t.Fatal(err)
	}
	if acc := string(files["postfix-accounts.cf"]); !strings.HasPrefix(acc, "info@example.com|{SHA512-CRYPT}$6$rounds=50000$") {
		t.Errorf("accounts = %q", acc)
	}
	if q := string(files["dovecot-quotas.cf"]); q != "info@example.com:500M\n" {
		t.Errorf("quotas = %q", q)
	}
	if v := string(files["postfix-virtual.cf"]); v != "sales@example.com info@example.com,boss@other.net\n" {
		t.Errorf("virtual = %q", v)
	}
	if c := string(files["rspamd/override.d/dkim_signing.conf"]); !strings.Contains(c, `path = "/tmp/docker-mailserver/rspamd/dkim/example.com.opx.key";`) {
		t.Errorf("dkim conf = %s", c)
	}
	bad := []Snapshot{
		{Mailboxes: []MailboxConfig{{Address: "x@example.com\nevil@x.com|{SHA512-CRYPT}$6$a$b", Hash: HashPassword("correct horse battery"), QuotaMB: 1}}},
		{Mailboxes: []MailboxConfig{{Address: "x@example.com", Hash: "plain", QuotaMB: 1}}},
		{Aliases: []AliasConfig{{Address: "x@example.com", Destinations: []string{"a@b.com y@z.com"}}}},
		{Domains: []DomainConfig{{Name: "example.com\"; }", Selector: "opx", KeyPEM: key}}},
	}
	for i, s := range bad {
		if _, err := Files(s); err == nil {
			t.Errorf("bad snapshot %d rendered", i)
		}
	}

	dir := t.TempDir()
	if changed, err := WriteFiles(dir, files); err != nil || !changed {
		t.Fatalf("first write: %v %v", changed, err)
	}
	if changed, _ := WriteFiles(dir, files); changed {
		t.Error("unchanged files rewritten (the container would reload for nothing)")
	}
	delete(files, "rspamd/dkim/example.com.opx.key")
	if _, err := WriteFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rspamd", "dkim", "example.com.opx.key")); !os.IsNotExist(err) {
		t.Error("key of a removed domain kept")
	}
	txt, err := DKIMRecord(key)
	if err != nil || !strings.HasPrefix(txt, "v=DKIM1; k=rsa; p=MII") {
		t.Errorf("DKIM record = %q %v", txt, err)
	}
}

func TestRecords(t *testing.T) {
	apex := Records("", "mail.example.com", "opx", "v=DKIM1; p=x", 7)
	sub := Records("shop", "mail.example.com", "opx", "v=DKIM1; p=x", 8)
	names := func(rs []db.DNSRecord) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Name+" "+r.Type)
		}
		return strings.Join(out, ", ")
	}
	if got := names(apex); got != "@ MX, @ TXT, opx._domainkey TXT, _dmarc TXT" {
		t.Errorf("apex = %s", got)
	}
	if got := names(sub); got != "shop MX, shop TXT, opx._domainkey.shop TXT, _dmarc.shop TXT" {
		t.Errorf("subdomain = %s", got)
	}
	if apex[0].Source != "mail:7" || apex[0].Content != "mail.example.com" || apex[0].Priority != 10 {
		t.Errorf("MX = %+v", apex[0])
	}
	userSPF := db.DNSRecord{Name: "@", Type: "TXT", Content: "v=spf1 include:_spf.google.com ~all"}
	if !clashes(userSPF, apex[1]) || clashes(userSPF, apex[2]) || clashes(db.DNSRecord{Name: "www", Type: "MX"}, apex[0]) {
		t.Error("clash rules")
	}
}

type fakeDNS struct {
	store   *db.DB
	changed int
}

func (f *fakeDNS) ZoneFor(ctx context.Context, host string) (*db.DNSZone, error) {
	zones, _ := f.store.ListDNSZones(ctx)
	for _, z := range zones {
		if host == z.Name || strings.HasSuffix(host, "."+z.Name) {
			return z, nil
		}
	}
	return nil, nil
}
func (f *fakeDNS) Changed() { f.changed++ }

type fakeServer struct {
	running  bool
	hostname string
	execs    []string
}

func (f *fakeServer) EnsureMail(_ context.Context, host, _ string) error {
	f.running, f.hostname = true, host
	return nil
}
func (f *fakeServer) StopMail(context.Context) error            { f.running = false; return nil }
func (f *fakeServer) MailStatus(context.Context) (bool, string) { return f.running, "healthy" }
func (f *fakeServer) MailExec(_ context.Context, c ...string) (string, error) {
	f.execs = append(f.execs, strings.Join(c, " "))
	return "", nil
}

func TestManager(t *testing.T) {
	ctx := context.Background()
	store := db.OpenMemory(t)
	box, _ := secrets.New(make([]byte, 32), "mail-test")
	u := &db.User{Username: "o", PasswordHash: "x", Role: db.RoleCustomer}
	_ = store.CreateUser(ctx, u)
	d := &db.Domain{OwnerID: u.ID, Name: "example.com", Kind: db.KindPrimary, Runtime: "static", Status: db.StatusRunning}
	if err := store.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	z := &db.DNSZone{DomainID: d.ID, Name: "example.com"}
	_ = store.CreateDNSZone(ctx, z, []db.DNSRecord{{Name: "@", Type: "TXT", Content: "v=spf1 include:other.net ~all", TTL: 3600}})
	srv, fdns := &fakeServer{}, &fakeDNS{store: store}
	dir := t.TempDir()
	m := New(store, srv, fdns, box, dir, dir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := m.UpdateSettings(ctx, db.MailSettings{Enabled: true}); err == nil {
		t.Error("enabled without a host name")
	}
	if err := m.UpdateSettings(ctx, db.MailSettings{Enabled: true, Hostname: "mail.example.com"}); err != nil {
		t.Fatal(err)
	}
	md, err := m.EnableDomain(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := store.ListDNSRecords(ctx, z.ID)
	var spf, mx, dkim int
	for _, r := range recs {
		switch {
		case r.Type == "TXT" && strings.HasPrefix(r.Content, "v=spf1"):
			spf++
		case r.Type == "MX" && r.Content == "mail.example.com":
			mx++
		case r.Name == "opx._domainkey":
			dkim++
		}
	}
	if spf != 1 || mx != 1 || dkim != 1 {
		t.Errorf("records: spf=%d mx=%d dkim=%d (%+v)", spf, mx, dkim, recs)
	}
	_ = store.CreateMailbox(ctx, &db.Mailbox{MailDomainID: md.ID, LocalPart: "info", PasswordHash: HashPassword("correct horse battery"), QuotaMB: 100, Enabled: true})
	_ = store.CreateMailbox(ctx, &db.Mailbox{MailDomainID: md.ID, LocalPart: "off", PasswordHash: HashPassword("correct horse battery"), QuotaMB: 100})
	m.Sync(ctx)
	if st, _ := m.Status(ctx); st.LastError != "" || !srv.running || srv.hostname != "mail.example.com" || st.Certificate != "self-signed" {
		t.Fatalf("status = %+v", st)
	}
	acc, _ := os.ReadFile(filepath.Join(dir, "mail", "config", "postfix-accounts.cf"))
	if !strings.Contains(string(acc), "info@example.com|") || strings.Contains(string(acc), "off@") {
		t.Errorf("accounts file = %s", acc)
	}

	// A Caddy certificate replaces the temporary one.
	cdir := filepath.Join(dir, "data", "caddy", "certificates", "acme-v02.api.letsencrypt.org-directory", "mail.example.com")
	_ = os.MkdirAll(cdir, 0o755)
	_ = os.WriteFile(filepath.Join(cdir, "mail.example.com.crt"), []byte("CERT"), 0o644)
	_ = os.WriteFile(filepath.Join(cdir, "mail.example.com.key"), []byte("KEY"), 0o600)
	m.Sync(ctx)
	if c, _ := os.ReadFile(filepath.Join(dir, "mail", "config", "ssl", "cert.pem")); string(c) != "CERT" {
		t.Errorf("certificate not taken from Caddy: %q", c)
	}

	// New host name: MX follows.
	_ = m.UpdateSettings(ctx, db.MailSettings{Enabled: true, Hostname: "mx.example.com"})
	recs, _ = store.ListDNSRecords(ctx, z.ID)
	for _, r := range recs {
		if r.Type == "MX" && r.Content != "mx.example.com" {
			t.Errorf("MX not moved: %+v", r)
		}
	}

	if err := m.DisableDomain(ctx, md); err != nil {
		t.Fatal(err)
	}
	recs, _ = store.ListDNSRecords(ctx, z.ID)
	if len(recs) != 1 {
		t.Errorf("mail records left after disabling: %+v", recs)
	}
	if len(srv.execs) == 0 || srv.execs[len(srv.execs)-1] != "rm -rf -- /var/mail/example.com" {
		t.Errorf("stored mail not removed: %v", srv.execs)
	}
	_ = m.UpdateSettings(ctx, db.MailSettings{Hostname: "mx.example.com"})
	m.Sync(ctx)
	if srv.running {
		t.Error("server still running after turning mail off")
	}
}
